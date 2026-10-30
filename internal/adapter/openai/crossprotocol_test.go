package openai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/anthropic"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

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

type streamedTool struct{ ID, Name, Args string }

// openAIStream is what an OpenAI SDK reassembles from a chunk stream.
type openAIStream struct {
	IDs, Models  map[string]bool
	Role         string
	Text         string
	Tools        []streamedTool
	FinishReason string
	Usage        *usage
}

// collectOpenAI reassembles chunks and checks stream invariants: unnamed
// events, one finish_reason, and [DONE] last.
func collectOpenAI(t *testing.T, events []sse.Event) openAIStream {
	t.Helper()
	s := openAIStream{IDs: map[string]bool{}, Models: map[string]bool{}}
	if len(events) == 0 || events[len(events)-1].Data != "[DONE]" {
		t.Fatalf("stream does not end with [DONE]:\n%s", dumpSSE(events))
	}
	for _, ev := range events[:len(events)-1] {
		if ev.Name != "" {
			t.Errorf("unexpected event name %q", ev.Name)
		}
		var c struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Role      string  `json:"role"`
					Content   *string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &c); err != nil {
			t.Fatalf("invalid chunk %s: %v", ev.Data, err)
		}
		if c.Object != "chat.completion.chunk" {
			t.Errorf("object = %q", c.Object)
		}
		s.IDs[c.ID], s.Models[c.Model] = true, true
		if c.Usage != nil {
			s.Usage = c.Usage
		}
		for _, ch := range c.Choices {
			if ch.Delta.Role != "" {
				s.Role = ch.Delta.Role
			}
			if ch.Delta.Content != nil {
				s.Text += *ch.Delta.Content
			}
			for _, tc := range ch.Delta.ToolCalls {
				for len(s.Tools) <= tc.Index {
					s.Tools = append(s.Tools, streamedTool{})
				}
				tool := &s.Tools[tc.Index]
				if tc.ID != "" {
					tool.ID = tc.ID
				}
				if tc.Function.Name != "" {
					tool.Name = tc.Function.Name
				}
				tool.Args += tc.Function.Arguments
			}
			if ch.FinishReason != nil {
				if s.FinishReason != "" {
					t.Errorf("second finish_reason %q", *ch.FinishReason)
				}
				s.FinishReason = *ch.FinishReason
			}
		}
	}
	return s
}

func TestAnthropicStreamToOpenAIClient(t *testing.T) {
	req := &adapter.Request{Model: "gpt-best", Stream: true, IncludeUsage: true}
	events := translate(t, anthropic.New().NewStreamDecoder(), New().NewStreamEncoder(req), anthropicToolStream)
	got := collectOpenAI(t, events)

	if !reflect.DeepEqual(got.IDs, map[string]bool{"msg_01ABC": true}) || !reflect.DeepEqual(got.Models, map[string]bool{"claude-sonnet-4-5-20250929": true}) {
		t.Errorf("ids %v, models %v", got.IDs, got.Models)
	}
	if got.Role != "assistant" || got.Text != "I'll check the weather." {
		t.Errorf("role %q, text %q", got.Role, got.Text)
	}
	wantTools := []streamedTool{
		{ID: "toolu_01A", Name: "get_weather", Args: `{"location": "San Francisco, CA", "unit": "celsius"}`},
		{ID: "toolu_01B", Name: "get_time", Args: "{}"},
	}
	if !reflect.DeepEqual(got.Tools, wantTools) {
		t.Errorf("tools = %+v, want %+v", got.Tools, wantTools)
	}
	if !jsonEqual(got.Tools[0].Args, `{"location":"San Francisco, CA","unit":"celsius"}`) {
		t.Errorf("tool arguments are not the original JSON: %s", got.Tools[0].Args)
	}
	if got.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q", got.FinishReason)
	}
	if want := (usage{PromptTokens: 112, CompletionTokens: 89, TotalTokens: 201}); got.Usage == nil || *got.Usage != want {
		t.Errorf("usage = %+v, want %+v", got.Usage, want)
	}
}

func TestAnthropicStreamToOpenAIClientWithoutUsage(t *testing.T) {
	req := &adapter.Request{Model: "gpt-best", Stream: true}
	events := translate(t, anthropic.New().NewStreamDecoder(), New().NewStreamEncoder(req), anthropicToolStream)
	if got := collectOpenAI(t, events); got.Usage != nil {
		t.Errorf("usage chunk sent without include_usage: %+v", got.Usage)
	}
}
