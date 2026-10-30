package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// relay delivers a 2xx upstream response to the client. commit must be called
// right before the first byte is written; it returns false when the attempt
// timed out in the meantime.
func (c *call) relay(ctx context.Context, resp *http.Response, p store.Provider, a routing.Attempt, m mode, commit func() bool) result {
	switch {
	case m.chat && m.stream:
		return c.relayStream(ctx, resp, m, commit)
	case m.chat:
		return c.relayChat(ctx, resp, m, commit)
	default:
		return c.relayOther(ctx, resp, p, a, commit)
	}
}

var (
	delivered = result{outcome: routing.Outcome{Status: http.StatusOK, Committed: true}, status: http.StatusOK, done: true}
	timedOut  = result{outcome: routing.Outcome{Timeout: true}, errMsg: "attempt timed out"}
)

// badResponse turns an invalid or error-carrying 2xx response into a failure.
func badResponse(err error) result {
	var ue *adapter.Error
	if errors.As(err, &ue) {
		return result{outcome: routing.Outcome{Status: ue.Status}, status: ue.Status, errMsg: ue.Error()}
	}
	return result{outcome: routing.Outcome{Status: http.StatusBadGateway}, status: http.StatusBadGateway,
		errMsg: "invalid upstream response: " + err.Error()}
}

func (c *call) relayChat(ctx context.Context, resp *http.Response, m mode, commit func() bool) result {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return c.transportError(ctx, err)
	}
	decoded, err := m.up.DecodeResponse(body)
	var ue *adapter.Error
	switch {
	case errors.As(err, &ue):
		return badResponse(err)
	case err != nil && !m.passthrough:
		return badResponse(err)
	}
	out, contentType := body, resp.Header.Get("Content-Type")
	if !m.passthrough {
		if out, err = c.g.inbound[c.ep.format].EncodeResponse(decoded); err != nil {
			return badResponse(err)
		}
		contentType = "application/json"
	}
	if contentType == "" {
		contentType = "application/json"
	}
	if !commit() {
		return timedOut
	}
	if decoded != nil {
		c.setUsage(decoded.Usage)
	}
	c.w.Header().Set("Content-Type", contentType)
	c.w.WriteHeader(http.StatusOK)
	c.w.Write(out)
	return delivered
}

// relayStream relays an SSE stream. The first upstream event is read before
// anything is sent, so failures up to that point can still be retried.
func (c *call) relayStream(ctx context.Context, resp *http.Response, m mode, commit func() bool) result {
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return result{outcome: routing.Outcome{Status: http.StatusBadGateway}, status: http.StatusBadGateway,
			errMsg: "upstream answered a stream request with JSON: " + m.up.ErrorMessage(body)}
	}
	reader := sse.NewReader(resp.Body)
	dec := m.up.NewStreamDecoder()
	var enc adapter.StreamEncoder
	if !m.passthrough {
		enc = c.g.inbound[c.ep.format].NewStreamEncoder(c.chat)
	}
	var (
		usage    adapter.Usage
		sawUsage bool
		ended    bool
	)
	// convert decodes one upstream event and returns what the client gets.
	convert := func(ev sse.Event) ([]sse.Event, error) {
		events, err := dec.Decode(ev)
		for _, e := range events {
			if e.Type == adapter.EventUsage && e.Usage != nil {
				usage, sawUsage = *e.Usage, true
			}
		}
		if errors.Is(err, io.EOF) {
			ended, err = true, nil
		}
		if m.passthrough {
			return []sse.Event{ev}, err
		}
		var out []sse.Event
		for _, e := range events {
			out = append(out, enc.Encode(e)...)
		}
		return out, err
	}

	var pending []sse.Event
	for len(pending) == 0 && !ended {
		ev, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return result{outcome: routing.Outcome{Status: http.StatusBadGateway}, status: http.StatusBadGateway,
				errMsg: "upstream closed the stream before sending any data"}
		}
		if err != nil {
			return c.transportError(ctx, err)
		}
		if pending, err = convert(ev); err != nil {
			return badResponse(err)
		}
	}
	if !commit() {
		return timedOut
	}

	h := c.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	c.w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(c.w)
	write := func(events []sse.Event) bool {
		if err := sse.Write(c.w, events...); err != nil {
			return false
		}
		rc.Flush()
		return true
	}
	defer func() {
		if sawUsage {
			c.setUsage(usage)
		}
	}()

	if !write(pending) {
		c.entry.Error = "client disconnected"
		return delivered
	}
	for !ended {
		ev, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if c.ctx.Err() != nil {
				c.entry.Error = "client disconnected"
				return delivered
			}
			c.entry.Error = "upstream stream interrupted: " + err.Error()
			if enc != nil {
				write(enc.Error(c.entry.Error))
			}
			return delivered
		}
		out, err := convert(ev)
		if err != nil {
			msg := err.Error()
			var ue *adapter.Error
			if errors.As(err, &ue) {
				msg = ue.Message
			}
			c.entry.Error = "upstream error during stream: " + msg
			if enc != nil {
				out = append(out, enc.Error(msg)...)
			}
			write(out)
			return delivered
		}
		if !write(out) {
			c.entry.Error = "client disconnected"
			return delivered
		}
	}
	if enc != nil {
		write(enc.Finish())
	}
	return delivered
}

// relayOther relays a non-chat response: JSON/text is buffered (to read usage
// and video ids), binary content such as audio is streamed through.
func (c *call) relayOther(ctx context.Context, resp *http.Response, p store.Provider, a routing.Attempt, commit func() bool) result {
	contentType := resp.Header.Get("Content-Type")
	if isTextual(contentType) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if err != nil {
			return c.transportError(ctx, err)
		}
		if !commit() {
			return timedOut
		}
		if c.ep.modelType == "video" {
			c.saveVideoJob(body, p, a)
		}
		c.entry.InputTokens, c.entry.OutputTokens = usageOf(body)
		c.w.Header().Set("Content-Type", contentType)
		c.w.WriteHeader(resp.StatusCode)
		c.w.Write(body)
		return delivered
	}
	if !commit() {
		return timedOut
	}
	copyHeaders(c.w.Header(), resp.Header, "Content-Type", "Content-Length", "Content-Disposition")
	c.w.WriteHeader(resp.StatusCode)
	if err := copyFlush(c.w, resp.Body); err != nil {
		c.entry.Error = "response interrupted: " + err.Error()
	}
	return delivered
}

func (c *call) saveVideoJob(body []byte, p store.Provider, a routing.Attempt) {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &v) != nil || v.ID == "" {
		return
	}
	job := store.VideoJob{ID: v.ID, Model: c.x.Model.Name, ProviderID: p.ID, KeyID: a.Key.ID}
	if err := c.g.store.SaveVideoJob(c.bg, job); err != nil {
		c.g.log.Error("save video job", "id", v.ID, "err", err)
	}
}

func copyHeaders(dst, src http.Header, names ...string) {
	for _, n := range names {
		if v := src.Get(n); v != "" {
			dst.Set(n, v)
		}
	}
}

// copyFlush copies src to w, flushing after every chunk so streamed audio or
// video reaches the client without delay.
func copyFlush(w http.ResponseWriter, src io.Reader) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			rc.Flush()
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
