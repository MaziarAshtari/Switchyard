// Package admin serves the JSON API used by the GUI (/api/*).
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MaziarAshtari/Switchyard/internal/gateway"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

const (
	sessionCookie   = "switchyard_session"
	sessionLifetime = 30 * 24 * time.Hour
)

// API is the admin JSON API.
type API struct {
	store   *store.Store
	gw      *gateway.Gateway
	clients *proxy.Clients
	limiter *loginLimiter
	log     *slog.Logger
	version string
	now     func() time.Time
}

// New returns the admin API.
func New(st *store.Store, gw *gateway.Gateway, clients *proxy.Clients, log *slog.Logger, version string) *API {
	return &API{store: st, gw: gw, clients: clients, log: log, version: version, now: time.Now,
		limiter: newLoginLimiter(5, 15*time.Minute, time.Now)}
}

// Routes mounts the API under /api.
func (a *API) Routes(r chi.Router) {
	r.Route("/api", func(r chi.Router) {
		r.Use(http.NewCrossOriginProtection().Handler)
		r.Post("/auth/login", a.login)
		r.Group(func(r chi.Router) {
			r.Use(a.requireSession)
			r.Post("/auth/logout", a.logout)
			r.Get("/auth/me", a.me)

			r.Get("/providers", a.listProviders)
			r.Post("/providers", a.createProvider)
			r.Get("/providers/{id}", a.getProvider)
			r.Put("/providers/{id}", a.updateProvider)
			r.Delete("/providers/{id}", a.deleteProvider)
			r.Get("/providers/{id}/keys", a.listProviderKeys)
			r.Post("/providers/{id}/keys", a.createKey)
			r.Post("/providers/{id}/keys/bulk", a.bulkAddKeys)

			r.Get("/keys", a.listAllKeys)
			r.Post("/keys/batch", a.batchKeys)
			r.Put("/keys/{id}", a.updateKey)
			r.Delete("/keys/{id}", a.deleteKey)
			r.Post("/keys/{id}/reset", a.resetKey)
			r.Post("/keys/{id}/test", a.testKey)

			r.Get("/proxies", a.listProxies)
			r.Post("/proxies", a.createProxy)
			r.Post("/proxies/import", a.importProxies)
			r.Post("/proxies/test", a.testProxies)
			r.Post("/proxies/assign", a.assignProxies)
			r.Put("/proxies/{id}", a.updateProxy)
			r.Delete("/proxies/{id}", a.deleteProxy)
			r.Post("/proxies/{id}/test", a.testProxy)

			r.Get("/models", a.listModels)
			r.Post("/models", a.createModel)
			r.Get("/models/{id}", a.getModel)
			r.Put("/models/{id}", a.updateModel)
			r.Delete("/models/{id}", a.deleteModel)
			r.Post("/models/{id}/test", a.testModel)

			r.Get("/logs", a.listLogs)
			r.Get("/logs/{id}", a.getLog)
			r.Get("/dashboard", a.dashboard)

			r.Get("/settings", a.getSettings)
			r.Put("/settings", a.saveSettings)
			r.Post("/settings/password", a.changePassword)

			r.Get("/gateway-keys", a.listGatewayKeys)
			r.Post("/gateway-keys", a.createGatewayKey)
			r.Put("/gateway-keys/{id}", a.updateGatewayKey)
			r.Delete("/gateway-keys/{id}", a.deleteGatewayKey)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "Unknown API endpoint")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		})
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// fail maps store errors to HTTP responses.
func (a *API) fail(w http.ResponseWriter, err error) {
	var fe store.FieldErrors
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Please fix the highlighted fields.", "fields": fe})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Not found")
	default:
		a.log.Error("admin request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "Internal error: "+err.Error())
	}
}

func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return 0, false
	}
	return id, true
}

// --- sessions and login ---

func (a *API) sessionHash(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return secret.HashToken(c.Value)
}

func (a *API) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := a.sessionHash(r)
		if hash == "" {
			writeError(w, http.StatusUnauthorized, "Please sign in")
			return
		}
		ok, err := a.store.SessionValid(r.Context(), hash)
		if err != nil {
			a.fail(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "Your session has expired, please sign in again")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait, blocked := a.limiter.blocked(ip); blocked {
		minutes := int(wait.Minutes()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":               fmt.Sprintf("Too many failed attempts. Try again in %d minute(s).", minutes),
			"retry_after_seconds": int(wait.Seconds()) + 1,
		})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	hash, err := a.store.PasswordHash(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if hash == "" || !secret.CheckPassword(hash, in.Password) {
		a.limiter.fail(ip)
		a.log.Warn("failed admin login", "ip", ip)
		writeError(w, http.StatusUnauthorized, "Wrong password")
		return
	}
	a.limiter.reset(ip)
	token := secret.NewToken("", 32)
	if err := a.store.CreateSession(r.Context(), secret.HashToken(token), a.now().Add(sessionLifetime)); err != nil {
		a.fail(w, err)
		return
	}
	a.setSessionCookie(w, r, token, int(sessionLifetime.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if hash := a.sessionHash(r); hash != "" {
		if err := a.store.DeleteSession(r.Context(), hash); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": a.version})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	hash, err := a.store.PasswordHash(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	fields := store.FieldErrors{}
	if !secret.CheckPassword(hash, in.Current) {
		fields["current_password"] = "Wrong password"
	}
	if len(in.New) < 8 {
		fields["new_password"] = "Use at least 8 characters"
	}
	if len(fields) > 0 {
		a.fail(w, fields)
		return
	}
	if err := a.store.SetPassword(r.Context(), in.New); err != nil {
		a.fail(w, err)
		return
	}
	if err := a.store.DeleteOtherSessions(r.Context(), a.sessionHash(r)); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loginLimiter allows max failed logins per IP within window.
type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	now      func() time.Time
	failures map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration, now func() time.Time) *loginLimiter {
	return &loginLimiter{max: max, window: window, now: now, failures: map[string][]time.Time{}}
}

// recent drops failures older than the window. Caller holds mu.
func (l *loginLimiter) recent(ip string) []time.Time {
	cutoff := l.now().Add(-l.window)
	kept := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, ip)
		return nil
	}
	l.failures[ip] = kept
	return kept
}

// blocked reports whether ip must wait, and for how long.
func (l *loginLimiter) blocked(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.recent(ip)
	if len(f) < l.max {
		return 0, false
	}
	return f[0].Add(l.window).Sub(l.now()), true
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.recent(ip), l.now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

// detached returns a context that survives the GUI navigating away, so a
// started test finishes and its effect on key state is recorded.
func detached(r *http.Request) context.Context { return context.WithoutCancel(r.Context()) }
