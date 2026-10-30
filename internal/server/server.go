// Package server wires the HTTP router: /healthz, the /v1 gateway, the /api
// admin API and the embedded GUI.
package server

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/MaziarAshtari/Switchyard/internal/admin"
	"github.com/MaziarAshtari/Switchyard/internal/gateway"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// New returns the root handler.
func New(gw *gateway.Gateway, api *admin.API, gui fs.FS, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})
	gw.Routes(r)
	api.Routes(r)
	r.NotFound(guiHandler(gui, log).ServeHTTP)
	return r
}

// guiHandler serves the SPA: existing files directly (hashed assets are
// cached forever), anything else falls back to index.html.
func guiHandler(gui fs.FS, log *slog.Logger) http.Handler {
	index, err := fs.ReadFile(gui, "index.html")
	if err != nil {
		log.Warn("GUI is not built (web/dist/index.html missing); run `make build`")
		index = []byte("<!doctype html><title>Switchyard</title><p>The GUI was not built. Run <code>make build</code>.</p>")
	}
	files := http.FileServerFS(gui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(gui, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					h.Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		h.Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

// Maintain deletes old request logs, video jobs and expired sessions at
// start and then every hour, until ctx is done.
func Maintain(ctx context.Context, st *store.Store, log *slog.Logger) {
	run := func() {
		set, err := st.Settings(ctx)
		if err != nil {
			log.Error("maintenance: load settings", "err", err)
			return
		}
		n, err := st.Prune(ctx, time.Duration(set.LogRetentionDays)*24*time.Hour)
		if err != nil {
			log.Error("maintenance: prune", "err", err)
			return
		}
		if n > 0 {
			log.Info("maintenance: deleted old request logs", "rows", n)
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
