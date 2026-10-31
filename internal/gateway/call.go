package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

var errAttemptTimeout = errors.New("attempt timeout")

// call is the state of one model request.
type call struct {
	g        *Gateway
	w        *captureWriter
	r        *http.Request
	ctx      context.Context // the client request; canceled when the client leaves
	bg       context.Context // for writes that must survive a client disconnect
	ep       endpoint
	settings store.Settings
	start    time.Time

	x       *Exchange
	chat    *adapter.Request // decoded chat request (chat endpoints only)
	reqBody []byte
	entry   store.LogDetail

	providers map[int64]store.Provider
	keys      map[int64]store.APIKey
}

// result is the outcome of one attempt.
type result struct {
	outcome     routing.Outcome
	status      int // upstream status, 0 when none
	errMsg      string
	latency     time.Duration
	done        bool // the response was delivered to the client
	canceled    bool // the client went away
	passthrough bool // upstream speaks the client's protocol
	body        []byte
	header      http.Header
}

func (g *Gateway) handle(ep endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { g.serve(w, r, ep) }
}

// serve runs the pipeline for one request and returns its log entry.
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, ep endpoint) *store.LogDetail {
	settings, err := g.store.Settings(r.Context())
	if err != nil {
		g.log.Error("load settings", "err", err)
		settings = store.DefaultSettings()
	}
	c := &call{
		g: g, r: r, ctx: r.Context(), bg: context.WithoutCancel(r.Context()), ep: ep, settings: settings,
		start:     time.Now(),
		w:         &captureWriter{ResponseWriter: w, capture: settings.LogBodies, limit: settings.LogBodyMaxBytes},
		providers: map[int64]store.Provider{},
		keys:      map[int64]store.APIKey{},
	}
	c.entry = store.LogDetail{
		LogEntry:       store.LogEntry{CreatedAt: c.start.UTC(), Endpoint: ep.path},
		AttemptDetails: []store.Attempt{},
	}
	c.run()
	c.finish()
	return &c.entry
}

func (c *call) run() {
	body, err := io.ReadAll(http.MaxBytesReader(c.w, c.r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.fail(http.StatusRequestEntityTooLarge, "Request body too large")
		} else {
			c.fail(http.StatusBadRequest, "Cannot read request body")
		}
		return
	}
	c.reqBody = body
	contentType := c.r.Header.Get("Content-Type")
	name, err := requestModel(body, contentType)
	if err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if name == "" {
		c.fail(http.StatusBadRequest, "The 'model' field is required")
		return
	}
	c.entry.Model = name
	m, err := c.g.store.ModelByName(c.ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		c.fail(http.StatusNotFound, fmt.Sprintf("Model '%s' does not exist in Switchyard.", name))
		return
	}
	if err != nil {
		c.internal(err)
		return
	}
	c.entry.ModelType = m.Type
	if m.Type != c.ep.modelType {
		c.fail(http.StatusBadRequest, fmt.Sprintf("Model '%s' is a %s model and cannot be used with %s; use %s.",
			name, m.Type, c.ep.path, endpointByType[m.Type].path))
		return
	}
	c.x = &Exchange{Endpoint: c.ep.path, Format: c.ep.format, Model: m, Header: c.r.Header, ContentType: contentType, Body: body}
	if err := c.preProcess(); err != nil {
		var pe *ProcessError
		switch {
		case c.ctx.Err() != nil:
			c.entry.StatusCode, c.entry.Error = 499, "client closed the request"
		case errors.As(err, &pe):
			c.fail(pe.Status, pe.Message)
		default:
			c.fail(http.StatusBadRequest, err.Error())
		}
		return
	}
	if c.ep.modelType == "chat" {
		req, err := c.g.inbound[c.ep.format].DecodeRequest(c.x.Body)
		if err != nil {
			c.fail(http.StatusBadRequest, "Invalid request: "+err.Error())
			return
		}
		c.chat = req
		c.entry.Stream = req.Stream
	}
	c.route()
}

