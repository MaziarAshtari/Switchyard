package anthropic

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
		"model": "claude-best",
		"max_tokens": 1024,
		"system": [
			{"type": "text", "text": "You are Claude Code."},
			{"type": "text", "text": "Be careful.", "cache_control": {"type": "ephemeral"}}
		],
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "user", "content": [
				{"type": "text", "text": "Look:"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR"}},
				{"type": "image", "source": {"type": "url", "url": "https://example.com/a.jpg"}},
				{"type": "document", "source": {"type": "base64", "media_type": "application/pdf", "data": "JVBER"}}
			]},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "Let me think", "signature": "sig"},
				{"type": "redacted_thinking", "data": "xxx"},
				{"type": "text", "text": "Checking."},
				{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "Paris", "days": [1, 2]}},
				{"type": "tool_use", "id": "toolu_2", "name": "get_time", "input": {}},
				{"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search", "input": {"query": "x"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "Sunny"},
				{"type": "tool_result", "tool_use_id": "toolu_2", "is_error": true, "content": [
					{"type": "text", "text": "12:00"},
					{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "x"}},
					{"type": "text", "text": "UTC"}
				]},
				{"type": "search_result", "source": "https://example.com", "title": "t", "content": [{"type": "text", "text": "x"}]},
				{"type": "text", "text": "Continue"}
			]}
		],
		"tools": [
			{"name": "get_weather", "description": "Weather", "input_schema": {"type":"object","properties":{"city":{"type":"string"}}}},
			{"type": "web_search_20250305", "name": "web_search", "max_uses": 5}
		],
		"tool_choice": {"type": "any", "disable_parallel_tool_use": true},
		"temperature": 0.7,
		"top_p": 0.9,
		"top_k": 40,
		"stop_sequences": ["STOP"],
		"stream": true,
		"thinking": {"type": "enabled", "budget_tokens": 2000},
		"metadata": {"user_id": "u"}
	}`
	got, err := New().DecodeRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	want := &adapter.Request{
		Model:  "claude-best",
		System: "You are Claude Code.\n\nBe careful.",
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Hello")}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				textPart("Look:"),
				imagePart(adapter.Image{MediaType: "image/png", Data: "iVBOR"}),
				imagePart(adapter.Image{URL: "https://example.com/a.jpg"}),
				textPart("[document omitted]"),
			}},
			{Role: adapter.RoleAssistant, Parts: []adapter.Part{
				textPart("Checking."),
				callPart("toolu_1", "get_weather", `{"city":"Paris","days":[1,2]}`),
				callPart("toolu_2", "get_time", "{}"),
			}},
			{Role: adapter.RoleUser, Parts: []adapter.Part{
				resultPart("toolu_1", "Sunny", false),
				resultPart("toolu_2", "12:00\n[image omitted]\nUTC", true),
				textPart("Continue"),
			}},
		},
		Tools: []adapter.Tool{
			{Name: "get_weather", Description: "Weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)},
		},
		ToolChoice:  &adapter.ToolChoice{Mode: "required"},
		MaxTokens:   1024,
		Temperature: ptr(0.7),
		TopP:        ptr(0.9),
		Stop:        []string{"STOP"},
		Stream:      true,
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("request mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestDecodeRequestVariants(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		check func(*adapter.Request) bool
	}{
		{"system string", `{"system":"Be brief.","messages":[]}`, func(r *adapter.Request) bool { return r.System == "Be brief." }},
		{"no system", `{"messages":[]}`, func(r *adapter.Request) bool { return r.System == "" && r.ToolChoice == nil }},
		{"tool_choice auto", `{"messages":[],"tool_choice":{"type":"auto"}}`, func(r *adapter.Request) bool {
			return reflect.DeepEqual(r.ToolChoice, &adapter.ToolChoice{Mode: "auto"})
		}},
		{"tool_choice none", `{"messages":[],"tool_choice":{"type":"none"}}`, func(r *adapter.Request) bool {
			return reflect.DeepEqual(r.ToolChoice, &adapter.ToolChoice{Mode: "none"})
		}},
		{"tool_choice tool", `{"messages":[],"tool_choice":{"type":"tool","name":"f"}}`, func(r *adapter.Request) bool {
			return reflect.DeepEqual(r.ToolChoice, &adapter.ToolChoice{Mode: "tool", Name: "f"})
		}},
		{"tool_use without input", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f"}]}]}`, func(r *adapter.Request) bool {
			return r.Messages[0].Parts[0].ToolCall.Arguments == "{}"
		}},
		{"tool_result without content", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}]}`, func(r *adapter.Request) bool {
			return r.Messages[0].Parts[0].ToolResult.Content == ""
		}},
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

func TestDecodeRequestErrors(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":          `{"messages":`,
		"messages not an array": `{"messages":"hi"}`,
		"messages missing":      `{"model":"x","max_tokens":10}`,
		"system of wrong type":  `{"messages":[],"system":42}`,
		"content of wrong type": `{"messages":[{"role":"user","content":42}]}`,
		"bad tool_result":       `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":42}]}]}`,
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
	tests := []struct {
		name string
		resp adapter.Response
		want string
	}{
		{
			name: "text and tool calls",
			resp: adapter.Response{
				ID: "chatcmpl-1", Model: "gpt-4o",
				Content: []adapter.Part{
					textPart("Checking."),
					callPart("call_1", "get_weather", `{"city":"Paris"}`),
					callPart("call_2", "broken", `{"city":`),
				},
				StopReason: adapter.StopToolUse,
				Usage:      adapter.Usage{InputTokens: 10, OutputTokens: 5},
			},
			want: `{"id":"chatcmpl-1","type":"message","role":"assistant","model":"gpt-4o","content":[
				{"type":"text","text":"Checking."},
				{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"Paris"}},
				{"type":"tool_use","id":"call_2","name":"broken","input":{}}],
				"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`,
		},
		{
			name: "empty with max tokens",
			resp: adapter.Response{ID: "x", Model: "m", StopReason: adapter.StopMaxTokens},
			want: `{"id":"x","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"max_tokens","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`,
		},
		{
			name: "content filter",
			resp: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{textPart("")}, StopReason: adapter.StopContentFilter},
			want: `{"id":"x","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"refusal","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`,
		},
		{
			name: "end",
			resp: adapter.Response{ID: "x", Model: "m", Content: []adapter.Part{textPart("Hi")}, StopReason: adapter.StopEnd},
			want: `{"id":"x","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"Hi"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`,
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

func TestEncodeResponseDefaults(t *testing.T) {
	got, err := New().EncodeResponse(&adapter.Response{Content: []adapter.Part{callPart("c1", "f", "{}")}})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID         string `json:"id"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.ID, "msg_") || out.StopReason != "tool_use" {
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
		{402, "billing_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{413, "request_too_large"},
		{422, "invalid_request_error"},
		{429, "rate_limit_error"},
		{500, "api_error"},
		{503, "api_error"},
		{529, "overloaded_error"},
	}
	for _, tt := range tests {
		got := New().EncodeError(tt.status, `bad "thing"`)
		assertJSON(t, got, `{"type":"error","error":{"type":"`+tt.typ+`","message":"bad \"thing\""}}`)
	}
}

func encodeAll(e adapter.StreamEncoder, events []adapter.Event) []sse.Event {
	var out []sse.Event
	for _, ev := range events {
		out = append(out, e.Encode(ev)...)
	}
	return out
}

func TestStreamEncoder(t *testing.T) {
	e := New().NewStreamEncoder(&adapter.Request{Model: "claude-best"})
	got := encodeAll(e, []adapter.Event{
		{Type: adapter.EventStart, ID: "chatcmpl-1", Model: "gpt-4o"},
		{Type: adapter.EventText, Text: "Hel"},
		{Type: adapter.EventText, Text: "lo"},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "call_1", ToolName: "get_weather"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `{"city":`},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `"Paris"}`},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "call_2", ToolName: "get_time"},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: `{}`},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 12, OutputTokens: 34}},
	})
	got = append(got, e.Finish()...)
	assertEvents(t, got, []sse.Event{
		ev(`{"type":"message_start","message":{"id":"chatcmpl-1","type":"message","role":"assistant","model":"gpt-4o","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`),
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`),
		ev(`{"type":"content_block_stop","index":0}`),
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`),
		ev(`{"type":"content_block_stop","index":1}`),
		ev(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_2","name":"get_time","input":{}}}`),
		ev(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`),
		ev(`{"type":"content_block_stop","index":2}`),
		ev(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":12,"output_tokens":34}}`),
		ev(`{"type":"message_stop"}`),
	})
}

func TestStreamEncoderTextAfterTool(t *testing.T) {
	e := New().NewStreamEncoder(&adapter.Request{Model: "claude-best"})
	got := encodeAll(e, []adapter.Event{
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 7}},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "c1", ToolName: "f"},
		{Type: adapter.EventText, Text: "Done."},
		{Type: adapter.EventStop, StopReason: adapter.StopEnd},
	})
	got = append(got, e.Finish()...)
	s := collectAnthropic(t, got)
	if s.Model != "claude-best" || !strings.HasPrefix(s.ID, "msg_") || s.StartInput != 7 {
		t.Errorf("message_start: id %q model %q input %d", s.ID, s.Model, s.StartInput)
	}
	want := []streamedBlock{{Type: "tool_use", ID: "c1", Name: "f"}, {Type: "text", Text: "Done."}}
	if !reflect.DeepEqual(s.Blocks, want) || s.StopReason != "end_turn" {
		t.Errorf("blocks %+v, stop %q", s.Blocks, s.StopReason)
	}
}

