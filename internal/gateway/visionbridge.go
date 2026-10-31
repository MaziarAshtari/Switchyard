package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// imageDescriptionPrefix starts the text part that replaces an image.
const imageDescriptionPrefix = "[Image description]: "

// bridgeContextLimit bounds the accompanying message text sent with each image (bytes).
const bridgeContextLimit = 4000

// visionBridge is the pre-processor that lets chat models without vision
// accept images. Each image of the request is described by the model's vision
// bridge model, through the normal routing and retry engine (and logged as a
// request of its own), and replaced by a text part holding the description.
type visionBridge struct{ g *Gateway }

// inVisionBridge marks the context of a vision bridge sub-request: the bridge
// never runs inside one, so the depth is at most 1.
type inVisionBridge struct{}

func (b visionBridge) PreProcess(ctx context.Context, x *Exchange) error {
	if !x.Model.VisionBridgeEnabled || ctx.Value(inVisionBridge{}) != nil {
		return nil
	}
	var vision *store.Model // loaded with the first image
	body, n, err := replaceImages(x.Format, x.Body, func(img bridgeImage) (string, error) {
		if vision == nil {
			vm, err := b.visionModel(ctx, x.Model)
			if err != nil {
				return "", err
			}
			vision = &vm
		}
		return b.describe(ctx, x, *vision, img)
	})
	if err != nil {
		return err
	}
	if n > 0 {
		x.Body = body
	}
	return nil
}

// visionModel loads the model that describes images for m.
func (b visionBridge) visionModel(ctx context.Context, m store.Model) (store.Model, error) {
	unavailable := func(reason string) error {
		return &ProcessError{Status: http.StatusServiceUnavailable,
			Message: fmt.Sprintf("Vision bridge failed: %s; check the vision bridge of model '%s'.", reason, m.Name)}
	}
	if m.VisionBridgeModelID == nil {
		return store.Model{}, unavailable("no vision model is selected")
	}
	vm, err := b.g.store.GetModel(ctx, *m.VisionBridgeModelID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return vm, unavailable("the vision model no longer exists")
	case err != nil:
		b.g.log.Error("vision bridge: load model", "err", err)
		return vm, &ProcessError{Status: http.StatusInternalServerError, Message: "Internal error"}
	case vm.Type != "chat":
		return vm, unavailable(fmt.Sprintf("vision model '%s' is not a chat model", vm.Name))
	}
	return vm, nil
}

// describe asks the vision model to describe one image (a non-streaming
// OpenAI-format request through the gateway) and returns the description.
func (b visionBridge) describe(ctx context.Context, x *Exchange, vm store.Model, img bridgeImage) (string, error) {
	prompt := x.Model.VisionBridgePrompt
	if text := strings.TrimSpace(img.Context); text != "" {
		prompt += "\n\nText of the user's message that contains the image:\n" + adapter.Truncate(text, bridgeContextLimit)
	}
	body, err := encodeJSON(map[string]any{
		"model":  vm.Name,
		"stream": false,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": img.URL}},
		}}},
	})
	if err != nil {
		return "", err
	}
	req := httptest.NewRequestWithContext(context.WithValue(ctx, inVisionBridge{}, true), http.MethodPost, epChat.path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	sub := b.g.serve(rec, req, epChat)
	x.SubRequests = append(x.SubRequests, store.Attempt{
		ProviderName: sub.ProviderName, KeyHint: sub.KeyHint, UpstreamModel: sub.UpstreamModel,
		StatusCode: sub.StatusCode, LatencyMS: sub.LatencyMS, Error: sub.Error, VisionBridge: vm.Name,
	})

	fail := func(status int, reason string) error {
		return &ProcessError{Status: status,
			Message: fmt.Sprintf("Vision bridge failed: model '%s' could not describe an image: %s", vm.Name, reason)}
	}
	// The sub-request's status is already client-safe (upstream auth errors became 502).
	if sub.StatusCode < 200 || sub.StatusCode > 299 {
		status := sub.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		return "", fail(status, sub.Error)
	}
	resp, err := b.g.upstream[string(adapter.OpenAI)].DecodeResponse(rec.Body.Bytes())
	if err != nil {
		return "", fail(http.StatusBadGateway, err.Error())
	}
	var text strings.Builder
	for _, p := range resp.Content {
		if p.Type == adapter.PartText {
			text.WriteString(p.Text)
		}
	}
	desc := strings.TrimSpace(text.String())
	if desc == "" {
		return "", fail(http.StatusBadGateway, "the response contains no text")
	}
	return desc, nil
}

