package anthropic

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
	if a.Format() != adapter.Anthropic {
		t.Errorf("Format = %q", a.Format())
	}
	if got := a.ChatURL("https://api.anthropic.com", "claude-sonnet-4-5", true); got != "https://api.anthropic.com/v1/messages" {
		t.Errorf("ChatURL = %q", got)
	}
	if got := a.ModelsURL("https://api.anthropic.com"); got != "https://api.anthropic.com/v1/models" {
		t.Errorf("ModelsURL = %q", got)
	}

	h := http.Header{}
	a.SetAuth(h, "sk-ant-test")
	if h.Get("x-api-key") != "sk-ant-test" || h.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("headers = %v", h)
	}
	h = http.Header{}
	h.Set("anthropic-version", "2024-10-22")
	a.SetAuth(h, "k")
	if got := h.Get("anthropic-version"); got != "2024-10-22" {
		t.Errorf("client anthropic-version overwritten: %q", got)
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
				System: "Be brief.",
				Messages: []adapter.Message{
					{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Hi")}},
					{Role: adapter.RoleUser, Parts: []adapter.Part{
						textPart(""),
						imagePart(adapter.Image{MediaType: "image/png", Data: "AAAA"}),
						imagePart(adapter.Image{URL: "https://example.com/cat.jpg"}),
					}},
					{Role: adapter.RoleAssistant, Parts: []adapter.Part{
						textPart("Checking."),
						callPart("call.1:x", "get_weather", `{"city":"Paris"}`),
						callPart("", "get_time", "not json"),
					}},
					{Role: adapter.RoleUser, Parts: []adapter.Part{
						textPart("Also this"),
						resultPart("call.1:x", "Sunny", false),
						resultPart("", "12:00", true),
					}},
					{Role: adapter.RoleAssistant},
					{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Thanks"), resultPart("call_9", "", false)}},
				},
				Tools: []adapter.Tool{
					{Name: "get_weather", Description: "Weather", Parameters: weatherSchema},
					{Name: "get_time"},
				},
				ToolChoice:  &adapter.ToolChoice{Mode: "required"},
				Temperature: ptr(1.5),
				TopP:        ptr(0.9),
				Stop:        []string{"END"},
				Stream:      true,
			},
			want: `{
				"model": "claude-sonnet-4-5",
				"max_tokens": 8192,
				"system": "Be brief.",
				"messages": [
					{"role":"user","content":[
						{"type":"text","text":"Hi"},
						{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},
						{"type":"image","source":{"type":"url","url":"https://example.com/cat.jpg"}}]},
					{"role":"assistant","content":[
						{"type":"text","text":"Checking."},
						{"type":"tool_use","id":"call_1_x","name":"get_weather","input":{"city":"Paris"}},
						{"type":"tool_use","id":"toolu_0","name":"get_time","input":{}}]},
					{"role":"user","content":[
						{"type":"tool_result","tool_use_id":"call_1_x","content":"Sunny"},
						{"type":"tool_result","tool_use_id":"toolu_0","content":"12:00","is_error":true},
						{"type":"tool_result","tool_use_id":"call_9"},
						{"type":"text","text":"Also this"},
						{"type":"text","text":"Thanks"}]}
				],
				"tools": [
					{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}},
					{"name":"get_time","input_schema":{"type":"object","properties":{}}}
				],
				"tool_choice": {"type":"any"},
				"temperature": 1,
				"stop_sequences": ["END"],
				"stream": true
			}`,
		},
		{
			name: "explicit max tokens, top_p only, tool choice without tools",
			req: adapter.Request{
				Messages:   []adapter.Message{{Role: adapter.RoleUser, Parts: []adapter.Part{textPart("Hi")}}},
				MaxTokens:  100,
				TopP:       ptr(0.5),
				ToolChoice: &adapter.ToolChoice{Mode: "auto"},
			},
			want: `{"model":"claude-sonnet-4-5","max_tokens":100,"messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]}],"top_p":0.5,"stream":false}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().EncodeRequest(&tt.req, "claude-sonnet-4-5")
			if err != nil {
				t.Fatalf("EncodeRequest: %v", err)
			}
			assertJSON(t, got, tt.want)
		})
	}
}

func TestEncodeRequestToolChoice(t *testing.T) {
	tests := []struct {
		choice adapter.ToolChoice
		want   string
	}{
		{adapter.ToolChoice{Mode: "auto"}, `{"type":"auto"}`},
		{adapter.ToolChoice{Mode: "none"}, `{"type":"none"}`},
		{adapter.ToolChoice{Mode: "required"}, `{"type":"any"}`},
		{adapter.ToolChoice{Mode: "tool", Name: "f"}, `{"type":"tool","name":"f"}`},
	}
	for _, tt := range tests {
		req := &adapter.Request{Tools: []adapter.Tool{{Name: "f"}}, ToolChoice: &tt.choice}
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
		assertJSON(t, out.ToolChoice, tt.want)
	}
}

func TestSanitizeID(t *testing.T) {
	var ids toolIDs
	tests := []struct{ got, want string }{
		{ids.call("toolu_01A-b"), "toolu_01A-b"},
		{ids.call("call.1:x/y z"), "call_1_x_y_z"},
		{ids.call("fc-é"), "fc-_"},
		{ids.call(""), "toolu_0"},
		{ids.call(""), "toolu_1"},
		{ids.result(""), "toolu_0"},
		{ids.result(""), "toolu_1"},
	}
	for i, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("case %d: got %q, want %q", i, tt.got, tt.want)
		}
	}
}

func TestDecodeResponse(t *testing.T) {
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929",
		"content":[
			{"type":"thinking","thinking":"hmm","signature":"s"},
			{"type":"text","text":"Checking.","citations":null},
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"q"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://x"}]},
			{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city": "Paris"}},
			{"type":"tool_use","id":"toolu_2","name":"get_time","input":null}
		],
		"stop_reason":"tool_use","stop_sequence":null,
		"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}`
	got, err := New().DecodeResponse([]byte(body))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	want := adapter.Response{
		ID: "msg_1", Model: "claude-sonnet-4-5-20250929",
		Content: []adapter.Part{
			textPart("Checking."),
			callPart("toolu_1", "get_weather", `{"city":"Paris"}`),
			callPart("toolu_2", "get_time", "{}"),
		},
		StopReason: adapter.StopToolUse,
		Usage:      adapter.Usage{InputTokens: 60, OutputTokens: 40},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %+v\nwant %+v", *got, want)
	}
}

func TestDecodeResponseStopReasons(t *testing.T) {
	tests := map[string]adapter.StopReason{
		"end_turn":                      adapter.StopEnd,
		"stop_sequence":                 adapter.StopEnd,
		"pause_turn":                    adapter.StopEnd,
		"something_new":                 adapter.StopEnd,
		"max_tokens":                    adapter.StopMaxTokens,
		"model_context_window_exceeded": adapter.StopMaxTokens,
		"tool_use":                      adapter.StopToolUse,
		"refusal":                       adapter.StopContentFilter,
	}
	for reason, want := range tests {
		body := `{"type":"message","content":[{"type":"text","text":"x"}],"stop_reason":"` + reason + `","usage":{"input_tokens":1,"output_tokens":1}}`
		got, err := New().DecodeResponse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s: got %q, want %q", reason, got.StopReason, want)
		}
	}
}

