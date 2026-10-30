package openai

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

func TestURLsAndAuth(t *testing.T) {
	a := New()
	if a.Format() != adapter.OpenAI {
		t.Errorf("Format = %q", a.Format())
	}
	if got := a.ChatURL("https://api.openai.com/v1", "gpt-4o", true); got != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("ChatURL = %q", got)
	}
	if got := a.ModelsURL("https://api.openai.com/v1"); got != "https://api.openai.com/v1/models" {
		t.Errorf("ModelsURL = %q", got)
	}
	h := http.Header{}
	a.SetAuth(h, "sk-test")
	if got := h.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestEncodeRequest(t *testing.T) {
	weatherSchema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	tests := []struct {
		name string
		req  adapter.Request
		want string
	}{
		{
			name: "full conversation",
			req: adapter.Request{
				Model:  "gpt-best",
				System: "Be brief.",
				Messages: []adapter.Message{
					{Role: adapter.RoleUser, Parts: []adapter.Part{
						textPart("What's the weather?"),
						imagePart(adapter.Image{MediaType: "image/png", Data: "AAAA"}),
						imagePart(adapter.Image{URL: "https://example.com/cat.jpg"}),
					}},
					{Role: adapter.RoleAssistant, Parts: []adapter.Part{
						textPart("Checking."),
						callPart("call_1", "get_weather", `{"city":"Paris"}`),
						callPart("toolu_2", "get_time", ""),
					}},
					{Role: adapter.RoleUser, Parts: []adapter.Part{
						resultPart("call_1", "Sunny", false),
						resultPart("toolu_2", "12:00", true),
						textPart("And tomorrow?"),
					}},
					{Role: adapter.RoleAssistant},
					{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("")}},
					{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Thanks")}},
				},
				Tools: []adapter.Tool{
					{Name: "get_weather", Description: "Weather", Parameters: weatherSchema},
					{Name: "get_time"},
				},
				ToolChoice:  &adapter.ToolChoice{Mode: "tool", Name: "get_weather"},
				MaxTokens:   256,
				Temperature: ptr(0.2),
				TopP:        ptr(0.8),
				Stop:        []string{"END"},
				Stream:      true,
			},
			want: `{
				"model": "gpt-4o",
				"messages": [
					{"role":"system","content":"Be brief."},
					{"role":"user","content":[
						{"type":"text","text":"What's the weather?"},
						{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
						{"type":"image_url","image_url":{"url":"https://example.com/cat.jpg"}}]},
					{"role":"assistant","content":"Checking.","tool_calls":[
						{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},
						{"id":"toolu_2","type":"function","function":{"name":"get_time","arguments":"{}"}}]},
					{"role":"tool","tool_call_id":"call_1","content":"Sunny"},
					{"role":"tool","tool_call_id":"toolu_2","content":"12:00"},
					{"role":"user","content":"And tomorrow?"},
					{"role":"user","content":"Thanks"}
				],
				"max_completion_tokens": 256,
				"temperature": 0.2,
				"top_p": 0.8,
				"stop": ["END"],
				"stream": true,
				"stream_options": {"include_usage": true},
				"tools": [
					{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}},
					{"type":"function","function":{"name":"get_time","parameters":{"type":"object","properties":{}}}}
				],
				"tool_choice": {"type":"function","function":{"name":"get_weather"}}
			}`,
		},
		{
			name: "minimal non-streaming",
			req: adapter.Request{
				Messages: []adapter.Message{
					{Role: adapter.RoleAssistant, Parts: []adapter.Part{callPart("c1", "f", `{"a":1}`)}},
					{Role: adapter.RoleUser, Parts: []adapter.Part{resultPart("c1", "", false)}},
				},
				ToolChoice: &adapter.ToolChoice{Mode: "auto"}, // dropped: no tools
			},
			want: `{
				"model": "gpt-4o",
				"messages": [
					{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},
					{"role":"tool","tool_call_id":"c1","content":""}
				],
				"stream": false
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().EncodeRequest(&tt.req, "gpt-4o")
			if err != nil {
				t.Fatalf("EncodeRequest: %v", err)
			}
			assertJSON(t, got, tt.want)
		})
	}
}

func TestEncodeRequestToolChoice(t *testing.T) {
	tests := map[string]string{"auto": `"auto"`, "none": `"none"`, "required": `"required"`}
	for mode, want := range tests {
		req := &adapter.Request{Tools: []adapter.Tool{{Name: "f"}}, ToolChoice: &adapter.ToolChoice{Mode: mode}}
		body, err := New().EncodeRequest(req, "m")
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		if string(out.ToolChoice) != want {
			t.Errorf("mode %s: tool_choice = %s, want %s", mode, out.ToolChoice, want)
		}
	}
}

func TestDecodeResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want adapter.Response
	}{
		{
			name: "text and tool call",
			body: `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-2024-08-06",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Let me check.","refusal":null,
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
				"logprobs":null,"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":50,"completion_tokens":20,"total_tokens":70,"prompt_tokens_details":{"cached_tokens":30}}}`,
			want: adapter.Response{
				ID: "chatcmpl-1", Model: "gpt-4o-2024-08-06",
				Content:    []adapter.Part{textPart("Let me check."), callPart("call_1", "get_weather", `{"city":"Paris"}`)},
				StopReason: adapter.StopToolUse,
				Usage:      adapter.Usage{InputTokens: 50, OutputTokens: 20},
			},
		},
		{
			name: "content parts and length",
			body: `{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}],"reasoning_content":"hmm"},"finish_reason":"length"}]}`,
			want: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{textPart("Hello world")}, StopReason: adapter.StopMaxTokens},
		},
		{
			name: "stop with tool calls means tool use",
			body: `{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":""}}]},"finish_reason":"stop"}]}`,
			want: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{callPart("c1", "f", "{}")}, StopReason: adapter.StopToolUse},
		},
		{
			name: "content filter",
			body: `{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":""},"finish_reason":"content_filter"}]}`,
			want: adapter.Response{ID: "x", Model: "m", StopReason: adapter.StopContentFilter},
		},
		{
			name: "null error field is not an error",
			body: `{"id":"x","model":"m","error":null,"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
			want: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{textPart("ok")}, StopReason: adapter.StopEnd},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().DecodeResponse([]byte(tt.body))
			if err != nil {
				t.Fatalf("DecodeResponse: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("got %+v\nwant %+v", *got, tt.want)
			}
		})
	}
}

func TestDecodeResponseGeneratesToolCallID(t *testing.T) {
	body := `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	got, err := New().DecodeResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 1 || !strings.HasPrefix(got.Content[0].ToolCall.ID, "call_") {
		t.Errorf("expected a generated call ID, got %+v", got.Content)
	}
}

func TestDecodeResponseErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *adapter.Error // nil: any non-*adapter.Error error
	}{
		{"error object with HTTP code", `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":429}}`, &adapter.Error{Status: 429, Message: "Rate limit reached"}},
		{"error object with string code", `{"error":{"message":"Server failed","type":"server_error","code":"server_error"}}`, &adapter.Error{Status: 502, Message: "Server failed"}},
		{"error string", `{"error":"quota exceeded"}`, &adapter.Error{Status: 502, Message: "quota exceeded"}},
		{"no choices", `{"id":"x","choices":[]}`, nil},
		{"invalid JSON", `<html>`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New().DecodeResponse([]byte(tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			var ae *adapter.Error
			isAdapterErr := errors.As(err, &ae)
			if tt.want == nil {
				if isAdapterErr {
					t.Errorf("unexpected *adapter.Error %v", ae)
				}
				return
			}
			if !isAdapterErr || *ae != *tt.want {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

const openAIToolStream = `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"Let me "},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"check."},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"Paris, FR\"}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"tz\":"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"CET\"}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":30,"total_tokens":50,"prompt_tokens_details":{"cached_tokens":0}}}

data: [DONE]

`

func TestStreamDecoder(t *testing.T) {
	got, sawEOF := decodeStream(t, New().NewStreamDecoder(), openAIToolStream)
	if !sawEOF {
		t.Error("[DONE] did not end the stream")
	}
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "chatcmpl-abc", Model: "gpt-4o-2024-08-06"},
		{Type: adapter.EventText, Text: "Let me "},
		{Type: adapter.EventText, Text: "check."},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "call_1", ToolName: "get_weather"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `{"loc`},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `ation": "Paris, FR"}`},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "call_2", ToolName: "get_time"},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: `{"tz":`},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: `"CET"}`},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 20, OutputTokens: 30}},
	})
}

func TestStreamDecoderToolCallsWithoutIndex(t *testing.T) {
	// Some compatible servers omit "index" or reuse it for every call.
	stream := `data: {"id":"x","model":"m","choices":[{"delta":{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"a","arguments":"{}"}}]}}]}

data: {"id":"x","model":"m","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"b","arguments":"{\"x\":"}}]}}]}

data: {"id":"x","model":"m","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}

data: {"id":"x","model":"m","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}

data: [DONE]
`
	got, _ := decodeStream(t, New().NewStreamDecoder(), stream)
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "x", Model: "m"},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "call_a", ToolName: "a"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: "{}"},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "call_b", ToolName: "b"},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: `{"x":`},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: "1}"},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 3, OutputTokens: 4}},
	})
}

func TestStreamDecoderGeneratesToolID(t *testing.T) {
	d := New().NewStreamDecoder()
	got, err := d.Decode(sse.Event{Data: `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"f","arguments":""}}]}}]}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Type != adapter.EventToolStart || !strings.HasPrefix(got[1].ToolID, "call_") {
		t.Errorf("unexpected events: %s", dumpEvents(got))
	}
}

func TestStreamDecoderErrorChunk(t *testing.T) {
	d := New().NewStreamDecoder()
	if _, err := d.Decode(sse.Event{Data: `{"id":"x","choices":[{"delta":{"content":"Hi"}}]}`}); err != nil {
		t.Fatal(err)
	}
	_, err := d.Decode(sse.Event{Data: `{"error":{"message":"The server had an error","type":"server_error","param":null,"code":null}}`})
	var ae *adapter.Error
	if !errors.As(err, &ae) || ae.Status != 502 || ae.Message != "The server had an error" {
		t.Errorf("got %v, want *adapter.Error 502", err)
	}
	if _, err := d.Decode(sse.Event{Data: "[DONE]"}); err != io.EOF {
		t.Errorf("[DONE] returned %v, want io.EOF", err)
	}
	if _, err := d.Decode(sse.Event{Data: "not json"}); err == nil {
		t.Error("invalid chunk should fail")
	}
}

func TestErrorMessage(t *testing.T) {
	long := strings.Repeat("x", 600)
	tests := []struct {
		body, want string
	}{
		{`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`, "Incorrect API key provided"},
		{`{"error":"Too many requests"}`, "Too many requests"},
		{`{"message":"Model not found","object":"error"}`, "Model not found"},
		{`{"error":{"code":"x"}}`, `{"error":{"code":"x"}}`},
		{"  <html>Bad Gateway</html>\n", "<html>Bad Gateway</html>"},
		{long, strings.Repeat("x", 500) + "..."},
		{"", ""},
	}
	for _, tt := range tests {
		if got := New().ErrorMessage([]byte(tt.body)); got != tt.want {
			t.Errorf("ErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}
