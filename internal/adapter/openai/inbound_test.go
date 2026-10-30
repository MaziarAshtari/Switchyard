package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

func TestDecodeRequestConversation(t *testing.T) {
	body := `{
		"model": "gpt-best",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "developer", "content": [{"type": "text", "text": "Be brief."}, {"type": "text", "text": "Use metric units."}]},
			{"role": "user", "content": "Hi"},
			{"role": "user", "content": [
				{"type": "text", "text": "What is in these images?"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}},
				{"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg", "detail": "high"}},
				{"type": "input_audio", "input_audio": {"data": "AAA", "format": "wav"}}
			]},
			{"role": "assistant", "content": null, "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}},
				{"id": "call_2", "type": "function", "function": {"name": "get_time", "arguments": ""}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "Sunny"},
			{"role": "tool", "tool_call_id": "call_2", "content": [{"type": "text", "text": "12:00"}, {"type": "text", "text": "UTC"}]},
			{"role": "assistant", "content": [{"type": "text", "text": "It is sunny."}, {"type": "refusal", "refusal": "no"}]},
			{"role": "user", "content": "Thanks"}
		],
		"tools": [
			{"type": "function", "function": {"name": "get_weather", "description": "Weather", "parameters": {"type":"object","properties":{"city":{"type":"string"}}}}},
			{"type": "function", "function": {"name": "get_time"}}
		],
		"tool_choice": "required",
		"max_completion_tokens": 100,
		"max_tokens": 50,
		"temperature": 0.5,
		"top_p": 0.9,
		"stop": ["END", ""],
		"stream": true,
		"stream_options": {"include_usage": true},
		"response_format": {"type": "json_object"},
		"n": 1
	}`
	got, err := New().DecodeRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	want := &adapter.Request{
		Model:  "gpt-best",
		System: "You are helpful.\n\nBe brief.\n\nUse metric units.",
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Hi")}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				textPart("What is in these images?"),
				imagePart(adapter.Image{MediaType: "image/png", Data: "iVBORw0KGgo="}),
				imagePart(adapter.Image{URL: "https://example.com/cat.jpg"}),
			}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{
				callPart("call_1", "get_weather", `{"city":"Paris"}`),
				callPart("call_2", "get_time", "{}"),
			}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				resultPart("call_1", "Sunny", false),
				resultPart("call_2", "12:00\nUTC", false),
			}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{textPart("It is sunny.")}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Thanks")}},
		},
		Tools: []adapter.Tool{
			{Name: "get_weather", Description: "Weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)},
			{Name: "get_time"},
		},
		ToolChoice:   &adapter.ToolChoice{Mode: "required"},
		MaxTokens:    100,
		Temperature:  ptr(0.5),
		TopP:         ptr(0.9),
		Stop:         []string{"END"},
		Stream:       true,
		IncludeUsage: true,
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("request mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestDecodeRequestFields(t *testing.T) {
	const msgs = `"messages":[{"role":"user","content":"Hi"}]`
	tests := []struct {
		name  string
		body  string
		check func(*adapter.Request) bool
	}{
		{"max_tokens fallback", `{` + msgs + `,"max_tokens":50}`, func(r *adapter.Request) bool { return r.MaxTokens == 50 }},
		{"max_completion_tokens preferred", `{` + msgs + `,"max_tokens":50,"max_completion_tokens":70}`, func(r *adapter.Request) bool { return r.MaxTokens == 70 }},
		{"no max tokens", `{` + msgs + `}`, func(r *adapter.Request) bool { return r.MaxTokens == 0 && r.Temperature == nil && r.TopP == nil }},
		{"stop string", `{` + msgs + `,"stop":"END"}`, func(r *adapter.Request) bool { return reflect.DeepEqual(r.Stop, []string{"END"}) }},
		{"stop array", `{` + msgs + `,"stop":["a","b"]}`, func(r *adapter.Request) bool { return reflect.DeepEqual(r.Stop, []string{"a", "b"}) }},
		{"stop null", `{` + msgs + `,"stop":null}`, func(r *adapter.Request) bool { return r.Stop == nil }},
		{"include_usage off", `{` + msgs + `,"stream":true,"stream_options":{"include_usage":false}}`, func(r *adapter.Request) bool { return r.Stream && !r.IncludeUsage }},
		{"no stream", `{` + msgs + `}`, func(r *adapter.Request) bool { return !r.Stream && !r.IncludeUsage }},
		{"empty messages array", `{"messages":[]}`, func(r *adapter.Request) bool { return len(r.Messages) == 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := New().DecodeRequest([]byte(tt.body))
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			if !tt.check(r) {
				t.Errorf("unexpected request: %+v", r)
			}
		})
	}
}

func TestDecodeRequestToolChoice(t *testing.T) {
	tests := []struct {
		choice string
		want   *adapter.ToolChoice
	}{
		{`"auto"`, &adapter.ToolChoice{Mode: "auto"}},
		{`"none"`, &adapter.ToolChoice{Mode: "none"}},
		{`"required"`, &adapter.ToolChoice{Mode: "required"}},
		{`{"type":"function","function":{"name":"get_weather"}}`, &adapter.ToolChoice{Mode: "tool", Name: "get_weather"}},
		{`null`, nil},
		{`"bogus"`, nil},
		{`{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[]}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.choice, func(t *testing.T) {
			body := `{"messages":[{"role":"user","content":"Hi"}],"tool_choice":` + tt.choice + `}`
			r, err := New().DecodeRequest([]byte(body))
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			if !reflect.DeepEqual(r.ToolChoice, tt.want) {
				t.Errorf("ToolChoice = %+v, want %+v", r.ToolChoice, tt.want)
			}
		})
	}
}

func TestDecodeRequestLegacyFunctionMessages(t *testing.T) {
	body := `{"messages":[
		{"role":"user","content":"Weather?"},
		{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},
		{"role":"function","name":"get_weather","content":"Sunny"}
	]}`
	r, err := New().DecodeRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(r.Messages) != 3 {
		t.Fatalf("got %d messages", len(r.Messages))
	}
	call := r.Messages[1].Parts[0].ToolCall
	result := r.Messages[2].Parts[0].ToolResult
	if call == nil || result == nil {
		t.Fatalf("unexpected parts: %+v", r.Messages)
	}
	if !strings.HasPrefix(call.ID, "call_") || result.CallID != call.ID {
		t.Errorf("call ID %q and result CallID %q should match", call.ID, result.CallID)
	}
	if call.Name != "get_weather" || call.Arguments != `{"city":"Paris"}` || result.Name != "get_weather" || result.Content != "Sunny" {
		t.Errorf("unexpected call %+v / result %+v", call, result)
	}
}

func TestDecodeRequestErrors(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":           `{"messages":`,
		"messages not an array":  `{"messages":{"role":"user"}}`,
		"messages missing":       `{"model":"x"}`,
		"messages null":          `{"messages":null}`,
		"content of wrong type":  `{"messages":[{"role":"user","content":42}]}`,
		"field of wrong type":    `{"messages":[],"max_tokens":"many"}`,
		"tool call of bad shape": `{"messages":[{"role":"assistant","tool_calls":[{"function":{"arguments":{}}}]}]}`,
		"stop of wrong type":     `{"messages":[],"stop":5}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New().DecodeRequest([]byte(body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if msg := err.Error(); strings.Contains(msg, "Go ") || strings.Contains(msg, "json:") {
				t.Errorf("error message exposes decoder internals: %q", msg)
			}
		})
	}
}

func TestEncodeResponse(t *testing.T) {
	fixNow(t)
	tests := []struct {
		name string
		resp adapter.Response
		want string
	}{
		{
			name: "text and tool calls",
			resp: adapter.Response{
				ID: "msg_1", Model: "claude-x",
				Content: []adapter.Part{
					textPart("Let me "), textPart("check."),
					callPart("toolu_1", "get_weather", `{"city":"Paris"}`),
					callPart("toolu_2", "get_time", ""),
				},
				StopReason: adapter.StopToolUse,
				Usage:      adapter.Usage{InputTokens: 10, OutputTokens: 5},
			},
			want: `{"id":"msg_1","object":"chat.completion","created":1700000000,"model":"claude-x",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Let me check.","tool_calls":[
					{"id":"toolu_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},
					{"id":"toolu_2","type":"function","function":{"name":"get_time","arguments":"{}"}}]},
				"logprobs":null,"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		},
		{
			name: "tool calls only",
			resp: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{callPart("c1", "f", "{}")}, StopReason: adapter.StopToolUse},
			want: `{"id":"x","object":"chat.completion","created":1700000000,"model":"m",
				"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
					{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
				"logprobs":null,"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		},
		{
			name: "empty",
			resp: adapter.Response{ID: "x", Model: "m", StopReason: adapter.StopMaxTokens},
			want: `{"id":"x","object":"chat.completion","created":1700000000,"model":"m",
				"choices":[{"index":0,"message":{"role":"assistant","content":""},"logprobs":null,"finish_reason":"length"}],
				"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		},
		{
			name: "content filter",
			resp: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{textPart("No.")}, StopReason: adapter.StopContentFilter},
			want: `{"id":"x","object":"chat.completion","created":1700000000,"model":"m",
				"choices":[{"index":0,"message":{"role":"assistant","content":"No."},"logprobs":null,"finish_reason":"content_filter"}],
				"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().EncodeResponse(&tt.resp)
			if err != nil {
				t.Fatalf("EncodeResponse: %v", err)
			}
			assertJSON(t, got, tt.want)
		})
	}
}

func TestEncodeResponseGeneratesID(t *testing.T) {
	got, err := New().EncodeResponse(&adapter.Response{StopReason: adapter.StopEnd})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID      string `json:"id"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.ID, "chatcmpl-") || len(out.Choices) != 1 || out.Choices[0].FinishReason != "stop" {
		t.Errorf("unexpected response %s", got)
	}
}

func TestEncodeError(t *testing.T) {
	tests := []struct {
		status int
		typ    string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{413, "invalid_request_error"},
		{429, "rate_limit_error"},
		{500, "api_error"},
		{503, "api_error"},
	}
	for _, tt := range tests {
		got := New().EncodeError(tt.status, `bad "thing"`)
		assertJSON(t, got, `{"error":{"message":"bad \"thing\"","type":"`+tt.typ+`","param":null,"code":null}}`)
	}
}

// chunk builds an expected stream chunk with the test encoder's id and model.
func chunk(choice string) sse.Event {
	return sse.Event{Data: `{"id":"msg_1","object":"chat.completion.chunk","created":1700000000,"model":"claude-x","choices":[` + choice + `]}`}
}

func TestStreamEncoder(t *testing.T) {
	fixNow(t)
	events := []adapter.Event{
		{Type: adapter.EventStart, ID: "msg_1", Model: "claude-x"},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 10, OutputTokens: 1}},
		{Type: adapter.EventText, Text: "Hello"},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "toolu_1", ToolName: "get_weather"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `{"city":`},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `"Paris"}`},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "toolu_2", ToolName: "get_time"},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 10, OutputTokens: 20}},
	}
	body := []sse.Event{
		chunk(`{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{"tool_calls":[{"index":0,"id":"toolu_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{"tool_calls":[{"index":1,"id":"toolu_2","type":"function","function":{"name":"get_time","arguments":""}}]},"logprobs":null,"finish_reason":null}`),
		chunk(`{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}`),
	}
	usageChunk := sse.Event{Data: `{"id":"msg_1","object":"chat.completion.chunk","created":1700000000,"model":"claude-x","choices":[],
		"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`}
	done := sse.Event{Data: "[DONE]"}

	for _, includeUsage := range []bool{true, false} {
		e := New().NewStreamEncoder(&adapter.Request{Model: "gpt-best", IncludeUsage: includeUsage})
		var got []sse.Event
		for _, ev := range events {
			got = append(got, e.Encode(ev)...)
		}
		got = append(got, e.Finish()...)
		want := append([]sse.Event{}, body...)
		if includeUsage {
			want = append(want, usageChunk)
		}
		assertEvents(t, got, append(want, done))
	}
}

func TestStreamEncoderFinishWithoutStop(t *testing.T) {
	fixNow(t)
	finish := func(reason string) sse.Event {
		return sse.Event{Data: `{"id":"x","object":"chat.completion.chunk","created":1700000000,"model":"gpt-best","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"` + reason + `"}]}`}
	}
	role := sse.Event{Data: `{"id":"x","object":"chat.completion.chunk","created":1700000000,"model":"gpt-best","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}`}
	done := sse.Event{Data: "[DONE]"}

	newEncoder := func() adapter.StreamEncoder {
		e := New().NewStreamEncoder(&adapter.Request{Model: "gpt-best"})
		e.(*streamEncoder).id = "x"
		return e
	}

	t.Run("nothing encoded", func(t *testing.T) {
		assertEvents(t, newEncoder().Finish(), []sse.Event{role, finish("stop"), done})
	})
	t.Run("tool call", func(t *testing.T) {
		e := newEncoder()
		got := e.Encode(adapter.Event{Type: adapter.EventToolStart, ToolID: "c1", ToolName: "f"})
		got = append(got, e.Finish()...)
		if len(got) != 4 {
			t.Fatalf("got %d events:\n%s", len(got), dumpSSE(got))
		}
		assertEvents(t, got[2:], []sse.Event{finish("tool_calls"), done})
	})
	t.Run("stop emitted once", func(t *testing.T) {
		e := newEncoder()
		got := e.Encode(adapter.Event{Type: adapter.EventStop, StopReason: adapter.StopMaxTokens})
		got = append(got, e.Encode(adapter.Event{Type: adapter.EventStop, StopReason: adapter.StopEnd})...)
		got = append(got, e.Finish()...)
		assertEvents(t, got, []sse.Event{role, finish("length"), done})
	})
}

func TestStreamEncoderError(t *testing.T) {
	e := New().NewStreamEncoder(&adapter.Request{Model: "gpt-best"})
	if got := e.Encode(adapter.Event{Type: adapter.EventText, Text: "Hi"}); len(got) != 2 {
		t.Fatalf("expected role and content chunks, got %d", len(got))
	}
	assertEvents(t, e.Error("upstream broke"), []sse.Event{
		{Data: `{"error":{"message":"upstream broke","type":"api_error","param":null,"code":null}}`},
		{Data: "[DONE]"},
	})
}
