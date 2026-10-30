// Package openai implements the OpenAI Chat Completions wire format, both as a
// client-facing protocol (adapter.Inbound) and as a provider-facing one
// (adapter.Upstream).
package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
)

// Adapter converts between the Chat Completions format and the canonical
// adapter model. It is stateless; streams get their own encoder or decoder.
type Adapter struct{}

var (
	_ adapter.Inbound  = (*Adapter)(nil)
	_ adapter.Upstream = (*Adapter)(nil)
)

// New returns an OpenAI adapter.
func New() *Adapter { return &Adapter{} }

// Format implements adapter.Inbound and adapter.Upstream.
func (*Adapter) Format() adapter.Format { return adapter.OpenAI }

// chatRequest is the translated subset of a Chat Completions request. It is
// used to decode client requests and to encode upstream requests.
type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []message       `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stop                stringList      `json:"stop,omitempty"`
	Stream              bool            `json:"stream"`
	StreamOptions       *streamOptions  `json:"stream_options,omitempty"`
	Tools               []tool          `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// message is a chat message (requests and responses) or a stream delta.
// Content is a string, an array of content parts, or null.
type message struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Name         string          `json:"name,omitempty"`
	ToolCalls    []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	FunctionCall *function       `json:"function_call,omitempty"` // legacy function calling
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type toolCall struct {
	Index    *int     `json:"index,omitempty"` // stream deltas only
	ID       string   `json:"id,omitempty"`
	Type     string   `json:"type,omitempty"`
	Function function `json:"function"`
}

type function struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func usageOf(u adapter.Usage) *usage {
	return &usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
}

// stringList decodes either a string or an array of strings.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	switch {
	case len(b) == 0 || string(b) == "null":
		*l = nil
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*l = stringList{s}
		return nil
	case b[0] == '[':
		return json.Unmarshal(b, (*[]string)(l))
	}
	return errors.New("stop must be a string or an array of strings")
}

// parseContent parses message content: a string (one text part), an array of
// parts, or null.
func parseContent(raw json.RawMessage) ([]contentPart, error) {
	switch {
	case isNull(raw):
		return nil, nil
	case raw[0] == '"':
		var s string
		err := json.Unmarshal(raw, &s)
		return []contentPart{{Type: "text", Text: s}}, err
	case raw[0] == '[':
		var parts []contentPart
		err := json.Unmarshal(raw, &parts)
		return parts, err
	}
	return nil, errors.New("content must be a string or an array")
}

// joinText joins the non-empty text parts with sep.
func joinText(parts []contentPart, sep string) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, sep)
}

// imageFromURL turns a base64 data URL into an inline image; other URLs stay
// remote.
func imageFromURL(u string) *adapter.Image {
	if rest, ok := strings.CutPrefix(u, "data:"); ok {
		meta, data, ok := strings.Cut(rest, ",")
		if ok && strings.HasSuffix(meta, ";base64") {
			mediaType, _, _ := strings.Cut(meta, ";")
			return &adapter.Image{MediaType: mediaType, Data: data}
		}
	}
	return &adapter.Image{URL: u}
}

// imageURLOf is the inverse of imageFromURL.
func imageURLOf(img *adapter.Image) string {
	if img.Data != "" {
		return "data:" + img.MediaType + ";base64," + img.Data
	}
	return img.URL
}

func stopReason(finishReason string) adapter.StopReason {
	switch finishReason {
	case "length":
		return adapter.StopMaxTokens
	case "tool_calls", "function_call":
		return adapter.StopToolUse
	case "content_filter":
		return adapter.StopContentFilter
	}
	return adapter.StopEnd
}

func finishReason(r adapter.StopReason) string {
	switch r {
	case adapter.StopMaxTokens:
		return "length"
	case adapter.StopToolUse:
		return "tool_calls"
	case adapter.StopContentFilter:
		return "content_filter"
	}
	return "stop"
}

// ErrorMessage implements adapter.Upstream. It understands
// {"error":{"message":...}}, {"error":"..."} and {"message":"..."}.
func (*Adapter) ErrorMessage(body []byte) string {
	var b struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &b) == nil {
		if msg, _ := parseError(b.Error); msg != "" {
			return msg
		}
		if b.Message != "" {
			return b.Message
		}
	}
	return rawMessage(body)
}

// parseError reads an "error" value (an object or a string). status is the
// error code when it is an HTTP error status, else 0.
func parseError(raw json.RawMessage) (msg string, status int) {
	if isNull(raw) {
		return "", 0
	}
	if raw[0] == '"' {
		_ = json.Unmarshal(raw, &msg)
		return msg, 0
	}
	var e struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	if code, err := strconv.Atoi(strings.Trim(string(e.Code), `"`)); err == nil && code >= 400 && code <= 599 {
		status = code
	}
	return e.Message, status
}

// bodyError converts the "error" value of a 200 response or a stream chunk
// into an *adapter.Error; nil when there is none.
func bodyError(raw json.RawMessage, body []byte) *adapter.Error {
	if isNull(raw) {
		return nil
	}
	msg, status := parseError(raw)
	if msg == "" {
		msg = rawMessage(body)
	}
	if status == 0 {
		status = http.StatusBadGateway
	}
	return &adapter.Error{Status: status, Message: msg}
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