// preProcess runs the pre-processors and records their sub-requests, also
// when one of them fails.
func (c *call) preProcess() error {
	var err error
	for _, p := range c.g.Pre {
		if err = p.PreProcess(c.ctx, c.x); err != nil {
			break
		}
	}
	c.entry.AttemptDetails = append(c.entry.AttemptDetails, c.x.SubRequests...)
	return err
}

// route runs the retry plan until an attempt delivers a response or the plan
// is exhausted.
func (c *call) route() {
	m := c.x.Model
	policy := routing.Policy{
		Strategy:         m.Strategy,
		MaxTotalAttempts: m.MaxTotalAttempts,
		MaxKeysPerTarget: m.MaxKeysPerTarget,
		RetryOn:          routing.ParseRetryOn(m.RetryOn),
		DefaultCooldown:  time.Duration(c.settings.DefaultCooldownSeconds) * time.Second,
	}
	targets := make([]routing.Target, 0, len(m.Targets))
	byID := map[int64]store.Target{}
	for _, t := range m.Targets {
		p, err := c.provider(t.ProviderID)
		if err != nil {
			c.internal(err)
			return
		}
		byID[t.ID] = t
		targets = append(targets, routing.Target{ID: t.ID, ProviderID: t.ProviderID, KeyStrategy: p.KeyStrategy,
			KeyRecovery: p.KeyRecovery(c.settings), Weight: t.Weight, Enabled: t.Enabled && p.Enabled})
	}
	plan := c.g.router.NewPlan(m.ID, policy, targets, c.keySource)
	var last *result
	for {
		a, ok := plan.Next()
		if !ok {
			break
		}
		t := byID[a.Target.ID]
		res := c.attempt(a, t)
		c.recordAttempt(a, t, res)
		if res.canceled {
			c.entry.StatusCode, c.entry.Error = 499, "client closed the request"
			return
		}
		d := plan.Report(a, res.outcome)
		if err := c.g.store.RecordKeyOutcome(c.bg, a.Key.ID, d, res.errMsg); err != nil {
			c.g.log.Error("record key outcome", "key_id", a.Key.ID, "err", err)
		}
		last = &res
		if res.done || !d.Retry {
			break
		}
	}
	switch {
	case last == nil:
		c.fail(http.StatusServiceUnavailable, c.noUpstreamMessage(plan, byID))
	case !last.done:
		c.failWith(*last)
	}
}

func (c *call) provider(id int64) (store.Provider, error) {
	if p, ok := c.providers[id]; ok {
		return p, nil
	}
	p, err := c.g.store.LookupProvider(c.ctx, id)
	if err == nil {
		c.providers[id] = p
	}
	return p, err
}

func (c *call) keySource(providerID int64) []routing.Key {
	keys, err := c.g.store.ListKeys(c.ctx, providerID)
	if err != nil {
		c.g.log.Error("load keys", "provider_id", providerID, "err", err)
		return nil
	}
	out := make([]routing.Key, 0, len(keys))
	for _, k := range keys {
		c.keys[k.ID] = k
		out = append(out, k.RoutingKey())
	}
	return out
}

func (c *call) recordAttempt(a routing.Attempt, t store.Target, res result) {
	p, k := c.providers[t.ProviderID], c.keys[a.Key.ID]
	c.entry.AttemptDetails = append(c.entry.AttemptDetails, store.Attempt{
		ProviderName: p.Name, KeyHint: k.SecretHint, UpstreamModel: t.UpstreamModel,
		StatusCode: res.status, LatencyMS: res.latency.Milliseconds(), Error: res.errMsg,
	})
	c.entry.Attempts++ // attempt details may also hold pre-processor sub-requests
	providerID, keyID := p.ID, k.ID
	c.entry.ProviderID, c.entry.ProviderName = &providerID, p.Name
	c.entry.KeyID, c.entry.KeyHint = &keyID, k.SecretHint
	c.entry.UpstreamModel = t.UpstreamModel
}

