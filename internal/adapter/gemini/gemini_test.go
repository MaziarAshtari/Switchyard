package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("invalid JSON %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("invalid expected JSON %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func assertCanonicalEvents(t *testing.T, got, want []adapter.Event) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("events mismatch\n got: %s\nwant: %s", g, w)
	}
}

func ptr[T any](v T) *T { return &v }

func textPart(s string) adapter.Part { return adapter.Part{Type: adapter.PartText, Text: s} }

func imgPart(img adapter.Image) adapter.Part {
	return adapter.Part{Type: adapter.PartImage, Image: &img}
}

func callPart(id, name, args string) adapter.Part {
	return adapter.Part{Type: adapter.PartToolCall, ToolCall: &adapter.ToolCall{ID: id, Name: name, Arguments: args}}
}

func resultPart(callID, name, content string, isError bool) adapter.Part {
	return adapter.Part{Type: adapter.PartToolResult, ToolResult: &adapter.ToolResult{CallID: callID, Name: name, Content: content, IsError: isError}}
}

func TestURLs(t *testing.T) {
	const host = "https://generativelanguage.googleapis.com"
	tests := []struct {
		base, model string
		stream      bool
		want        string
	}{
		{host, "gemini-2.5-flash", false, host + "/v1beta/models/gemini-2.5-flash:generateContent"},
		{host, "gemini-2.5-flash", true, host + "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse"},
		{host, "models/gemini-2.5-pro", false, host + "/v1beta/models/gemini-2.5-pro:generateContent"},
		{host + "/v1beta", "gemini-2.5-pro", false, host + "/v1beta/models/gemini-2.5-pro:generateContent"},
		{host + "/v1", "gemini-2.5-pro", true, host + "/v1/models/gemini-2.5-pro:streamGenerateContent?alt=sse"},
		{host + "/v1alpha", "m", false, host + "/v1alpha/models/m:generateContent"},
		{host + "/v1beta2", "m", false, host + "/v1beta2/models/m:generateContent"},
		{"https://proxy.example.com/gemini", "m", false, "https://proxy.example.com/gemini/v1beta/models/m:generateContent"},
		{"https://proxy.example.com/v1beta/vertex", "m", false, "https://proxy.example.com/v1beta/vertex/v1beta/models/m:generateContent"},
		{host, "my model?x", false, host + "/v1beta/models/my%20model%3Fx:generateContent"},
	}
	a := New()
	if a.Format() != adapter.Gemini {
		t.Errorf("Format = %q", a.Format())
	}
	for _, tt := range tests {
		if got := a.ChatURL(tt.base, tt.model, tt.stream); got != tt.want {
			t.Errorf("ChatURL(%q, %q, %v) = %q, want %q", tt.base, tt.model, tt.stream, got, tt.want)
		}
	}
	if got := a.ModelsURL(host); got != host+"/v1beta/models" {
		t.Errorf("ModelsURL = %q", got)
	}
	if got := a.ModelsURL(host + "/v1beta"); got != host+"/v1beta/models" {
		t.Errorf("ModelsURL with version = %q", got)
	}
	h := http.Header{}
	a.SetAuth(h, "AIza-test")
	if got := h.Get("x-goog-api-key"); got != "AIza-test" {
		t.Errorf("x-goog-api-key = %q", got)
	}
}

