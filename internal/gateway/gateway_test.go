package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MaziarAshtari/Switchyard/internal/db"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

var ctx = context.Background()

type env struct {
	t     *testing.T
	st    *store.Store
	srv   *httptest.Server
	token string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox("gateway-test-key-0123")
	st := store.New(d, box)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	logs := NewLogWriter(st, log)
	gw := New(st, routing.NewRouter(time.Now, rand.New(rand.NewPCG(1, 2))), proxy.NewClients(), logs, log, "test")
	r := chi.NewRouter()
	gw.Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.Close(); logs.Close(); d.Close() })
	k, err := st.CreateGatewayKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, st: st, srv: srv, token: k.Secret}
}

// upstream is a fake provider that records every request.
type upstream struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
}

type recorded struct {
	Path   string
	Header http.Header
	Body   []byte
}

func newUpstream(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *upstream {
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.requests = append(u.requests, recorded{r.URL.RequestURI(), r.Header.Clone(), body})
		u.mu.Unlock()
		h(w, r, body)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) calls() []recorded {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]recorded(nil), u.requests...)
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (e *env) provider(name, typ, baseURL string) store.Provider {
	e.t.Helper()
	p, err := e.st.CreateProvider(ctx, store.ProviderInput{Name: name, Type: typ, BaseURL: baseURL, Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) key(providerID int64, secret string) store.APIKey {
	e.t.Helper()
	k, err := e.st.CreateKey(ctx, providerID, store.KeyInput{Secret: secret})
	if err != nil {
		e.t.Fatal(err)
	}
	return k
}

func (e *env) model(name, typ string, mutate func(*store.ModelInput), targets ...store.TargetInput) store.Model {
	e.t.Helper()
	in := store.ModelInput{Name: name, Type: typ, MaxTotalAttempts: 5, MaxKeysPerTarget: 5, AttemptTimeoutSeconds: 30,
		RetryOn: routing.RetryOnValues, Targets: targets}
	if mutate != nil {
		mutate(&in)
	}
	m, err := e.st.CreateModel(ctx, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func target(providerID int64, model string) store.TargetInput {
	return store.TargetInput{ProviderID: providerID, UpstreamModel: model, Enabled: true}
}

func (e *env) do(method, path, contentType string, body io.Reader, headers ...string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *env) post(path, body string, headers ...string) (*http.Response, string) {
	e.t.Helper()
	resp := e.do(http.MethodPost, path, "application/json", strings.NewReader(body), headers...)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// waitLog waits for the newest request log (written asynchronously).
func (e *env) waitLog(n int) store.LogDetail {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items, _, _ := e.st.ListLogs(ctx, store.LogFilter{Limit: 100})
		if len(items) >= n {
			d, _ := e.st.GetLog(ctx, items[0].ID)
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("expected %d request logs", n)
	return store.LogDetail{}
}

func (e *env) keyState(id int64) store.APIKey {
	k, err := e.st.GetKey(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return k
}

const chatOK = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-x","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`

func TestChatPassthroughReplacesModelAndRelaysBody(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, chatOK)
	})
	p := e.provider("OA", "openai", up.URL+"/v1")
	e.key(p.ID, "sk-upstream-key-000001")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[{"role":"user","content":"hi"}],"seed":42}`)
	if resp.StatusCode != 200 || body != chatOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	calls := up.calls()
	if len(calls) != 1 || calls[0].Path != "/v1/chat/completions" {
		t.Fatalf("upstream calls: %+v", calls)
	}
	var sent map[string]any
	json.Unmarshal(calls[0].Body, &sent)
	if sent["model"] != "gpt-x" || sent["seed"] != float64(42) {
		t.Fatalf("upstream body: %s", calls[0].Body)
	}
	if got := calls[0].Header.Get("Authorization"); got != "Bearer sk-upstream-key-000001" {
		t.Fatalf("auth header %q", got)
	}
	log := e.waitLog(1)
	if log.StatusCode != 200 || *log.InputTokens != 7 || *log.OutputTokens != 1 || log.ProviderName != "OA" ||
		log.UpstreamModel != "gpt-x" || log.KeyHint != "sk-...0001" || log.Attempts != 1 {
		t.Fatalf("log: %+v", log)
	}
}

func TestRetryOn429CoolsDownKeyAndUsesNextKey(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if bearer(r) == "sk-key-one-0000000000" {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"slow down"}}`)
			return
		}
		io.WriteString(w, chatOK)
	})
	p := e.provider("OA", "openai", up.URL)
	k1 := e.key(p.ID, "sk-key-one-0000000000")
	k2 := e.key(p.ID, "sk-key-two-0000000000")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, _ := e.post("/v1/chat/completions", `{"model":"best","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	s1 := e.keyState(k1.ID)
	if s1.Status != routing.StatusCoolingDown || s1.CooldownUntil == nil || !strings.Contains(s1.LastError, "slow down") {
		t.Fatalf("key 1: %+v", s1)
	}
	if d := time.Until(*s1.CooldownUntil); d < 25*time.Second || d > 31*time.Second {
		t.Fatalf("cooldown should follow Retry-After (30s), got %v", d)
	}
	if s2 := e.keyState(k2.ID); s2.SuccessCount != 1 {
		t.Fatalf("key 2: %+v", s2)
	}
	if log := e.waitLog(1); log.Attempts != 2 || log.AttemptDetails[0].StatusCode != 429 || log.KeyHint != "sk-...0000" {
		t.Fatalf("log: %+v", log)
	}
	// The cooling key is skipped by the next request.
	e.post("/v1/chat/completions", `{"model":"best","messages":[{"role":"user","content":"again"}]}`)
	calls := up.calls()
	if last := calls[len(calls)-1]; strings.TrimPrefix(last.Header.Get("Authorization"), "Bearer ") != "sk-key-two-0000000000" || len(calls) != 3 {
		t.Fatalf("cooling key must be skipped; calls=%d", len(calls))
	}
}

func TestAuthErrorDisablesKeyAndClientNeverSees401(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	})
	p := e.provider("OA", "openai", up.URL)
	k1 := e.key(p.ID, "sk-key-one-0000000000")
	k2 := e.key(p.ID, "sk-key-two-0000000000")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`)
	if resp.StatusCode != 502 || !strings.Contains(body, "All 2 attempts failed") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	for _, id := range []int64{k1.ID, k2.ID} {
		if k := e.keyState(id); k.Status != routing.StatusDisabled || !strings.Contains(k.LastError, "invalid api key") {
			t.Fatalf("key %d should be disabled: %+v", id, k)
		}
	}
	resp, body = e.post("/v1/chat/completions", `{"model":"best","messages":[]}`)
	if resp.StatusCode != 503 || !strings.Contains(body, "no available keys") {
		t.Fatalf("with all keys disabled: %d %s", resp.StatusCode, body)
	}
}

func TestClientErrorIsNotRetriedAndRelayedVerbatim(t *testing.T) {
	e := newEnv(t)
	const upstreamErr = `{"error":{"message":"bad temperature","type":"invalid_request_error","param":"temperature"}}`
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, upstreamErr)
	})
	p := e.provider("OA", "openai", up.URL)
	e.key(p.ID, "sk-key-one-0000000000")
	e.key(p.ID, "sk-key-two-0000000000")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[],"temperature":9}`)
	if resp.StatusCode != 400 || body != upstreamErr || len(up.calls()) != 1 {
		t.Fatalf("status %d body %s calls %d", resp.StatusCode, body, len(up.calls()))
	}
}

func TestFailoverToNextTargetAndExhaustion(t *testing.T) {
	e := newEnv(t)
	bad := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) { w.WriteHeader(503) })
	good := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) { io.WriteString(w, chatOK) })
	pBad := e.provider("Bad", "openai", bad.URL)
	pGood := e.provider("Good", "openai", good.URL)
	e.key(pBad.ID, "sk-bad-1-000000000000")
	e.key(pBad.ID, "sk-bad-2-000000000000")
	e.key(pGood.ID, "sk-good-000000000000")
	e.model("best", "chat", func(in *store.ModelInput) { in.MaxKeysPerTarget = 1 },
		target(pBad.ID, "m1"), target(pGood.ID, "m2"))

	resp, _ := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`)
	if resp.StatusCode != 200 || len(bad.calls()) != 1 || len(good.calls()) != 1 {
		t.Fatalf("status %d bad=%d good=%d", resp.StatusCode, len(bad.calls()), len(good.calls()))
	}

	e.model("only-bad", "chat", func(in *store.ModelInput) { in.MaxTotalAttempts = 2 }, target(pBad.ID, "m1"))
	resp, body := e.post("/v1/chat/completions", `{"model":"only-bad","messages":[]}`)
	if resp.StatusCode != 503 || !strings.Contains(body, "All 2 attempts failed") {
		t.Fatalf("exhausted: %d %s", resp.StatusCode, body)
	}
}

func TestStreamRetriesBeforeFirstByteButNotAfter(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch bearer(r) {
		case "sk-empty-000000000000": // 200 but the stream ends before any event
			return
		case "sk-good-0000000000000":
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	})
	p := e.provider("OA", "openai", up.URL)
	e.key(p.ID, "sk-empty-000000000000")
	e.key(p.ID, "sk-good-0000000000000")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","stream":true,"messages":[]}`)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(body, `"Hel"`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if n := len(up.calls()); n != 2 {
		t.Fatalf("expected a retry before the first byte, got %d calls", n)
	}
}

