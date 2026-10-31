package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

func collectImages(t *testing.T, f adapter.Format, body string) ([]bridgeImage, map[string]any, int) {
	t.Helper()
	var seen []bridgeImage
	out, n, err := replaceImages(f, []byte(body), func(img bridgeImage) (string, error) {
		seen = append(seen, img)
		return fmt.Sprintf("image %d", len(seen)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid output %s: %v", out, err)
	}
	if strings.Contains(body, "<brief>") && !strings.Contains(string(out), "<brief>") {
		t.Fatalf("untouched strings must not be HTML-escaped: %s", out)
	}
	return seen, v, n
}

// part returns messages[msg].content[i] of a decoded request.
func part(v map[string]any, msg, i int) map[string]any {
	return v["messages"].([]any)[msg].(map[string]any)["content"].([]any)[i].(map[string]any)
}

func TestReplaceImagesOpenAIFormat(t *testing.T) {
	seen, v, n := collectImages(t, adapter.OpenAI, `{"model":"m","seed":7,"messages":[
		{"role":"system","content":"Be <brief>"},
		{"role":"user","content":[{"type":"text","text":"What is this?"},
			{"type":"image_url","image_url":{"url":"https://img.example/a.png","detail":"high"}},
			{"type":"image_url","image_url":{"url":""}}]},
		{"role":"assistant","content":null},
		{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
	want := []bridgeImage{{URL: "https://img.example/a.png", Context: "What is this?"}, {URL: "data:image/png;base64,AAAA"}}
	if n != 2 || len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("images = %d %+v", n, seen)
	}
	if p := part(v, 1, 1); p["type"] != "text" || p["text"] != "[Image description]: image 1" || len(p) != 2 {
		t.Fatalf("replaced part: %v", p)
	}
	if p := part(v, 1, 2); p["type"] != "image_url" {
		t.Fatalf("an image without URL is left alone: %v", p)
	}
	if p := part(v, 3, 0); p["text"] != "[Image description]: image 2" {
		t.Fatalf("second message: %v", p)
	}
	if v["seed"] != float64(7) || v["model"] != "m" {
		t.Fatalf("other fields must be kept: %v", v)
	}
}

func TestReplaceImagesAnthropicFormatIncludingToolResults(t *testing.T) {
	seen, v, n := collectImages(t, adapter.Anthropic, `{"model":"m","max_tokens":10,"system":"Be <brief>","messages":[
		{"role":"user","content":[
			{"type":"text","text":"Compare these"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"},"cache_control":{"type":"ephemeral"}},
			{"type":"image","source":{"type":"file","file_id":"file_1"}},
			{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"screenshot"},
				{"type":"image","source":{"type":"url","url":"https://img.example/s.png"}}]}]}]}`)
	want := []bridgeImage{{URL: "data:image/png;base64,AAAA", Context: "Compare these"}, {URL: "https://img.example/s.png", Context: "screenshot"}}
	if n != 2 || len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("images = %d %+v", n, seen)
	}
	p := part(v, 0, 1)
	if p["text"] != "[Image description]: image 1" || p["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("replaced block must keep cache_control: %v", p)
	}
	if p := part(v, 0, 2); p["type"] != "image" {
		t.Fatalf("file images cannot be described and are left alone: %v", p)
	}
	tr := part(v, 0, 3)
	nested := tr["content"].([]any)[1].(map[string]any)
	if tr["tool_use_id"] != "t1" || nested["text"] != "[Image description]: image 2" {
		t.Fatalf("tool result: %v", tr)
	}
}

func TestReplaceImagesLeavesOtherBodiesAlone(t *testing.T) {
	called := false
	describe := func(bridgeImage) (string, error) { called = true; return "x", nil }
	for _, body := range []string{
		`not json`,
		`{"model":"m","messages":"nope"}`,
		`{"model":"m","messages":[{"role":"user","content":"plain text"}]}`,
	} {
		out, n, err := replaceImages(adapter.OpenAI, []byte(body), describe)
		if err != nil || n != 0 || string(out) != body {
			t.Fatalf("%s: %s %d %v", body, out, n, err)
		}
	}
	if called {
		t.Fatal("describe must not be called without images")
	}
	boom := errors.New("boom")
	_, _, err := replaceImages(adapter.OpenAI, []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`),
		func(bridgeImage) (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("describe errors must be returned: %v", err)
	}
}

func visionReply(text string) string {
	b, _ := json.Marshal(map[string]any{"id": "chatcmpl-v", "object": "chat.completion", "created": 1, "model": "seer-x",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110}})
	return string(b)
}