func TestDecodeResponseErrors(t *testing.T) {
	tests := []struct {
		body string
		want adapter.Error
	}{
		{`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, adapter.Error{Status: 529, Message: "Overloaded"}},
		{`{"type":"error","error":{"type":"rate_limit_error","message":"Slow down"}}`, adapter.Error{Status: 429, Message: "Slow down"}},
		{`{"type":"error","error":{"type":"api_error","message":"Internal"}}`, adapter.Error{Status: 500, Message: "Internal"}},
		{`{"type":"error","error":{"type":"mystery_error","message":"?"}}`, adapter.Error{Status: 502, Message: "?"}},
		{`{"type":"error"}`, adapter.Error{Status: 502, Message: `{"type":"error"}`}},
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

// anthropicToolStream is a realistic Anthropic stream with thinking, text and
// two tool calls.
const anthropicToolStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_01ABC","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":1}}}

event: ping
data: {"type": "ping"}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"The user wants the weather."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAh=="}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"I'll check "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"the weather."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"location\": \"San"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":" Francisco, CA\", \"unit\": \"celsius\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_01B","name":"get_time","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":89}}

event: message_stop
data: {"type":"message_stop"}

`

func TestStreamDecoder(t *testing.T) {
	got, sawEOF := decodeStream(t, New().NewStreamDecoder(), anthropicToolStream)
	if !sawEOF {
		t.Error("message_stop did not end the stream")
	}
	assertCanonicalEvents(t, got, []adapter.Event{
		{Type: adapter.EventStart, ID: "msg_01ABC", Model: "claude-sonnet-4-5-20250929"},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 112, OutputTokens: 1}},
		{Type: adapter.EventText, Text: "I'll check "},
		{Type: adapter.EventText, Text: "the weather."},
		{Type: adapter.EventToolStart, ToolIndex: 0, ToolID: "toolu_01A", ToolName: "get_weather"},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: `{"location": "San`},
		{Type: adapter.EventToolArgs, ToolIndex: 0, ArgsDelta: ` Francisco, CA", "unit": "celsius"}`},
		{Type: adapter.EventToolStart, ToolIndex: 1, ToolID: "toolu_01B", ToolName: "get_time"},
		{Type: adapter.EventToolArgs, ToolIndex: 1, ArgsDelta: "{}"},
		{Type: adapter.EventStop, StopReason: adapter.StopToolUse},
		{Type: adapter.EventUsage, Usage: &adapter.Usage{InputTokens: 112, OutputTokens: 89}},
	})
}