func TestEncodeRequest(t *testing.T) {
	weatherSchema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	req := &adapter.Request{
		Model:  "gemini-best",
		System: "Be brief.",
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				textPart("What's this?"),
				textPart(""),
				imgPart(adapter.Image{MediaType: "image/png", Data: "AAAA"}),
				imgPart(adapter.Image{URL: "https://example.com/cat.WEBP?size=large"}),
				imgPart(adapter.Image{URL: "https://example.com/photo"}),
				imgPart(adapter.Image{URL: "https://example.com/x.png", MediaType: "image/gif"}),
			}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{
				textPart("Checking."),
				callPart("call_1", "get_weather", `{"city":"Paris"}`),
				callPart("call_2", "get_time", "not json"),
			}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				resultPart("call_1", "", "Sunny", false),
				resultPart("call_2", "", "boom", true),
			}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				resultPart("unknown", "legacy_fn", "x", false),
				resultPart("missing", "", "y", false),
			}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{textPart("Again.")}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{
				callPart("call_3", "get_weather", `{"city":"Rome"}`),
				callPart("call_4", "get_weather", `{"city":"Oslo"}`),
			}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{resultPart("call_3", "", "Rainy", false), resultPart("call_4", "", "Snow", false)}},
			{Role: adapter.RoleAssistant},
		},
		Tools: []adapter.Tool{
			{Name: "get_weather", Description: "Weather", Parameters: weatherSchema},
			{Name: "get_time"},
		},
		ToolChoice:  &adapter.ToolChoice{Mode: "tool", Name: "get_weather"},
		MaxTokens:   100,
		Temperature: ptr(0.3),
		TopP:        ptr(0.95),
		Stop:        []string{"END"},
		Stream:      true,
	}
	got, err := New().EncodeRequest(req, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	assertJSON(t, got, `{
		"contents": [
			{"role":"user","parts":[
				{"text":"What's this?"},
				{"inlineData":{"mimeType":"image/png","data":"AAAA"}},
				{"fileData":{"mimeType":"image/webp","fileUri":"https://example.com/cat.WEBP?size=large"}},
				{"fileData":{"mimeType":"image/jpeg","fileUri":"https://example.com/photo"}},
				{"fileData":{"mimeType":"image/gif","fileUri":"https://example.com/x.png"}}]},
			{"role":"model","parts":[
				{"text":"Checking."},
				{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"skip_thought_signature_validator"},
				{"functionCall":{"name":"get_time","args":{}}}]},
			{"role":"user","parts":[
				{"functionResponse":{"name":"get_weather","response":{"content":"Sunny"}}},
				{"functionResponse":{"name":"get_time","response":{"error":"boom"}}},
				{"functionResponse":{"name":"legacy_fn","response":{"content":"x"}}},
				{"functionResponse":{"name":"tool","response":{"content":"y"}}}]},
			{"role":"model","parts":[
				{"text":"Again."},
				{"functionCall":{"name":"get_weather","args":{"city":"Rome"}},"thoughtSignature":"skip_thought_signature_validator"},
				{"functionCall":{"name":"get_weather","args":{"city":"Oslo"}}}]},
			{"role":"user","parts":[
				{"functionResponse":{"name":"get_weather","response":{"content":"Rainy"}}},
				{"functionResponse":{"name":"get_weather","response":{"content":"Snow"}}}]}
		],
		"systemInstruction": {"parts":[{"text":"Be brief."}]},
		"tools": [{"functionDeclarations":[
			{"name":"get_weather","description":"Weather","parametersJsonSchema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}},
			{"name":"get_time","parametersJsonSchema":{"type":"object","properties":{}}}]}],
		"toolConfig": {"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["get_weather"]}},
		"generationConfig": {"maxOutputTokens":100,"temperature":0.3,"topP":0.95,"stopSequences":["END"]}
	}`)
}