// bridgeEnv has a "seer" model on a vision upstream and a "blind" model on a
// text-only upstream whose vision bridge uses seer.
type bridgeEnv struct {
	*env
	vision, text *upstream
	seer, blind  store.Model
}

func newBridgeEnv(t *testing.T, textType string, visionHandler func(w http.ResponseWriter, r *http.Request, body []byte)) *bridgeEnv {
	e := newEnv(t)
	b := &bridgeEnv{env: e}
	b.vision = newUpstream(t, visionHandler)
	b.text = newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		if textType == "anthropic" {
			io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
			return
		}
		io.WriteString(w, chatOK)
	})
	pv := e.provider("Vision", "openai", b.vision.URL)
	e.key(pv.ID, "sk-vision-00000000000")
	pt := e.provider("Text", textType, b.text.URL)
	e.key(pt.ID, "sk-text-0000000000000")
	b.seer = e.model("seer", "chat", nil, target(pv.ID, "seer-x"))
	b.blind = e.model("blind", "chat", func(in *store.ModelInput) {
		in.VisionBridgeEnabled, in.VisionBridgeModelID, in.VisionBridgePrompt = true, &b.seer.ID, "Describe it."
	}, target(pt.ID, "text-only"))
	return b
}

func describeAs(text string) func(w http.ResponseWriter, r *http.Request, body []byte) {
	return func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, visionReply(text))
	}
}

func TestVisionBridgeReplacesImagesBeforeRouting(t *testing.T) {
	b := newBridgeEnv(t, "openai", describeAs("A cat on a sofa"))

	resp, body := b.post("/v1/chat/completions", `{"model":"blind","seed":42,"messages":[{"role":"user","content":[
		{"type":"text","text":"Who is on the sofa?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`)
	if resp.StatusCode != 200 || body != chatOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}

	vc := b.vision.calls()
	if len(vc) != 1 {
		t.Fatalf("vision calls: %d", len(vc))
	}
	var sub struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content []struct {
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	json.Unmarshal(vc[0].Body, &sub)
	if sub.Model != "seer-x" || sub.Stream || len(sub.Messages) != 1 || len(sub.Messages[0].Content) != 2 {
		t.Fatalf("vision request: %s", vc[0].Body)
	}
	if c := sub.Messages[0].Content; c[0].Text != "Describe it.\n\nText of the user's message that contains the image:\nWho is on the sofa?" ||
		c[1].ImageURL.URL != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("vision prompt or image: %s", vc[0].Body)
	}

	tc := b.text.calls()
	if len(tc) != 1 || strings.Contains(string(tc[0].Body), "image_url") {
		t.Fatalf("text model must not receive images: %d calls, %s", len(tc), tc[0].Body)
	}
	var sent map[string]any
	json.Unmarshal(tc[0].Body, &sent)
	content := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if sent["seed"] != float64(42) || sent["model"] != "text-only" || len(content) != 2 ||
		content[1].(map[string]any)["text"] != "[Image description]: A cat on a sofa" {
		t.Fatalf("forwarded body: %s", tc[0].Body)
	}

	parent := b.waitLog(2)
	if parent.Model != "blind" || parent.Attempts != 1 || len(parent.AttemptDetails) != 2 {
		t.Fatalf("parent log: %+v", parent)
	}
	if a := parent.AttemptDetails[0]; a.VisionBridge != "seer" || a.ProviderName != "Vision" || a.StatusCode != 200 {
		t.Fatalf("bridge entry: %+v", a)
	}
	if a := parent.AttemptDetails[1]; a.VisionBridge != "" || a.ProviderName != "Text" {
		t.Fatalf("upstream attempt: %+v", a)
	}
	items, _, _ := b.st.ListLogs(ctx, store.LogFilter{Model: "seer"})
	if len(items) != 1 || items[0].StatusCode != 200 || items[0].InputTokens == nil || *items[0].InputTokens != 100 {
		t.Fatalf("the sub-request must be logged with its usage: %+v", items)
	}

	// Requests without images are forwarded without calling the vision model.
	if resp, _ := b.post("/v1/chat/completions", `{"model":"blind","messages":[{"role":"user","content":"hi"}]}`); resp.StatusCode != 200 {
		t.Fatalf("text-only request: %d", resp.StatusCode)
	}
	if len(b.vision.calls()) != 1 {
		t.Fatal("the vision model must only be called for images")
	}
}