func TestStreamDecoderMessageDeltaUsage(t *testing.T) {
	start := sse.Event{Name: "message_start", Data: `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":10,"cache_read_input_tokens":100,"output_tokens":1}}}`}
	tests := []struct {
		name  string
		delta string
		want  adapter.Usage
	}{
		{"output only", `{"output_tokens":50}`, adapter.Usage{InputTokens: 110, OutputTokens: 50}},
		{"input without cache fields", `{"input_tokens":12,"output_tokens":50}`, adapter.Usage{InputTokens: 112, OutputTokens: 50}},
		{"all fields", `{"input_tokens":12,"cache_creation_input_tokens":3,"cache_read_input_tokens":0,"output_tokens":50}`, adapter.Usage{InputTokens: 15, OutputTokens: 50}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New().NewStreamDecoder()
			if _, err := d.Decode(start); err != nil {
				t.Fatal(err)
			}
			got, err := d.Decode(sse.Event{Name: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":` + tt.delta + `}`})
			if err != nil {
				t.Fatal(err)
			}
			assertCanonicalEvents(t, got, []adapter.Event{
				{Type: adapter.EventStop, StopReason: adapter.StopEnd},
				{Type: adapter.EventUsage, Usage: &tt.want},
			})
		})
	}
}

func TestStreamDecoderErrorEvent(t *testing.T) {
	stream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

`
	d := New().NewStreamDecoder()
	var err error
	for _, e := range readSSE(t, stream) {
		if _, err = d.Decode(e); err != nil {
			break
		}
	}
	var ae *adapter.Error
	if !errors.As(err, &ae) || *ae != (adapter.Error{Status: 529, Message: "Overloaded"}) {
		t.Errorf("got %v, want *adapter.Error 529 Overloaded", err)
	}
}

func TestStreamDecoderEdgeCases(t *testing.T) {
	d := New().NewStreamDecoder()
	for _, data := range []string{"", `{"type":"ping"}`, `{"type":"content_block_stop","index":0}`, `{"type":"future_event"}`} {
		if got, err := d.Decode(sse.Event{Data: data}); err != nil || len(got) != 0 {
			t.Errorf("Decode(%q) = %v, %v; want nothing", data, got, err)
		}
	}
	// The type may be missing from data if the event name carries it.
	if got, err := d.Decode(sse.Event{Name: "message_stop", Data: `{}`}); err != io.EOF || len(got) != 0 {
		t.Errorf("message_stop by name: %v, %v", got, err)
	}
	if _, err := d.Decode(sse.Event{Data: "not json"}); err == nil {
		t.Error("invalid data should fail")
	}
	got, err := New().NewStreamDecoder().Decode(sse.Event{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"f","input":{}}}`})
	if err != nil || len(got) != 1 || !strings.HasPrefix(got[0].ToolID, "toolu_") {
		t.Errorf("tool_use without id: %s, %v", dumpEvents(got), err)
	}
}

func TestErrorMessage(t *testing.T) {
	long := strings.Repeat("y", 600)
	tests := []struct{ body, want string }{
		{`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: Field required"}}`, "max_tokens: Field required"},
		{`{"error":{"message":"bad key"}}`, "bad key"},
		{`{"error":"plain"}`, `{"error":"plain"}`},
		{"upstream connect error\n", "upstream connect error"},
		{long, strings.Repeat("y", 500) + "..."},
		{"", ""},
	}
	for _, tt := range tests {
		if got := New().ErrorMessage([]byte(tt.body)); got != tt.want {
			t.Errorf("ErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}
