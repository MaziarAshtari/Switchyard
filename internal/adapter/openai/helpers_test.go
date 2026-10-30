package openai

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

const fixedUnix = 1700000000

// fixNow freezes the "created" timestamp for the duration of the test.
func fixNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Unix(fixedUnix, 0) }
	t.Cleanup(func() { now = old })
}

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
		ev, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("reading SSE: %v", err)
		}
		out = append(out, ev)
	}
}

// decodeStream feeds every event of stream to d. It stops at io.EOF and
// reports whether it was seen.
func decodeStream(t *testing.T, d adapter.StreamDecoder, stream string) (events []adapter.Event, sawEOF bool) {
	t.Helper()
	for _, ev := range readSSE(t, stream) {
		out, err := d.Decode(ev)
		events = append(events, out...)
		if errors.Is(err, io.EOF) {
			return events, true
		}
		if err != nil {
			t.Fatalf("Decode(%q): %v", ev.Data, err)
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
	for _, ev := range events {
		out = append(out, e.Encode(ev)...)
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