// bridgeImage is one image of a chat request: an http(s) URL or a data URI,
// with the text next to it (same message or tool result) as context.
type bridgeImage struct {
	URL     string
	Context string
}

// replaceImages calls describe for each image part of a chat request body in
// client format f and replaces the part with a text part holding
// "[Image description]: <description>". It returns the rewritten body and the
// number of replaced images. Parts it does not understand are left alone; the
// request decoder reports invalid bodies afterwards.
func replaceImages(f adapter.Format, body []byte, describe func(bridgeImage) (string, error)) ([]byte, int, error) {
	var (
		req  map[string]json.RawMessage
		msgs []json.RawMessage
	)
	if json.Unmarshal(body, &req) != nil || json.Unmarshal(req["messages"], &msgs) != nil {
		return body, 0, nil
	}
	w := imageWalker{format: f, describe: describe}
	changed := false
	for i, raw := range msgs {
		var msg map[string]json.RawMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		content, ok, err := w.content(msg["content"])
		if err != nil {
			return nil, w.count, err
		}
		if !ok {
			continue
		}
		msg["content"] = content
		if msgs[i], err = encodeJSON(msg); err != nil {
			return nil, w.count, err
		}
		changed = true
	}
	if !changed {
		return body, 0, nil
	}
	var err error
	if req["messages"], err = encodeJSON(msgs); err != nil {
		return nil, w.count, err
	}
	out, err := encodeJSON(req)
	return out, w.count, err
}

type imageWalker struct {
	format   adapter.Format
	describe func(bridgeImage) (string, error)
	count    int
}

// textPart replaces an image; cache_control is kept so Anthropic prompt
// caching breakpoints stay where the client put them.
type textPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// content rewrites the images of one content array, including those inside
// Anthropic tool results. Strings and other values are returned unchanged;
// ok reports whether anything changed.
func (w *imageWalker) content(raw json.RawMessage) (json.RawMessage, bool, error) {
	var parts []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &parts) != nil {
		return raw, false, nil
	}
	decoded := make([]map[string]json.RawMessage, len(parts))
	var texts []string
	for i, p := range parts {
		if json.Unmarshal(p, &decoded[i]) != nil || partType(decoded[i]) != "text" {
			continue
		}
		var t string
		if json.Unmarshal(decoded[i]["text"], &t) == nil && strings.TrimSpace(t) != "" {
			texts = append(texts, t)
		}
	}
	context := strings.Join(texts, "\n")
	changed := false
	for i, part := range decoded {
		var err error
		switch url := w.imageURL(part); {
		case url != "":
			var desc string
			if desc, err = w.describe(bridgeImage{URL: url, Context: context}); err != nil {
				return nil, false, err
			}
			w.count++
			parts[i], err = encodeJSON(textPart{Type: "text", Text: imageDescriptionPrefix + desc, CacheControl: part["cache_control"]})
			changed = true
		case w.format == adapter.Anthropic && partType(part) == "tool_result":
			nested, ok, nerr := w.content(part["content"])
			if nerr != nil {
				return nil, false, nerr
			}
			if ok {
				part["content"] = nested
				parts[i], err = encodeJSON(part)
				changed = true
			}
		}
		if err != nil {
			return nil, false, err
		}
	}
	if !changed {
		return raw, false, nil
	}
	out, err := encodeJSON(parts)
	return out, err == nil, err
}

// imageURL returns the URL or data URI of an image part; "" for other parts
// and for images that cannot be passed on (such as Anthropic file references).
func (w *imageWalker) imageURL(part map[string]json.RawMessage) string {
	switch t := partType(part); {
	case w.format == adapter.OpenAI && t == "image_url":
		var v struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(part["image_url"], &v)
		return v.URL
	case w.format == adapter.Anthropic && t == "image":
		var s struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
			URL       string `json:"url"`
		}
		_ = json.Unmarshal(part["source"], &s)
		switch {
		case s.Type == "base64" && s.Data != "":
			return "data:" + s.MediaType + ";base64," + s.Data
		case s.Type == "url":
			return s.URL
		}
	}
	return ""
}

func partType(part map[string]json.RawMessage) string {
	var t string
	_ = json.Unmarshal(part["type"], &t)
	return t
}
