// Package adapter defines a protocol-neutral chat model and the interfaces that
// translate between it and the wire formats of clients and upstream providers.
//
// Each wire format lives in its own sub-package (openai, anthropic, gemini).
// A format used by clients implements Inbound; a format used by providers
// implements Upstream.
package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// Format identifies a chat wire protocol. Provider types use the same values.
type Format string

const (
	OpenAI    Format = "openai"
	Anthropic Format = "anthropic"
	Gemini    Format = "gemini"
)

// Role of a message author. System prompts are kept in Request.System and
// tool results are parts of user messages.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// PartType identifies the kind of a message part.
type PartType string

const (
	PartText       PartType = "text"
	PartImage      PartType = "image"
	PartToolCall   PartType = "tool_call"
	PartToolResult PartType = "tool_result"
)

// StopReason is why the model stopped generating.
type StopReason string

const (
	StopEnd           StopReason = "end"            // natural end or stop sequence
	StopMaxTokens     StopReason = "max_tokens"     // output limit reached
	StopToolUse       StopReason = "tool_use"       // model wants tool results
	StopContentFilter StopReason = "content_filter" // blocked or refused
)

// Request is a chat request in canonical form.
type Request struct {
	Model       string
	System      string // all system/developer instructions joined by "\n\n"
	Messages    []Message
	Tools       []Tool
	ToolChoice  *ToolChoice // nil means provider default (auto)
	MaxTokens   int         // 0 means not set
	Temperature *float64
	TopP        *float64
	Stop        []string
	Stream      bool
	// IncludeUsage is set when an OpenAI client asked for a usage chunk at the
	// end of a stream (stream_options.include_usage).
	IncludeUsage bool
}

// Message is one conversation turn.
type Message struct {
	Role  Role
	Parts []Part
}

// Part is one piece of message content. Exactly one of the fields matching
// Type is set.
type Part struct {
	Type       PartType
	Text       string
	Image      *Image
	ToolCall   *ToolCall
	ToolResult *ToolResult
}

// Image is an image either inline (MediaType + base64 Data) or by URL.
type Image struct {
	MediaType string // e.g. "image/png"; may be empty for URL images
	Data      string // base64 without "data:" prefix
	URL       string // remote URL (when Data is empty)
}

// ToolCall is a function call requested by the assistant.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON object text, "{}" when there are no arguments
}

// ToolResult is the result of a tool call, sent back by the client.
type ToolResult struct {
	CallID  string
	Name    string // tool name when known (required by Gemini; may be empty)
	Content string // text content; images are replaced by "[image omitted]"
	IsError bool
}

// Tool is a function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema of the arguments object
}

// ToolChoice controls tool usage.
type ToolChoice struct {
	Mode string // "auto", "none", "required" or "tool"
	Name string // tool name when Mode == "tool"
}

// Response is a complete (non-streaming) chat response.
type Response struct {
	ID         string
	Model      string
	Content    []Part // text and tool_call parts, in order
	StopReason StopReason
	Usage      Usage
}

// Usage holds token counts. InputTokens includes cached prompt tokens.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// EventType identifies a streaming event.
type EventType int

const (
	// EventStart opens the message. ID and Model may be set. Decoders emit it
	// at most once, before any other event; encoders must not rely on it.
	EventStart EventType = iota
	// EventText carries a text delta in Text.
	EventText
	// EventToolStart opens tool call number ToolIndex (0-based, in order of
	// appearance within the message) with ToolID and ToolName.
	EventToolStart
	// EventToolArgs carries a fragment of the JSON arguments of tool call
	// ToolIndex in ArgsDelta.
	EventToolArgs
	// EventStop carries the StopReason. It may be followed by EventUsage.
	EventStop
	// EventUsage carries cumulative token counts; the latest value wins.
	EventUsage
)

// Event is one canonical streaming event.
type Event struct {
	Type       EventType
	ID         string
	Model      string
	Text       string
	ToolIndex  int
	ToolID     string
	ToolName   string
	ArgsDelta  string
	StopReason StopReason
	Usage      *Usage
}

// Inbound is a client-facing chat protocol.
type Inbound interface {
	Format() Format
	// DecodeRequest parses a client request body.
	DecodeRequest(body []byte) (*Request, error)
	// EncodeResponse renders a complete response for the client.
	EncodeResponse(resp *Response) ([]byte, error)
	// NewStreamEncoder returns an encoder for one streamed response.
	// req is the decoded client request (e.g. for IncludeUsage and Model).
	NewStreamEncoder(req *Request) StreamEncoder
	// EncodeError renders an error body (JSON) in this protocol's format.
	EncodeError(status int, message string) []byte
}

// Upstream is a provider-facing chat protocol.
type Upstream interface {
	Format() Format
	// ChatURL returns the chat endpoint for baseURL (no trailing slash) and the
	// upstream model name.
	ChatURL(baseURL, model string, stream bool) string
	// ModelsURL returns an endpoint that lists models; used to test a key.
	ModelsURL(baseURL string) string
	// SetAuth sets the authentication headers for apiKey (and any headers the
	// protocol always requires, such as anthropic-version).
	SetAuth(h http.Header, apiKey string)
	// EncodeRequest renders req as an upstream request body for model.
	EncodeRequest(req *Request, model string) ([]byte, error)
	// DecodeResponse parses a non-streaming response body.
	DecodeResponse(body []byte) (*Response, error)
	// NewStreamDecoder returns a decoder for one upstream stream.
	NewStreamDecoder() StreamDecoder
	// ErrorMessage extracts a readable message from an upstream error body.
	// It falls back to the (truncated) raw body.
	ErrorMessage(body []byte) string
}

// StreamDecoder converts upstream SSE events into canonical events.
type StreamDecoder interface {
	// Decode converts one upstream event into zero or more canonical events.
	// It returns io.EOF when the upstream signals the end of the stream (the
	// events returned alongside io.EOF, if any, are still valid) and *Error
	// when the upstream reports an error inside the stream.
	Decode(ev sse.Event) ([]Event, error)
}

// StreamEncoder converts canonical events into client SSE events.
type StreamEncoder interface {
	// Encode converts one canonical event into zero or more client events.
	Encode(ev Event) []sse.Event
	// Finish returns the events that close the stream after the upstream
	// stream ended (it must cope with a missing EventStop).
	Finish() []sse.Event
	// Error returns the events that report a failure in the middle of the
	// stream to the client. No other method is called afterwards.
	Error(message string) []sse.Event
}

// Error is an error reported by an upstream inside an otherwise successful
// response, such as an SSE error event. Status is the closest HTTP status
// (429 for rate limits, 529 for overload, 500/502 otherwise).
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("upstream error (%d): %s", e.Status, e.Message) }

// Truncate shortens s to at most n bytes (on a UTF-8 boundary), appending
// "..." when cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
