// Command switchyard runs the Switchyard AI API gateway.
//
// Usage:
//
//	switchyard                  start the server
//	switchyard healthcheck      exit 0 if the local server answers /healthz
//	switchyard reset-password   set the admin password to $ADMIN_PASSWORD
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/admin"
	"github.com/MaziarAshtari/Switchyard/internal/config"
	"github.com/MaziarAshtari/Switchyard/internal/db"
	"github.com/MaziarAshtari/Switchyard/internal/gateway"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
	"github.com/MaziarAshtari/Switchyard/internal/server"
	"github.com/MaziarAshtari/Switchyard/internal/store"
	"github.com/MaziarAshtari/Switchyard/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "":
		err = run(log)
	case "healthcheck":
		err = healthcheck()
	case "reset-password":
		err = resetPassword(log)
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (expected healthcheck, reset-password or version)", cmd)
	}
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func openStore(cfg config.Config) (*store.Store, func(), error) {
	box, err := secret.NewBox(cfg.EncryptionKey)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	st := store.New(database, box)
	if err := st.CheckEncryptionKey(context.Background()); err != nil {
		database.Close()
		return nil, nil, err
	}
	return st, func() { database.Close() }, nil
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, closeDB, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer closeDB()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hash, err := st.PasswordHash(ctx)
	if err != nil {
		return err
	}
	if hash == "" {
		if len(cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD (at least 8 characters) is required on first start")
		}
		if err := st.SetPassword(ctx, cfg.AdminPassword); err != nil {
			return err
		}
		log.Info("admin password initialised from ADMIN_PASSWORD")
	}

	clients := proxy.NewClients()
	router := routing.NewRouter(time.Now, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())))
	logs := gateway.NewLogWriter(st, log)
	gw := gateway.New(st, router, clients, logs, log, version)
	api := admin.New(st, gw, clients, log, version)
	go server.Maintain(ctx, st, log)

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           server.New(gw, api, web.FS(), log),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No write timeout: responses may stream for minutes.
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("switchyard listening", "addr", srv.Addr, "version", version, "data_dir", cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	logs.Close()
	return err
}

func healthcheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}

func resetPassword(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if len(cfg.AdminPassword) < 8 {
		return errors.New("set ADMIN_PASSWORD (at least 8 characters) to the new password")
	}
	st, closeDB, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	if err := st.SetPassword(ctx, cfg.AdminPassword); err != nil {
		return err
	}
	if err := st.DeleteOtherSessions(ctx, ""); err != nil {
		return err
	}
	log.Info("admin password reset to ADMIN_PASSWORD; all sessions were signed out")
	return nil
}