func TestVisionBridgeAnthropicClientPassthrough(t *testing.T) {
	b := newBridgeEnv(t, "anthropic", describeAs("A bar chart"))

	resp, body := b.post("/v1/messages", `{"model":"blind","max_tokens":50,"messages":[
		{"role":"user","content":[{"type":"text","text":"Summarise"},
			{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"/9j/4AAQ"}}]}]}]}`,
		"anthropic-version", "2023-06-01")
	if resp.StatusCode != 200 || !strings.Contains(body, `"text":"ok"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if vc := b.vision.calls(); len(vc) != 1 || !strings.Contains(string(vc[0].Body), `"url":"data:image/jpeg;base64,/9j/4AAQ"`) {
		t.Fatalf("vision request: %v", vc)
	}
	tc := b.text.calls()
	var sent map[string]any
	json.Unmarshal(tc[0].Body, &sent)
	result := part(sent, 0, 1)
	block := result["content"].([]any)[0].(map[string]any)
	if result["tool_use_id"] != "toolu_1" || block["type"] != "text" || block["text"] != "[Image description]: A bar chart" ||
		tc[0].Header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("forwarded body: %s", tc[0].Body)
	}
}

func TestVisionBridgeFailureFailsTheRequest(t *testing.T) {
	b := newBridgeEnv(t, "openai", func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"vision overloaded"}}`)
	})
	const withImage = `{"model":"blind","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://img.example/a.png"}}]}]}`

	resp, body := b.post("/v1/chat/completions", withImage)
	if resp.StatusCode != 503 || !strings.Contains(body, "Vision bridge failed") || !strings.Contains(body, "seer") ||
		!strings.Contains(body, "vision overloaded") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if len(b.text.calls()) != 0 {
		t.Fatal("the main request must not be routed when the bridge fails")
	}
	parent := b.waitLog(2)
	if parent.Model != "blind" || parent.StatusCode != 503 || !strings.Contains(parent.Error, "Vision bridge failed") ||
		parent.Attempts != 0 || len(parent.AttemptDetails) != 1 || parent.AttemptDetails[0].StatusCode != 503 {
		t.Fatalf("parent log: %+v", parent)
	}

	if err := b.st.DeleteModel(ctx, b.seer.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = b.post("/v1/chat/completions", withImage)
	if resp.StatusCode != 503 || !strings.Contains(body, "no vision model is selected") {
		t.Fatalf("without vision model: %d %s", resp.StatusCode, body)
	}
}

func TestVisionBridgeDoesNotRecurse(t *testing.T) {
	var sawImage atomic.Bool
	b := newBridgeEnv(t, "openai", func(w http.ResponseWriter, r *http.Request, body []byte) {
		sawImage.Store(strings.Contains(string(body), "image_url"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, visionReply("A dog"))
	})
	// seer has a bridge of its own (back to blind); it must not run inside a bridge call.
	in := store.ModelInput{Name: "seer", Type: "chat", MaxTotalAttempts: 5, MaxKeysPerTarget: 5, AttemptTimeoutSeconds: 30,
		VisionBridgeEnabled: true, VisionBridgeModelID: &b.blind.ID,
		Targets: []store.TargetInput{target(b.seer.Targets[0].ProviderID, "seer-x")}}
	if _, err := b.st.UpdateModel(ctx, b.seer.ID, in); err != nil {
		t.Fatal(err)
	}

	resp, body := b.post("/v1/chat/completions", `{"model":"blind","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://img.example/dog.png"}}]}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if len(b.vision.calls()) != 1 || !sawImage.Load() || len(b.text.calls()) != 1 {
		t.Fatalf("vision calls %d (image %v), text calls %d", len(b.vision.calls()), sawImage.Load(), len(b.text.calls()))
	}
}