func TestEncodeRequestMinimal(t *testing.T) {
	req := &adapter.Request{
		Messages:   []adapter.Message{{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Hi")}}},
		ToolChoice: &adapter.ToolChoice{Mode: "auto"}, // dropped: no tools
	}
	got, err := New().EncodeRequest(req, "m")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"contents":[{"role":"user","parts":[{"text":"Hi"}]}]}`)
}

func TestEncodeRequestToolConfig(t *testing.T) {
	tests := map[string]string{"auto": "AUTO", "none": "NONE", "required": "ANY"}
	for mode, want := range tests {
		req := &adapter.Request{Tools: []adapter.Tool{{Name: "f"}}, ToolChoice: &adapter.ToolChoice{Mode: mode}}
		got, err := New().EncodeRequest(req, "m")
		if err != nil {
			t.Fatal(err)
		}
		var out request
		if err := json.Unmarshal(got, &out); err != nil {
			t.Fatal(err)
		}
		if c := out.ToolConfig; c == nil || c.FunctionCallingConfig.Mode != want || c.FunctionCallingConfig.AllowedFunctionNames != nil {
			t.Errorf("mode %s: toolConfig = %+v, want %s", mode, c, want)
		}
	}
}

func TestDecodeResponse(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[
			{"text":"Planning...","thought":true},
			{"text":"Hello "},
			{"text":"world"},
			{"functionCall":{"id":"fc_1","name":"get_weather","args":{"city": "Paris"}},"thoughtSignature":"abc"},
			{"functionCall":{"name":"get_time"}}
		],"role":"model"},"finishReason":"STOP","index":0}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":7,"totalTokenCount":22},
		"modelVersion":"gemini-2.5-flash","responseId":"resp_1"}`
	got, err := New().DecodeResponse([]byte(body))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if len(got.Content) != 4 || !strings.HasPrefix(got.Content[3].ToolCall.ID, "call_") {
		t.Fatalf("unexpected content %+v", got.Content)
	}
	got.Content[3].ToolCall.ID = "generated"
	want := adapter.Response{
		ID: "resp_1", Model: "gemini-2.5-flash",
		Content: []adapter.Part{
			textPart("Hello "),
			textPart("world"),
			callPart("fc_1", "get_weather", `{"city":"Paris"}`),
			callPart("generated", "get_time", "{}"),
		},
		StopReason: adapter.StopToolUse,
		Usage:      adapter.Usage{InputTokens: 10, OutputTokens: 12},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %+v\nwant %+v", *got, want)
	}
}

func TestDecodeResponseStopReasons(t *testing.T) {
	tests := map[string]adapter.StopReason{
		"STOP":               adapter.StopEnd,
		"MAX_TOKENS":         adapter.StopMaxTokens,
		"SAFETY":             adapter.StopContentFilter,
		"RECITATION":         adapter.StopContentFilter,
		"BLOCKLIST":          adapter.StopContentFilter,
		"PROHIBITED_CONTENT": adapter.StopContentFilter,
		"SPII":               adapter.StopContentFilter,
		"IMAGE_SAFETY":       adapter.StopContentFilter,
		"OTHER":              adapter.StopEnd,
		"LANGUAGE":           adapter.StopEnd,
	}
	for reason, want := range tests {
		body := `{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"finishReason":"` + reason + `"}]}`
		got, err := New().DecodeResponse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s: got %q, want %q", reason, got.StopReason, want)
		}
	}
}

func TestDecodeResponseBlockedPrompt(t *testing.T) {
	body := `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":8,"totalTokenCount":8},"modelVersion":"gemini-2.5-flash","responseId":"r"}`
	got, err := New().DecodeResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Response{ID: "r", Model: "gemini-2.5-flash", StopReason: adapter.StopContentFilter, Usage: adapter.Usage{InputTokens: 8}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %+v, want %+v", *got, want)
	}
	if _, err := New().DecodeResponse([]byte(`{"candidates":[]}`)); err == nil {
		t.Error("a response without candidates or block reason should fail")
	}
}

func TestDecodeResponseErrors(t *testing.T) {
	tests := []struct {
		body string
		want adapter.Error
	}{
		{`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`, adapter.Error{Status: 429, Message: "Resource has been exhausted"}},
		{`{"error":{"code":0,"message":"weird"}}`, adapter.Error{Status: 502, Message: "weird"}},
		{`[{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}]`, adapter.Error{Status: 503, Message: "The model is overloaded."}},
	}
	for _, tt := range tests {
		_, err := New().DecodeResponse([]byte(tt.body))
		var ae *adapter.Error
		if !errors.As(err, &ae) || *ae != tt.want {
			t.Errorf("%s: got %v, want %v", tt.body, err, tt.want)
		}
	}
	if _, err := New().DecodeResponse([]byte("<html>")); err == nil {
		t.Error("invalid JSON should fail")
	}
}

