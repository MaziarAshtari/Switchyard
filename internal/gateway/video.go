package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// clientFor returns the HTTP client for a key's effective proxy: its specific
// proxy, the provider default, or a direct connection.
func (g *Gateway) clientFor(ctx context.Context, p store.Provider, k store.APIKey) (*http.Client, error) {
	var proxyID *int64
	switch k.ProxyMode {
	case store.ProxySpecific:
		if k.ProxyID == nil {
			return nil, errors.New("the key's proxy no longer exists")
		}
		proxyID = k.ProxyID
	case store.ProxyInherit:
		proxyID = p.ProxyID
	}
	if proxyID == nil {
		return g.clients.Get(nil), nil
	}
	spec, enabled, err := g.store.ProxySpec(ctx, *proxyID)
	if err != nil {
		return nil, fmt.Errorf("load proxy: %w", err)
	}
	if !enabled {
		return nil, errors.New("the key's proxy is disabled")
	}
	return g.clients.Get(spec.URL()), nil
}

// videoGet proxies GET /v1/videos/{id}[/content] to the provider and key that
// created the job.
func (g *Gateway) videoGet(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := chi.URLParam(r, "id")
		job, err := g.store.GetVideoJob(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			g.writeError(w, adapter.OpenAI, http.StatusNotFound, fmt.Sprintf("Unknown video id '%s'.", id))
			return
		}
		if err != nil {
			g.log.Error("load video job", "err", err)
			g.writeError(w, adapter.OpenAI, http.StatusInternalServerError, "Internal error")
			return
		}
		p, err := g.store.LookupProvider(ctx, job.ProviderID)
		if err != nil {
			g.writeError(w, adapter.OpenAI, http.StatusNotFound, "The provider of this video no longer exists.")
			return
		}
		k, err := g.store.GetKey(ctx, job.KeyID)
		if err != nil {
			g.writeError(w, adapter.OpenAI, http.StatusNotFound, "The API key that created this video no longer exists.")
			return
		}
		secret, err := g.store.KeySecret(ctx, k.ID)
		if err != nil {
			g.writeError(w, adapter.OpenAI, http.StatusInternalServerError, "Cannot read the API key.")
			return
		}
		client, err := g.clientFor(ctx, p, k)
		if err != nil {
			g.writeError(w, adapter.OpenAI, http.StatusBadGateway, err.Error())
			return
		}
		target := p.BaseURL + "/videos/" + url.PathEscape(id) + suffix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			g.writeError(w, adapter.OpenAI, http.StatusBadRequest, err.Error())
			return
		}
		g.setUpstreamHeaders(req, p, g.upstream[string(adapter.OpenAI)], secret)
		resp, err := client.Do(req)
		if err != nil {
			status := http.StatusBadGateway
			if isTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			g.writeError(w, adapter.OpenAI, status, "Upstream request failed: "+err.Error())
			return
		}
		defer resp.Body.Close()
		copyHeaders(w.Header(), resp.Header, "Content-Type", "Content-Length", "Content-Disposition")
		w.WriteHeader(resp.StatusCode)
		copyFlush(w, resp.Body)
	}
}