// attempt sends the request to one (target, key) and, on success, relays the
// response to the client.
func (c *call) attempt(a routing.Attempt, t store.Target) (res result) {
	start := time.Now()
	defer func() { res.latency = time.Since(start) }()
	p, k := c.providers[t.ProviderID], c.keys[a.Key.ID]

	client, err := c.g.clientFor(c.ctx, p, k)
	if err != nil {
		return result{outcome: routing.Outcome{ConnError: true}, errMsg: err.Error()}
	}
	secret, err := c.g.store.KeySecret(c.ctx, k.ID)
	if err != nil {
		return result{outcome: routing.Outcome{ConnError: true}, errMsg: "cannot read API key: " + err.Error()}
	}

	// The provider timeout caps the whole upstream request; the attempt
	// timeout only runs until the response is committed to the client.
	reqCtx, cancel := context.WithTimeout(c.ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	attemptCtx, cancelAttempt := context.WithCancelCause(reqCtx)
	defer cancelAttempt(nil)
	timer := time.AfterFunc(time.Duration(c.x.Model.AttemptTimeoutSeconds)*time.Second,
		func() { cancelAttempt(errAttemptTimeout) })
	defer timer.Stop()

	req, m, err := c.buildRequest(attemptCtx, p, t, secret)
	if err != nil {
		return result{outcome: routing.Outcome{Status: http.StatusBadRequest}, status: http.StatusBadRequest,
			errMsg: "cannot build upstream request: " + err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return c.transportError(attemptCtx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return result{
			outcome:     routing.Outcome{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")},
			status:      resp.StatusCode,
			errMsg:      strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, m.up.ErrorMessage(body))),
			passthrough: m.passthrough,
			body:        body,
			header:      resp.Header,
		}
	}
	res = c.relay(attemptCtx, resp, p, a, m, timer.Stop)
	res.passthrough = m.passthrough
	return res
}

// transportError classifies an error that occurred before a response was committed.
func (c *call) transportError(ctx context.Context, err error) result {
	switch {
	case c.ctx.Err() != nil:
		return result{canceled: true, errMsg: "client closed the request"}
	case errors.Is(context.Cause(ctx), errAttemptTimeout):
		return result{outcome: routing.Outcome{Timeout: true}, errMsg: "attempt timed out"}
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return result{outcome: routing.Outcome{Timeout: true}, errMsg: "request timed out"}
	default:
		return result{outcome: routing.Outcome{ConnError: true}, errMsg: "connection error: " + err.Error()}
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// mode describes how an attempt talks to its upstream.
type mode struct {
	chat        bool
	stream      bool
	passthrough bool
	up          adapter.Upstream
}

func (c *call) buildRequest(ctx context.Context, p store.Provider, t store.Target, secret string) (*http.Request, mode, error) {
	m := mode{up: c.g.upstream[p.Type], chat: c.ep.modelType == "chat"}
	if m.up == nil {
		return nil, m, fmt.Errorf("unknown provider type %q", p.Type)
	}
	var (
		url, contentType string
		body             []byte
		err              error
	)
	if m.chat {
		m.stream = c.chat.Stream
		m.passthrough = string(c.ep.format) == p.Type
		url = m.up.ChatURL(p.BaseURL, t.UpstreamModel, m.stream)
		if m.passthrough {
			body, contentType, err = withModel(c.x.Body, "application/json", t.UpstreamModel)
		} else {
			body, err = m.up.EncodeRequest(c.chat, t.UpstreamModel)
			contentType = "application/json"
		}
	} else {
		if p.Type != string(adapter.OpenAI) {
			return nil, m, fmt.Errorf("provider %q is %s-compatible; %s models need an OpenAI-compatible provider", p.Name, p.Type, c.ep.modelType)
		}
		m.passthrough = true
		url = p.BaseURL + c.ep.upstreamPath
		body, contentType, err = withModel(c.x.Body, c.x.ContentType, t.UpstreamModel)
	}
	if err != nil {
		return nil, m, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, m, err
	}
	req.Header.Set("Content-Type", contentType)
	c.g.setUpstreamHeaders(req, p, m.up, secret)
	if m.passthrough && p.Type == string(adapter.Anthropic) {
		for _, h := range []string{"anthropic-version", "anthropic-beta"} {
			if v := c.r.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
	}
	return req, m, nil
}

// setUpstreamHeaders applies the provider's extra headers and the key's auth.
func (g *Gateway) setUpstreamHeaders(req *http.Request, p store.Provider, up adapter.Upstream, secret string) {
	req.Header.Set("User-Agent", "Switchyard/"+g.version)
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	up.SetAuth(req.Header, secret)
}

// failWith answers the client after the last attempt failed.
func (c *call) failWith(last result) {
	status := last.status
	switch {
	case last.outcome.Timeout:
		status = http.StatusGatewayTimeout
	case last.outcome.ConnError, status == 0:
		status = http.StatusBadGateway
	case status == http.StatusUnauthorized || status == http.StatusPaymentRequired || status == http.StatusForbidden:
		// Never show a client an auth error caused by an upstream key.
		status = http.StatusBadGateway
	}
	clientError := status >= 400 && status < 500 && status != http.StatusTooManyRequests
	if clientError && last.passthrough && len(last.body) > 0 && status == last.status {
		// Same protocol: relay the upstream error verbatim.
		ct := last.header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		c.entry.StatusCode, c.entry.Error = status, last.errMsg
		c.w.Header().Set("Content-Type", ct)
		c.w.WriteHeader(status)
		c.w.Write(last.body)
		return
	}
	var msg string
	switch {
	case clientError:
		msg = fmt.Sprintf("%s rejected the request: %s", c.entry.ProviderName, last.errMsg)
	case c.entry.Attempts > 1:
		msg = fmt.Sprintf("All %d attempts failed. Last error (%s): %s", c.entry.Attempts, c.entry.ProviderName, last.errMsg)
	default:
		msg = fmt.Sprintf("Upstream %s failed: %s", c.entry.ProviderName, last.errMsg)
	}
	if status == http.StatusTooManyRequests && last.header != nil {
		if ra := last.header.Get("Retry-After"); ra != "" {
			c.w.Header().Set("Retry-After", ra)
		}
	}
	c.fail(status, msg)
}

func (c *call) noUpstreamMessage(plan *routing.Plan, byID map[int64]store.Target) string {
	name := c.x.Model.Name
	if len(c.x.Model.Targets) == 0 {
		return fmt.Sprintf("Model '%s' has no targets.", name)
	}
	var reasons []string
	for _, s := range plan.Skipped {
		t := byID[s.TargetID]
		reasons = append(reasons, fmt.Sprintf("%s/%s: %s", t.ProviderName, t.UpstreamModel, s.Reason))
	}
	return fmt.Sprintf("No upstream available for model '%s' (%s).", name, strings.Join(reasons, "; "))
}

// fail sends an error in the client's protocol (unless output already started).
func (c *call) fail(status int, msg string) {
	c.entry.StatusCode = status
	if c.entry.Error == "" {
		c.entry.Error = msg
	}
	if c.w.started() {
		return
	}
	c.g.writeError(c.w, c.ep.format, status, msg)
}

func (c *call) internal(err error) {
	c.g.log.Error("request failed", "endpoint", c.ep.path, "err", err)
	c.fail(http.StatusInternalServerError, "Internal error")
}

func (c *call) setUsage(u adapter.Usage) {
	in, out := int64(u.InputTokens), int64(u.OutputTokens)
	c.entry.InputTokens, c.entry.OutputTokens = &in, &out
}

// finish runs the post-processors and queues the log entry.
func (c *call) finish() {
	if c.entry.StatusCode == 0 {
		c.entry.StatusCode = c.w.status
	}
	c.entry.LatencyMS = time.Since(c.start).Milliseconds()
	if c.settings.LogBodies {
		c.entry.RequestBody = bodyForLog(c.reqBody, c.r.Header.Get("Content-Type"), c.settings.LogBodyMaxBytes)
		c.entry.ResponseBody = c.w.logBody()
	}
	if c.x != nil {
		for _, p := range c.g.Post {
			p.PostProcess(c.bg, c.x, &c.entry)
		}
	}
	c.g.logs.Write(&c.entry)
	c.g.log.Info("request", "endpoint", c.ep.path, "model", c.entry.Model, "status", c.entry.StatusCode,
		"latency_ms", c.entry.LatencyMS, "attempts", c.entry.Attempts, "provider", c.entry.ProviderName)
}