func TestStreamEncoderFinishWithoutStop(t *testing.T) {
	tests := []struct {
		name   string
		events []adapter.Event
		stop   string
	}{
		{"nothing encoded", nil, "end_turn"},
		{"text", []adapter.Event{{Type: adapter.EventText, Text: "Hi"}}, "end_turn"},
		{"tool call", []adapter.Event{{Type: adapter.EventToolStart, ToolID: "c1", ToolName: "f"}}, "tool_use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New().NewStreamEncoder(&adapter.Request{Model: "claude-best"})
			got := append(encodeAll(e, tt.events), e.Finish()...)
			if s := collectAnthropic(t, got); s.StopReason != tt.stop {
				t.Errorf("stop_reason = %q, want %q", s.StopReason, tt.stop)
			}
		})
	}
}

func TestStreamEncoderError(t *testing.T) {
	errEvent := ev(`{"type":"error","error":{"type":"api_error","message":"upstream broke"}}`)

	e := New().NewStreamEncoder(&adapter.Request{Model: "claude-best"})
	if got := e.Encode(adapter.Event{Type: adapter.EventText, Text: "Hi"}); len(got) != 3 {
		t.Fatalf("expected message_start, block start and delta, got:\n%s", dumpSSE(got))
	}
	assertEvents(t, e.Error("upstream broke"), []sse.Event{errEvent})

	// Nothing sent yet: the error still follows a message_start.
	got := New().NewStreamEncoder(&adapter.Request{Model: "claude-best"}).Error("upstream broke")
	if len(got) != 2 || got[0].Name != "message_start" {
		t.Fatalf("unexpected events:\n%s", dumpSSE(got))
	}
	assertEvents(t, got[1:], []sse.Event{errEvent})
}