func TestNoRetryAfterStreamStarted(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack() // drop the connection mid-stream
		conn.Close()
	})
	p := e.provider("OA", "openai", up.URL)
	e.key(p.ID, "sk-key-one-0000000000")
	e.key(p.ID, "sk-key-two-0000000000")
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","stream":true,"messages":[]}`)
	if resp.StatusCode != 200 || !strings.Contains(body, "partial") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if n := len(up.calls()); n != 1 {
		t.Fatalf("must not retry mid-stream, got %d calls", n)
	}
	if log := e.waitLog(1); log.Attempts != 1 || !strings.Contains(log.Error, "interrupted") {
		t.Fatalf("log: %+v", log)
	}
}

func TestAnthropicClientStreamTranslatedFromOpenAIUpstream(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"c1","model":"gpt-x","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
			`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]}}]}`,
			`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Oslo\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"id":"c1","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	})
	p := e.provider("OA", "openai", up.URL)
	e.key(p.ID, "sk-key-one-0000000000")
	e.model("claude-sonnet", "chat", nil, target(p.ID, "gpt-x"))

	resp, body := e.post("/v1/messages", `{"model":"claude-sonnet","max_tokens":100,"stream":true,
		"system":[{"type":"text","text":"Be brief"}],
		"tools":[{"name":"get_weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],
		"messages":[{"role":"user","content":"Weather in Oslo?"}]}`, "anthropic-version", "2023-06-01")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var sent map[string]any
	json.Unmarshal(up.calls()[0].Body, &sent)
	if sent["model"] != "gpt-x" || sent["stream"] != true {
		t.Fatalf("upstream request not translated: %s", up.calls()[0].Body)
	}
	for _, want := range []string{"event: message_start", `"text_delta"`, `"tool_use"`, `"input_json_delta"`,
		`"stop_reason":"tool_use"`, `"output_tokens":5`, "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Errorf("translated stream lacks %s:\n%s", want, body)
		}
	}
	log := e.waitLog(1)
	if *log.InputTokens != 11 || *log.OutputTokens != 5 || !log.Stream {
		t.Fatalf("log: %+v", log)
	}
}

func TestAnthropicPassthroughForwardsVersionAndBetaHeaders(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	p := e.provider("AN", "anthropic", up.URL)
	e.key(p.ID, "sk-ant-key-000000000")
	e.model("claude", "chat", nil, target(p.ID, "claude-x"))

	resp, body := e.post("/v1/messages?beta=true", `{"model":"claude","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`,
		"anthropic-version", "2023-06-01", "anthropic-beta", "interleaved-thinking-2025-05-14")
	if resp.StatusCode != 200 || !strings.Contains(body, `"text":"ok"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	c := up.calls()[0]
	if c.Path != "/v1/messages" || c.Header.Get("x-api-key") != "sk-ant-key-000000000" ||
		c.Header.Get("anthropic-beta") != "interleaved-thinking-2025-05-14" || c.Header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("upstream request: %s %v", c.Path, c.Header)
	}
}

func TestErrorsFollowInboundProtocol(t *testing.T) {
	e := newEnv(t)
	oa := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {})
	p := e.provider("OA", "openai", oa.URL)
	e.model("embed", "embedding", nil, target(p.ID, "text-embedding-3-small"))

	resp, body := e.post("/v1/messages", `{"model":"missing","max_tokens":5,"messages":[]}`)
	if resp.StatusCode != 404 || !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, "not_found_error") {
		t.Fatalf("anthropic 404: %d %s", resp.StatusCode, body)
	}
	resp, body = e.post("/v1/chat/completions", `{"model":"embed","messages":[]}`)
	if resp.StatusCode != 400 || !strings.Contains(body, `"error":{`) || !strings.Contains(body, "embedding model") {
		t.Fatalf("type mismatch: %d %s", resp.StatusCode, body)
	}
	e.token = "sy-wrong"
	resp, body = e.post("/v1/messages", `{}`)
	if resp.StatusCode != 401 || !strings.Contains(body, "authentication_error") || !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("anthropic auth error: %d %s", resp.StatusCode, body)
	}
	resp, body = e.post("/v1/embeddings", `{}`)
	if resp.StatusCode != 401 || !strings.HasPrefix(body, `{"error":{`) {
		t.Fatalf("openai auth error: %d %s", resp.StatusCode, body)
	}
}

func TestAlternativeAuthHeaders(t *testing.T) {
	e := newEnv(t)
	for _, h := range []string{"x-api-key", "x-goog-api-key"} {
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/v1/models", nil)
		req.Header.Set(h, e.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", h, resp.StatusCode, err)
		}
		resp.Body.Close()
	}
}

func TestTranscriptionMultipartModelIsReplaced(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"hello world"}`)
	})
	p := e.provider("OA", "openai", up.URL+"/v1")
	e.key(p.ID, "sk-key-one-0000000000")
	e.model("my-asr", "asr", nil, target(p.ID, "whisper-1"))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("model", "my-asr")
	mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "a.wav")
	fw.Write([]byte("RIFF-fake-audio"))
	mw.Close()
	resp := e.do(http.MethodPost, "/v1/audio/transcriptions", mw.FormDataContentType(), &buf)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"text":"hello world"}` {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	c := up.calls()[0]
	if c.Path != "/v1/audio/transcriptions" {
		t.Fatalf("path %s", c.Path)
	}
	_, params, _ := mime.ParseMediaType(c.Header.Get("Content-Type"))
	form, err := multipart.NewReader(bytes.NewReader(c.Body), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["model"][0] != "whisper-1" || form.Value["language"][0] != "en" || form.File["file"][0].Filename != "a.wav" {
		t.Fatalf("form: %+v", form.Value)
	}
}

func TestSpeechStreamsBinaryAudio(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte{0xff, 0xfb, 0x90, 0x00})
	})
	p := e.provider("OA", "openai", up.URL)
	e.key(p.ID, "sk-key-one-0000000000")
	e.model("my-tts", "tts", nil, target(p.ID, "tts-1"))
	resp, body := e.post("/v1/audio/speech", `{"model":"my-tts","input":"hi","voice":"alloy"}`)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/mpeg" || body != "\xff\xfb\x90\x00" {
		t.Fatalf("status %d ct %s body %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

func TestVideoPollingUsesTheCreatingKey(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case r.Method == http.MethodPost && bearer(r) == "sk-key-one-0000000000":
			w.WriteHeader(429) // first key is rate limited; the job is created with key two
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"video_123","object":"video","status":"queued"}`)
		case strings.HasSuffix(r.URL.Path, "/content"):
			w.Header().Set("Content-Type", "video/mp4")
			io.WriteString(w, "MP4DATA")
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"video_123","status":"completed","key":"`+bearer(r)+`"}`)
		}
	})
	p := e.provider("OA", "openai", up.URL+"/v1")
	e.key(p.ID, "sk-key-one-0000000000")
	e.key(p.ID, "sk-key-two-0000000000")
	e.model("my-video", "video", nil, target(p.ID, "sora-2"))

	resp, body := e.post("/v1/videos", `{"model":"my-video","prompt":"a cat"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, "video_123") {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	// Key one is now cooling down, but polling must still go to key two (the creator).
	resp = e.do(http.MethodGet, "/v1/videos/video_123", "", nil)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"key":"sk-key-two-0000000000"`) {
		t.Fatalf("poll: %d %s", resp.StatusCode, b)
	}
	resp = e.do(http.MethodGet, "/v1/videos/video_123/content?variant=video", "", nil)
	b, _ = io.ReadAll(resp.Body)
	if string(b) != "MP4DATA" || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("content: %s", b)
	}
	if last := up.calls()[len(up.calls())-1]; last.Path != "/v1/videos/video_123/content?variant=video" {
		t.Fatalf("content path %s", last.Path)
	}
	resp = e.do(http.MethodGet, "/v1/videos/unknown", "", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown video: %d", resp.StatusCode)
	}
}

func TestKeyProxyIsUsedAndDisabledProxySkipsKey(t *testing.T) {
	e := newEnv(t)
	var viaProxy atomic.Int32
	// A forward proxy that relays plain HTTP requests to the upstream.
	fwd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Add(1)
		out, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
		out.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer fwd.Close()
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) { io.WriteString(w, chatOK) })
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(fwd.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	px, err := e.st.CreateProxy(ctx, store.ProxyInput{Type: "http", Host: host, Port: port, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p := e.provider("OA", "openai", up.URL)
	k, _ := e.st.CreateKey(ctx, p.ID, store.KeyInput{Secret: "sk-proxied-0000000000", ProxyMode: store.ProxySpecific, ProxyID: &px.ID})
	e.model("best", "chat", nil, target(p.ID, "gpt-x"))

	if resp, _ := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`); resp.StatusCode != 200 || viaProxy.Load() != 1 {
		t.Fatalf("status %d via proxy %d", resp.StatusCode, viaProxy.Load())
	}
	e.st.UpdateProxy(ctx, px.ID, store.ProxyInput{Type: "http", Host: host, Port: port, Enabled: false})
	resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`)
	if resp.StatusCode != 503 || viaProxy.Load() != 1 {
		t.Fatalf("disabled proxy must skip the key: %d %s", resp.StatusCode, body)
	}
	_ = k
}

func TestAttemptTimeoutMovesToNextKey(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if bearer(r) == "sk-slow-0000000000000" {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		io.WriteString(w, chatOK)
	})
	p := e.provider("OA", "openai", up.URL)
	slow := e.key(p.ID, "sk-slow-0000000000000")
	e.key(p.ID, "sk-fast-0000000000000")
	e.model("best", "chat", func(in *store.ModelInput) { in.AttemptTimeoutSeconds = 1 }, target(p.ID, "gpt-x"))

	start := time.Now()
	resp, _ := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`)
	if resp.StatusCode != 200 || time.Since(start) > 4*time.Second {
		t.Fatalf("status %d after %v", resp.StatusCode, time.Since(start))
	}
	if k := e.keyState(slow.ID); k.FailureCount != 1 || !strings.Contains(k.LastError, "timed out") {
		t.Fatalf("slow key: %+v", k)
	}
}

