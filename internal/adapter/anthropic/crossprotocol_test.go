package anthropic

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/gemini"
	"github.com/MaziarAshtari/Switchyard/internal/adapter/openai"
)

// openAIToolStream is a realistic OpenAI stream with text, two tool calls
// with streamed arguments and a final usage chunk.
const openAIToolStream = `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"Let me "},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"check."},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"Paris, FR\", \"days\": [1, 2]}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"tz\":"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"CET\"}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}],"usage":null}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":30,"total_tokens":50,"prompt_tokens_details":{"cached_tokens":0}}}

data: [DONE]

`

func TestOpenAIStreamToAnthropicClient(t *testing.T) {
	req := &adapter.Request{Model: "claude-best", Stream: true}
	events := translate(t, openai.New().NewStreamDecoder(), New().NewStreamEncoder(req), openAIToolStream)
	got := collectAnthropic(t, events)

	if got.ID != "chatcmpl-abc" || got.Model != "gpt-4o-2024-08-06" {
		t.Errorf("id %q, model %q", got.ID, got.Model)
	}
	wantBlocks := []streamedBlock{
		{Type: "text", Text: "Let me check."},
		{Type: "tool_use", ID: "call_1", Name: "get_weather", JSON: `{"location": "Paris, FR", "days": [1, 2]}`},
		{Type: "tool_use", ID: "call_2", Name: "get_time", JSON: `{"tz":"CET"}`},
	}
	if !reflect.DeepEqual(got.Blocks, wantBlocks) {
		t.Errorf("blocks = %+v\nwant %+v", got.Blocks, wantBlocks)
	}
	if !jsonEqual(got.Blocks[1].JSON, `{"location":"Paris, FR","days":[1,2]}`) {
		t.Errorf("reassembled tool input is not the original JSON: %s", got.Blocks[1].JSON)
	}
	if got.StopReason != "tool_use" || got.InputTokens != 20 || got.OutputTokens != 30 {
		t.Errorf("stop %q, usage %d/%d", got.StopReason, got.InputTokens, got.OutputTokens)
	}
}

// geminiToolStream is a realistic streamGenerateContent?alt=sse stream with a
// thought part, text, two function calls and usage metadata.
const geminiToolStream = `data: {"candidates": [{"content": {"parts": [{"text": "Thinking about it", "thought": true}],"role": "model"},"index": 0}],"usageMetadata": {"promptTokenCount": 12,"totalTokenCount": 12,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

data: {"candidates": [{"content": {"parts": [{"text": "Let me "}],"role": "model"},"index": 0}],"usageMetadata": {"promptTokenCount": 12,"candidatesTokenCount": 2,"totalTokenCount": 34,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

data: {"candidates": [{"content": {"parts": [{"text": "check."},{"functionCall": {"name": "get_weather","args": {"location": "Paris, FR","days": [1, 2]}},"thoughtSignature": "CiQBVKhc7"},{"functionCall": {"id": "fc_2","name": "get_time","args": {}}}],"role": "model"},"finishReason": "STOP","index": 0}],"usageMetadata": {"promptTokenCount": 12,"candidatesTokenCount": 25,"totalTokenCount": 57,"thoughtsTokenCount": 20},"modelVersion": "gemini-2.5-flash","responseId": "resp-1"}

`

func TestGeminiStreamToAnthropicClient(t *testing.T) {
	req := &adapter.Request{Model: "claude-best", Stream: true}
	events := translate(t, gemini.New().NewStreamDecoder(), New().NewStreamEncoder(req), geminiToolStream)
	got := collectAnthropic(t, events)

	if got.ID != "resp-1" || got.Model != "gemini-2.5-flash" {
		t.Errorf("id %q, model %q", got.ID, got.Model)
	}
	if len(got.Blocks) != 3 {
		t.Fatalf("got %d blocks: %+v", len(got.Blocks), got.Blocks)
	}
	if !strings.HasPrefix(got.Blocks[1].ID, "call_") {
		t.Errorf("generated tool ID %q", got.Blocks[1].ID)
	}
	got.Blocks[1].ID = "generated"
	wantBlocks := []streamedBlock{
		{Type: "text", Text: "Let me check."},
		{Type: "tool_use", ID: "generated", Name: "get_weather", JSON: `{"location":"Paris, FR","days":[1,2]}`},
		{Type: "tool_use", ID: "fc_2", Name: "get_time", JSON: `{}`},
	}
	if !reflect.DeepEqual(got.Blocks, wantBlocks) {
		t.Errorf("blocks = %+v\nwant %+v", got.Blocks, wantBlocks)
	}
	// Output includes thinking tokens.
	if got.StopReason != "tool_use" || got.InputTokens != 12 || got.OutputTokens != 45 {
		t.Errorf("stop %q, usage %d/%d", got.StopReason, got.InputTokens, got.OutputTokens)
	}
}
