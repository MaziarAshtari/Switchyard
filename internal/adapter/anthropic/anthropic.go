// Package anthropic implements the Anthropic Messages wire format, both as a
// client-facing protocol (adapter.Inbound) and as a provider-facing one
// (adapter.Upstream).
package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
)

// Adapter converts between the Messages format and the canonical adapter
// model. It is stateless; streams get their own encoder or decoder.
type Adapter struct{}

var (
	_ adapter.Inbound  = (*Adapter)(nil)
	_ adapter.Upstream = (*Adapter)(nil)
)

// New returns an Anthropic adapter.
func New() *Adapter { return &Adapter{} }

// Format implements adapter.Inbound and adapter.Upstream.
func (*Adapter) Format() adapter.Format { return adapter.Anthropic }

// messagesRequest is the translated subset of a Messages request. It is used
// to decode client requests and to encode upstream requests.
type messagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	System        json.RawMessage `json:"system,omitempty"` // string or text blocks
	Messages      []message       `json:"messages"`
	Tools         []tool          `json:"tools,omitempty"`
	ToolChoice    *toolChoice     `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream"`
}

type message struct {
	Role    string  `json:"role"`
	Content content `json:"content"`
}

// content is a list of blocks. When decoding, a plain string is accepted as
// one text block.
type content []block

func (c *content) UnmarshalJSON(b []byte) error {
	switch {
	case len(b) == 0 || string(b) == "null":
		*c = nil
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = content{{Type: "text", Text: s}}
		return nil
	case b[0] == '[':
		return json.Unmarshal(b, (*[]block)(c))
	}
	return errors.New("content must be a string or an array of content blocks")
}

// block is a content block of any type; only the fields of its type are set.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Source    *source         `json:"source,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // tool_result: string or blocks
	IsError   bool            `json:"is_error,omitempty"`
}

type source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

func (s *source) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] != '{' {
		return nil // e.g. search_result blocks have a string source
	}
	type plain source
	return json.Unmarshal(b, (*plain)(s))
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type errorResponse struct {
	Type  string   `json:"type"`
	Error apiError `json:"error"`
}

// usage is a usage object in a response or stream event. Fields are pointers
// because message_delta may omit any of them.
type usage struct {
	InputTokens              *int `json:"input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
}

// usageTotals keeps the latest value of each usage counter of one message.
type usageTotals struct{ input, cacheCreation, cacheRead, output int }

// update applies the counters present in u and returns the canonical usage,
// whose input includes cached prompt tokens.
func (t *usageTotals) update(u *usage) adapter.Usage {
	if u != nil {
		set(&t.input, u.InputTokens)
		set(&t.cacheCreation, u.CacheCreationInputTokens)
		set(&t.cacheRead, u.CacheReadInputTokens)
		set(&t.output, u.OutputTokens)
	}
	return adapter.Usage{InputTokens: t.input + t.cacheCreation + t.cacheRead, OutputTokens: t.output}
}

func set(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// outUsage is the usage object sent to clients.
type outUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func stopReason(s string) adapter.StopReason {
	switch s {
	case "max_tokens", "model_context_window_exceeded":
		return adapter.StopMaxTokens
	case "tool_use":
		return adapter.StopToolUse
	case "refusal":
		return adapter.StopContentFilter
	}
	return adapter.StopEnd // end_turn, stop_sequence, pause_turn, unknown
}

func stopReasonString(r adapter.StopReason) string {
	switch r {
	case adapter.StopMaxTokens:
		return "max_tokens"
	case adapter.StopToolUse:
		return "tool_use"
	case adapter.StopContentFilter:
		return "refusal"
	}
	return "end_turn"
}

// errorTypes maps HTTP statuses to Anthropic error types and back.
var errorTypes = map[int]string{
	http.StatusBadRequest:            "invalid_request_error",
	http.StatusUnauthorized:          "authentication_error",
	http.StatusPaymentRequired:       "billing_error",
	http.StatusForbidden:             "permission_error",
	http.StatusNotFound:              "not_found_error",
	http.StatusRequestEntityTooLarge: "request_too_large",
	http.StatusTooManyRequests:       "rate_limit_error",
	http.StatusInternalServerError:   "api_error",
	529:                              "overloaded_error",
}

func errorType(status int) string {
	if t, ok := errorTypes[status]; ok {
		return t
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

func errorStatus(typ string) int {
	for status, t := range errorTypes {
		if t == typ {
			return status
		}
	}
	return http.StatusBadGateway
}

// ErrorMessage implements adapter.Upstream.
func (*Adapter) ErrorMessage(body []byte) string {
	var b struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(body, &b) == nil && b.Error != nil && b.Error.Message != "" {
		return b.Error.Message
	}
	return rawMessage(body)
}

// bodyError converts an error object reported with status 200 or inside a
// stream.
func bodyError(e *apiError, body []byte) *adapter.Error {
	if e == nil {
		return &adapter.Error{Status: http.StatusBadGateway, Message: rawMessage(body)}
	}
	msg := e.Message
	if msg == "" {
		msg = rawMessage(body)
	}
	return &adapter.Error{Status: errorStatus(e.Type), Message: msg}
}

func rawMessage(body []byte) string {
	return adapter.Truncate(strings.TrimSpace(string(body)), 500)
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// jsonObject returns s as raw JSON when it is a JSON object, else {}.
func jsonObject(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") && json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return json.RawMessage("{}")
}

// compactJSON returns raw as compact JSON text, or "{}" when it is absent or
// invalid.
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if isNull(raw) || json.Compact(&b, raw) != nil {
		return "{}"
	}
	return b.String()
}