func TestListModelsFormats(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {})
	p := e.provider("OA", "openai", up.URL)
	e.model("alpha", "chat", nil, target(p.ID, "x"))

	resp := e.do(http.MethodGet, "/v1/models", "", nil)
	var oa struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&oa)
	if oa.Object != "list" || len(oa.Data) != 1 || oa.Data[0].ID != "alpha" {
		t.Fatalf("openai format: %+v", oa)
	}
	resp = e.do(http.MethodGet, "/v1/models", "", nil, "anthropic-version", "2023-06-01")
	var an map[string]any
	json.NewDecoder(resp.Body).Decode(&an)
	if an["first_id"] != "alpha" || an["has_more"] != false {
		t.Fatalf("anthropic format: %+v", an)
	}
}

func TestStreamFromAnthropicUpstreamToOpenAIClient(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		events := [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":9,"output_tokens":1}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"ping", `{"type":"ping"}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}
		for _, ev := range events {
			fmt.Fprintf(bw, "event: %s\ndata: %s\n\n", ev[0], ev[1])
		}
		bw.Flush()
	})
	p := e.provider("AN", "anthropic", up.URL)
	e.key(p.ID, "sk-ant-key-000000000")
	e.model("best", "chat", nil, target(p.ID, "claude-x"))

	resp, body := e.post("/v1/chat/completions", `{"model":"best","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"content":"Hello"`, `"finish_reason":"stop"`, `"prompt_tokens":9`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in:\n%s", want, body)
		}
	}
	var sent map[string]any
	json.Unmarshal(up.calls()[0].Body, &sent)
	if sent["system"] != "s" || sent["max_tokens"] == nil {
		t.Fatalf("anthropic request: %s", up.calls()[0].Body)
	}
}

