package sse

import (
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, input string) []Event {
	t.Helper()
	r := NewReader(strings.NewReader(input))
	var out []Event
	for {
		ev, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, ev)
	}
}

func TestReaderParsesNamedAndUnnamedEvents(t *testing.T) {
	input := "event: message_start\ndata: {\"a\":1}\n\n" +
		": keep-alive comment\n\n" +
		"data: first\ndata: second\n\n" +
		"data: [DONE]\n\n"
	got := readAll(t, input)
	want := []Event{
		{Name: "message_start", Data: `{"a":1}`},
		{Data: "first\nsecond"},
		{Data: "[DONE]"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestReaderHandlesCRLFAndMissingTrailingBlankLine(t *testing.T) {
	got := readAll(t, "data:no-space\r\n\r\ndata: last")
	if len(got) != 2 || got[0].Data != "no-space" || got[1].Data != "last" {
		t.Fatalf("unexpected events: %#v", got)
	}
}

func TestReaderLongLine(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	got := readAll(t, "data: "+long+"\n\n")
	if len(got) != 1 || got[0].Data != long {
		t.Fatalf("long line not preserved (got %d events)", len(got))
	}
}

func TestEventBytesRoundTrip(t *testing.T) {
	ev := Event{Name: "content_block_delta", Data: "line1\nline2"}
	want := "event: content_block_delta\ndata: line1\ndata: line2\n\n"
	if string(ev.Bytes()) != want {
		t.Fatalf("Bytes() = %q, want %q", ev.Bytes(), want)
	}
	got := readAll(t, string(ev.Bytes()))
	if len(got) != 1 || got[0] != ev {
		t.Fatalf("round trip = %#v", got)
	}
}
