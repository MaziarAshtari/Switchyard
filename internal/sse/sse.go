// Package sse reads and writes Server-Sent Events streams.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// Event is one SSE event. Name is the "event:" field (empty for unnamed events);
// Data is the concatenation of its "data:" lines joined by "\n".
type Event struct {
	Name string
	Data string
}

// maxLine bounds a single SSE line. Some upstreams send large JSON chunks
// (e.g. base64 images) on one line.
const maxLine = 32 << 20

// ErrLineTooLong is returned when a line exceeds the size limit.
var ErrLineTooLong = errors.New("sse: line too long")

// Reader parses an SSE stream.
type Reader struct {
	r *bufio.Reader
}

// NewReader returns a Reader that parses events from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next returns the next event. Comment lines and the id/retry fields are ignored.
// It returns io.EOF once the stream ends; a final event without its terminating
// blank line is still returned before io.EOF.
func (r *Reader) Next() (Event, error) {
	var (
		ev      Event
		data    []string
		hasData bool
		hasName bool
	)
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (hasData || hasName) {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			return Event{}, err
		}
		if line == "" {
			if !hasData && !hasName {
				continue // stray blank line
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if line[0] == ':' {
			continue // comment / keep-alive
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
			hasName = true
		case "data":
			data = append(data, value)
			hasData = true
		}
	}
}

// readLine returns one line without its LF or CRLF terminator. A bare CR is not
// treated as a line break; no known upstream uses it.
func (r *Reader) readLine() (string, error) {
	var buf []byte
	for {
		chunk, err := r.r.ReadSlice('\n')
		if len(buf)+len(chunk) > maxLine {
			return "", ErrLineTooLong
		}
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return strings.TrimRight(string(buf), "\r\n"), nil
			}
			return "", err
		}
		return strings.TrimRight(string(buf), "\r\n"), nil
	}
}

// Bytes renders the event in wire format, including the terminating blank line.
func (e Event) Bytes() []byte {
	var b bytes.Buffer
	if e.Name != "" {
		b.WriteString("event: ")
		b.WriteString(e.Name)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(e.Data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// Write writes events to w in wire format.
func Write(w io.Writer, events ...Event) error {
	for _, e := range events {
		if _, err := w.Write(e.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