const geminiStream = `data: {"candidates": [{"content": {"parts": [{"text": "Considering the request", "thought": true}],"role": "model"},"index": 0}],"usageMetadata": {"promptTokenCount": 12,"totalTokenCount": 32,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

data: {"candidates": [{"content": {"parts": [{"text": "Hel"}],"role": "model"},"index": 0}],"usageMetadata": {"promptTokenCount": 12,"candidatesTokenCount": 1,"totalTokenCount": 33,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

data: {"candidates": [{"content": {"parts": [{"text": "lo"},{"functionCall": {"id": "fc_1","name": "get_weather","args": {"city": "Paris"}},"thoughtSignature": "CiQBVKhc7"},{"functionCall": {"id": "fc_2","name": "get_time"}}],"role": "model"},"finishReason": "STOP","index": 0}],"usageMetadata": {"promptTokenCount": 12,"candidatesTokenCount": 8,"totalTokenCount": 40,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

`

func TestStreamDecoder(t *testing.T) {
	d := New().NewStreamDecoder()
	var got []adapter.Event
	for _, e := range readSSE(t, geminiStream) {
		out, err := d.Decode(e)
		if err != nil {
			t.Fatalf("Decode: %v", err) // also catches io.EOF, which must never be returned
		}
		got = append(got, out...)
	}
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "resp-1", Model: "gemini-2.5-flash"},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 12, OutputTokens: 20}},
		{Type: adapter.EventText, Text: "Hel"},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 12, OutputTokens: 21}},
		{Type: adapter.EventText, Text: "lo"},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "fc_1", ToolName: "get_weather"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `{"city":"Paris"}`},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "fc_2", ToolName: "get_time"},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: `{}`},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 12, OutputTokens: 28}},
	})
}

func TestStreamDecoderStopAndBlock(t *testing.T) {
	d := New().NewStreamDecoder()
	got, err := d.Decode(sse.Event{Data: `{"candidates":[{"content":{"parts":[{"text":"Done"}],"role":"model"},"finishReason":"MAX_TOKENS"}],"responseId":"r","modelVersion":"m"}`})
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "r", Model: "m"},
		{Type: adapter.EventText, Text: "Done"},
		{Type: adapter.EventStop, StopReason: adapter.StopMaxTokens},
	})

	got, err = New().NewStreamDecoder().Decode(sse.Event{Data: `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"},"responseId":"r","modelVersion":"m"}`})
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "r", Model: "m"},
		{Type: adapter.EventStop, StopReason: adapter.StopContentFilter},
	})
}

func TestStreamDecoderErrors(t *testing.T) {
	tests := []struct {
		data string
		want adapter.Error
	}{
		{`{"error":{"code":500,"message":"Internal error encountered.","status":"INTERNAL"}}`, adapter.Error{Status: 500, Message: "Internal error encountered."}},
		{`[{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}]`, adapter.Error{Status: 429, Message: "Quota exceeded"}},
	}
	for _, tt := range tests {
		_, err := New().NewStreamDecoder().Decode(sse.Event{Data: tt.data})
		var ae *adapter.Error
		if !errors.As(err, &ae) || *ae != tt.want {
			t.Errorf("%s: got %v, want %v", tt.data, err, tt.want)
		}
	}
	d := New().NewStreamDecoder()
	for _, data := range []string{"", "[DONE]"} {
		if got, err := d.Decode(sse.Event{Data: data}); err != nil || len(got) != 0 {
			t.Errorf("Decode(%q) = %v, %v; want nothing", data, got, err)
		}
	}
	if _, err := d.Decode(sse.Event{Data: "not json"}); err == nil || err == io.EOF {
		t.Errorf("invalid data: got %v, want an error", err)
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct{ body, want string }{
		{`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`, "API key not valid. Please pass a valid API key."},
		{`[{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}]`, "The model is overloaded."},
		{`{"error":{"code":404}}`, `{"error":{"code":404}}`},
		{"  Service Unavailable\n", "Service Unavailable"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := New().ErrorMessage([]byte(tt.body)); got != tt.want {
			t.Errorf("ErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}

func readSSE(t *testing.T, stream string) []sse.Event {
	t.Helper()
	r := sse.NewReader(strings.NewReader(stream))
	var out []sse.Event
	for {
		e, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("reading SSE: %v", err)
		}
		out = append(out, e)
	}
}
