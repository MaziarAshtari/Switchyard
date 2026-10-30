package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// jsonEqual compares two JSON texts semantically; non-JSON is compared as is.
func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	return reflect.DeepEqual(x, y)
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	if !json.Valid(got) {
		t.Fatalf("invalid JSON: %s", got)
	}
	if !jsonEqual(string(got), want) {
		t.Errorf("JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func assertEvents(t *testing.T, got, want []sse.Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d:\n%s", len(got), len(want), dumpSSE(got))
	}
	for i := range want {
		if got[i].Name != want[i].Name || !jsonEqual(got[i].Data, want[i].Data) {
			t.Errorf("event %d:\n got: %q %s\nwant: %q %s", i, got[i].Name, got[i].Data, want[i].Name, want[i].Data)
		}
	}
}

// ev builds an expected named event whose name is the payload's type.
func ev(data string) sse.Event {
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(data), &p)
	return sse.Event{Name: p.Type, Data: data}
}

func dumpSSE(events []sse.Event) string {
	var b strings.Builder
	for _, e := range events {
		b.Write(e.Bytes())
	}
	return b.String()
}

func dumpEvents(events []adapter.Event) string {
	b, _ := json.MarshalIndent(events, "", "  ")
	return string(b)
}

func assertCanonicalEvents(t *testing.T, got, want []adapter.Event) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events mismatch\n got: %s\nwant: %s", dumpEvents(got), dumpEvents(want))
	}
}

// readSSE parses a raw SSE stream.
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

// decodeStream feeds every event of stream to d. It stops at io.EOF and
// reports whether it was seen.
func decodeStream(t *testing.T, d adapter.StreamDecoder, stream string) (events []adapter.Event, sawEOF bool) {
	t.Helper()
	for _, e := range readSSE(t, stream) {
		out, err := d.Decode(e)
		events = append(events, out...)
		if errors.Is(err, io.EOF) {
			return events, true
		}
		if err != nil {
			t.Fatalf("Decode(%q): %v", e.Data, err)
		}
	}
	return events, false
}

// translate pipes an upstream stream through decoder and encoder like the
// gateway does, including Finish at the end.
func translate(t *testing.T, d adapter.StreamDecoder, e adapter.StreamEncoder, stream string) []sse.Event {
	t.Helper()
	events, _ := decodeStream(t, d, stream)
	var out []sse.Event
	for _, e2 := range events {
		out = append(out, e.Encode(e2)...)
	}
	return append(out, e.Finish()...)
}

func ptr[T any](v T) *T { return &v }

func textPart(s string) adapter.Part { return adapter.Part{Type: adapter.PartText, Text: s} }

func imagePart(img adapter.Image) adapter.Part {
	return adapter.Part{Type: adapter.PartImage, Image: &img}
}

func callPart(id, name, args string) adapter.Part {
	return adapter.Part{Type: adapter.PartToolCall, ToolCall: &adapter.ToolCall{ID: id, Name: name, Arguments: args}}
}

func resultPart(callID, content string, isError bool) adapter.Part {
	return adapter.Part{Type: adapter.PartToolResult, ToolResult: &adapter.ToolResult{CallID: callID, Content: content, IsError: isError}}
}

type streamedBlock struct {
	Type, Text, ID, Name, JSON string
}

// anthropicStream is what an Anthropic SDK reassembles from an event stream.
type anthropicStream struct {
	ID, Model    string
	StartInput   int
	Blocks       []streamedBlock
	StopReason   string
	InputTokens  int
	OutputTokens int
}

// collectAnthropic reassembles a stream and checks protocol invariants:
// event name equals the data type, message_start first, block indexes
// sequential, deltas only for the open block, message_stop last.
func collectAnthropic(t *testing.T, events []sse.Event) anthropicStream {
	t.Helper()
	var s anthropicStream
	open := -1
	for i, e := range events {
		var p struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				Text string `json:"text"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
			t.Fatalf("invalid event data %s: %v", e.Data, err)
		}
		if e.Name != p.Type {
			t.Errorf("event %d: name %q != type %q", i, e.Name, p.Type)
		}
		if (i == 0) != (p.Type == "message_start") {
			t.Errorf("event %d: unexpected %s", i, p.Type)
		}
		if (i == len(events)-1) != (p.Type == "message_stop") {
			t.Errorf("event %d: unexpected %s", i, p.Type)
		}
		switch p.Type {
		case "message_start":
			s.ID, s.Model, s.StartInput = p.Message.ID, p.Message.Model, p.Message.Usage.InputTokens
		case "content_block_start":
			if open != -1 || p.Index != len(s.Blocks) {
				t.Errorf("event %d: block %d started while %d open, %d blocks", i, p.Index, open, len(s.Blocks))
			}
			open = p.Index
			cb := p.ContentBlock
			s.Blocks = append(s.Blocks, streamedBlock{Type: cb.Type, Text: cb.Text, ID: cb.ID, Name: cb.Name})
		case "content_block_delta":
			if p.Index != open {
				t.Fatalf("event %d: delta for block %d while %d open", i, p.Index, open)
			}
			s.Blocks[open].Text += p.Delta.Text
			s.Blocks[open].JSON += p.Delta.PartialJSON
		case "content_block_stop":
			if p.Index != open {
				t.Errorf("event %d: stop for block %d while %d open", i, p.Index, open)
			}
			open = -1
		case "message_delta":
			if open != -1 {
				t.Errorf("message_delta with block %d open", open)
			}
			s.StopReason, s.InputTokens, s.OutputTokens = p.Delta.StopReason, p.Usage.InputTokens, p.Usage.OutputTokens
		}
	}
	return s
}