func TestAuthErrorDisablesKeyUntilRecoveryTime(t *testing.T) {
	e := newEnv(t)
	var credit atomic.Bool
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if !credit.Load() {
			w.WriteHeader(402)
			io.WriteString(w, `{"error":{"message":"no credit left"}}`)
			return
		}
		io.WriteString(w, chatOK)
	})
	set, _ := e.st.Settings(ctx)
	set.DisabledKeyRecoveryMinutes = 10
	if _, err := e.st.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	inherit := e.provider("Inherit", "openai", up.URL)
	custom := e.provider("Custom", "openai", up.URL)
	five := 5
	if _, err := e.st.UpdateProvider(ctx, custom.ID, store.ProviderInput{Name: "Custom", Type: "openai", BaseURL: up.URL,
		Enabled: true, DisabledKeyRecoveryMinutes: &five}); err != nil {
		t.Fatal(err)
	}
	k1 := e.key(inherit.ID, "sk-inherit-0000000000")
	k2 := e.key(custom.ID, "sk-custom-00000000000")
	e.model("best", "chat", nil, target(inherit.ID, "gpt-x"), target(custom.ID, "gpt-x"))

	if resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`); resp.StatusCode != 502 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	for id, want := range map[int64]time.Duration{k1.ID: 10 * time.Minute, k2.ID: 5 * time.Minute} {
		k := e.keyState(id)
		if k.Status != routing.StatusDisabled || k.DisabledUntil == nil {
			t.Fatalf("key %d: %+v", id, k)
		}
		if d := time.Until(*k.DisabledUntil); d < want-time.Minute || d > want {
			t.Fatalf("key %d recovers in %v, want ~%v", id, d, want)
		}
	}
	if resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`); resp.StatusCode != 503 {
		t.Fatalf("disabled keys must be skipped: %d %s", resp.StatusCode, body)
	}

	// The recovery time of the first key passes.
	past := time.Now().Add(-time.Second).UnixMilli()
	if _, err := e.st.DB().Exec(`UPDATE api_keys SET disabled_until = ? WHERE id = ?`, past, k1.ID); err != nil {
		t.Fatal(err)
	}
	credit.Store(true)
	if resp, body := e.post("/v1/chat/completions", `{"model":"best","messages":[]}`); resp.StatusCode != 200 {
		t.Fatalf("recovered key must be used again: %d %s", resp.StatusCode, body)
	}
	if k := e.keyState(k1.ID); k.Status != routing.StatusActive || k.SuccessCount != 1 {
		t.Fatalf("recovered key: %+v", k)
	}
}
