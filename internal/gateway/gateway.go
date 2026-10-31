// Package gateway serves the client-facing /v1 API. Each request flows
// through a simple pipeline:
//
//	auth -> resolve model -> [pre-processors] -> route/retry -> adapter -> [post-processors] -> log
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/anthropic"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/gemini"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/openai"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// Exchange is a client request on its way through the pipeline.
type Exchange struct {
	Endpoint    string         // client path, e.g. "/v1/chat/completions"
	Format      adapter.Format // client protocol (openai or anthropic)
	Model       store.Model    // resolved public model
	Header      http.Header    // client request headers
	ContentType string
	Body        []byte // client request body; pre-processors may rewrite it
	// SubRequests are requests a pre-processor sent through the gateway (such
	// as vision bridge calls); they are added to the request's attempt details.
	SubRequests []store.Attempt
}

// PreProcessor may inspect or rewrite a request after the model is resolved
// and before it is routed. Returning an error rejects the request with the
// status of a *ProcessError, or 400 for any other error.
type PreProcessor interface {
	PreProcess(ctx context.Context, x *Exchange) error
}

// ProcessError rejects a request from a PreProcessor with a specific status.
type ProcessError struct {
	Status  int
	Message string
}

func (e *ProcessError) Error() string { return e.Message }

// PostProcessor observes a finished request (the response has been sent)
// before it is logged.
type PostProcessor interface {
	PostProcess(ctx context.Context, x *Exchange, entry *store.LogDetail)
}

// Gateway handles /v1 requests.
type Gateway struct {
	store   *store.Store
	router  *routing.Router
	clients *proxy.Clients
	logs    *LogWriter
	log     *slog.Logger
	version string

	inbound  map[adapter.Format]adapter.Inbound
	upstream map[string]adapter.Upstream // by provider type

	// Pre and Post are the processor hooks. Pre holds the vision bridge.
	Pre  []PreProcessor
	Post []PostProcessor
}

// New returns a Gateway.
func New(st *store.Store, router *routing.Router, clients *proxy.Clients, logs *LogWriter, log *slog.Logger, version string) *Gateway {
	oa, an := openai.New(), anthropic.New()
	g := &Gateway{
		store: st, router: router, clients: clients, logs: logs, log: log, version: version,
		inbound: map[adapter.Format]adapter.Inbound{adapter.OpenAI: oa, adapter.Anthropic: an},
		upstream: map[string]adapter.Upstream{
			string(adapter.OpenAI): oa, string(adapter.Anthropic): an, string(adapter.Gemini): gemini.New(),
		},
	}
	g.Pre = []PreProcessor{visionBridge{g}}
	return g
}

// endpoint describes a model-based client endpoint.
type endpoint struct {
	path         string
	modelType    string
	format       adapter.Format // client protocol, also used for errors
	upstreamPath string         // appended to an openai base URL (non-chat only)
}

var (
	epChat          = endpoint{"/v1/chat/completions", "chat", adapter.OpenAI, ""}
	epMessages      = endpoint{"/v1/messages", "chat", adapter.Anthropic, ""}
	epEmbeddings    = endpoint{"/v1/embeddings", "embedding", adapter.OpenAI, "/embeddings"}
	epImages        = endpoint{"/v1/images/generations", "image", adapter.OpenAI, "/images/generations"}
	epSpeech        = endpoint{"/v1/audio/speech", "tts", adapter.OpenAI, "/audio/speech"}
	epTranscription = endpoint{"/v1/audio/transcriptions", "asr", adapter.OpenAI, "/audio/transcriptions"}
	epVideos        = endpoint{"/v1/videos", "video", adapter.OpenAI, "/videos"}
)

var endpointByType = map[string]endpoint{
	"chat": epChat, "embedding": epEmbeddings, "image": epImages, "tts": epSpeech, "asr": epTranscription, "video": epVideos,
}

// Routes mounts the /v1 API on r.
func (g *Gateway) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(g.auth)
		for _, ep := range []endpoint{epChat, epMessages, epEmbeddings, epImages, epSpeech, epTranscription, epVideos} {
			r.Post(ep.path, g.handle(ep))
		}
		r.Get("/v1/videos/{id}", g.videoGet(""))
		r.Get("/v1/videos/{id}/content", g.videoGet("/content"))
		r.Get("/v1/models", g.listModels)
	})
	// Unknown /v1 paths answer in the protocol of the caller.
	r.HandleFunc("/v1/*", func(w http.ResponseWriter, r *http.Request) {
		g.writeError(w, formatFor(r), http.StatusNotFound, "Unknown endpoint "+r.Method+" "+r.URL.Path)
	})
}

func formatFor(r *http.Request) adapter.Format {
	if strings.HasPrefix(r.URL.Path, epMessages.path) {
		return adapter.Anthropic
	}
	return adapter.OpenAI
}

type ctxKey int

const authBypass ctxKey = 1

// auth checks the gateway key (Authorization: Bearer, x-api-key or x-goog-api-key).
func (g *Gateway) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(authBypass) == true {
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimSpace(r.Header.Get("x-api-key"))
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("x-goog-api-key"))
		}
		if v := r.Header.Get("Authorization"); token == "" && v != "" {
			if t, ok := strings.CutPrefix(v, "Bearer "); ok {
				token = strings.TrimSpace(t)
			}
		}
		if token == "" {
			g.writeError(w, formatFor(r), http.StatusUnauthorized,
				"Missing API key. Send your Switchyard gateway key as 'Authorization: Bearer <key>' or 'x-api-key'.")
			return
		}
		if _, err := g.store.AuthenticateGatewayKey(r.Context(), token); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				g.log.Error("gateway key lookup failed", "err", err)
				g.writeError(w, formatFor(r), http.StatusInternalServerError, "Internal error")
				return
			}
			g.writeError(w, formatFor(r), http.StatusUnauthorized, "Invalid or disabled gateway key.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeError writes an error body in the client's protocol.
func (g *Gateway) writeError(w http.ResponseWriter, f adapter.Format, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(g.inbound[f].EncodeError(status, msg))
}

// listModels serves GET /v1/models in OpenAI format, or Anthropic format when
// the client sends an anthropic-version header.
func (g *Gateway) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := g.store.ListModels(r.Context())
	if err != nil {
		g.log.Error("list models", "err", err)
		g.writeError(w, adapter.OpenAI, http.StatusInternalServerError, "Internal error")
		return
	}
	var body any
	if r.Header.Get("anthropic-version") != "" {
		data := make([]map[string]any, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]any{"type": "model", "id": m.Name, "display_name": m.Name,
				"created_at": m.CreatedAt.Format(time.RFC3339)})
		}
		resp := map[string]any{"data": data, "has_more": false, "first_id": nil, "last_id": nil}
		if len(models) > 0 {
			resp["first_id"], resp["last_id"] = models[0].Name, models[len(models)-1].Name
		}
		body = resp
	} else {
		data := make([]map[string]any, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]any{"id": m.Name, "object": "model", "created": m.CreatedAt.Unix(),
				"owned_by": "switchyard"})
		}
		body = map[string]any{"object": "list", "data": data}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}
