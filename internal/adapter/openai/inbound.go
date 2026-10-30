package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// now is replaced in tests.
var now = time.Now

// DecodeRequest implements adapter.Inbound.
func (*Adapter) DecodeRequest(body []byte) (*adapter.Request, error) {
	var in chatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, invalidRequest(err)
	}
	if in.Messages == nil {
		return nil, errors.New("messages must be an array")
	}
	req := &adapter.Request{
		Model:       in.Model,
		MaxTokens:   in.MaxCompletionTokens,
		Temperature: in.Temperature,
		TopP:        in.TopP,
		Stream:      in.Stream,
		ToolChoice:  decodeToolChoice(in.ToolChoice),
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = in.MaxTokens
	}
	if in.StreamOptions != nil {
		req.IncludeUsage = in.StreamOptions.IncludeUsage
	}
	for _, s := range in.Stop {
		if s != "" {
			req.Stop = append(req.Stop, s)
		}
	}
	for _, t := range in.Tools {
		if (t.Type == "function" || t.Type == "") && t.Function.Name != "" {
			req.Tools = append(req.Tools, adapter.Tool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			})
		}
	}
	if err := decodeMessages(req, in.Messages); err != nil {
		return nil, err
	}
	return req, nil
}

// invalidRequest describes a JSON decoding error without Go type names.
func invalidRequest(err error) error {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Errorf("malformed JSON: %v", err)
	case errors.As(err, &typ) && typ.Field != "":
		return fmt.Errorf("field %q has the wrong type", typ.Field)
	case errors.As(err, &typ):
		return errors.New("a value has the wrong type")
	}
	return err
}

// decodeMessages converts the message list into req. System and developer
// messages go to req.System; consecutive tool messages share one user message.
func decodeMessages(req *adapter.Request, msgs []message) error {
	var system []string
	legacyCallID := "" // ID given to the last legacy function_call
	prevTool := false
	for i, m := range msgs {
		parts, err := parseContent(m.Content)
		if err != nil {
			return fmt.Errorf("messages[%d]: %w", i, err)
		}
		isTool := m.Role == "tool" || m.Role == "function"
		switch {
		case m.Role == "system" || m.Role == "developer":
			if text := joinText(parts, "\n\n"); text != "" {
				system = append(system, text)
			}
		case m.Role == "assistant":
			msg := adapter.Message{Role: adapter.RoleAssistant}
			for _, p := range parts {
				if p.Type == "text" && p.Text != "" {
					msg.Parts = append(msg.Parts, adapter.Part{Type: adapter.PartText, Text: p.Text})
				}
			}
			for _, tc := range m.ToolCalls {
				msg.Parts = append(msg.Parts, toolCallPart(tc.ID, tc.Function))
			}
			if m.FunctionCall != nil {
				legacyCallID = adapter.NewID("call_")
				msg.Parts = append(msg.Parts, toolCallPart(legacyCallID, *m.FunctionCall))
			}
			req.Messages = append(req.Messages, msg)
		case isTool:
			callID := m.ToolCallID
			if callID == "" && m.Role == "function" {
				callID = legacyCallID
			}
			result := adapter.Part{Type: adapter.PartToolResult, ToolResult: &adapter.ToolResult{
				CallID:  callID,
				Name:    m.Name,
				Content: joinText(parts, "\n"),
			}}
			if prevTool {
				last := &req.Messages[len(req.Messages)-1]
				last.Parts = append(last.Parts, result)
			} else {
				req.Messages = append(req.Messages, adapter.Message{Role: adapter.RoleUser, Parts: []adapter.Part{result}})
			}
		default: // user (unknown roles are treated as user)
			msg := adapter.Message{Role: adapter.RoleUser}
			for _, p := range parts {
				switch {
				case p.Type == "text" && p.Text != "":
					msg.Parts = append(msg.Parts, adapter.Part{Type: adapter.PartText, Text: p.Text})
				case p.Type == "image_url" && p.ImageURL != nil && p.ImageURL.URL != "":
					msg.Parts = append(msg.Parts, adapter.Part{Type: adapter.PartImage, Image: imageFromURL(p.ImageURL.URL)})
				}
			}
			req.Messages = append(req.Messages, msg)
		}
		prevTool = isTool
	}
	req.System = strings.Join(system, "\n\n")
	return nil
}

func toolCallPart(id string, f function) adapter.Part {
	args := f.Arguments
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	return adapter.Part{Type: adapter.PartToolCall, ToolCall: &adapter.ToolCall{ID: id, Name: f.Name, Arguments: args}}
}

func decodeToolChoice(raw json.RawMessage) *adapter.ToolChoice {
	if isNull(raw) {
		return nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		switch mode {
		case "auto", "none", "required":
			return &adapter.ToolChoice{Mode: mode}
		}
		return nil
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &named) == nil && named.Function.Name != "" {
		return &adapter.ToolChoice{Mode: "tool", Name: named.Function.Name}
	}
	return nil
}

// completion is a chat.completion response or a chat.completion.chunk.
type completion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
}

type choice struct {
	Index        int             `json:"index"`
	Message      *message        `json:"message,omitempty"`
	Delta        *delta          `json:"delta,omitempty"`
	Logprobs     json.RawMessage `json:"logprobs"` // always null
	FinishReason *string         `json:"finish_reason"`
}

type delta struct {
	Role      string     `json:"role,omitempty"`
	Content   *string    `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

// EncodeResponse implements adapter.Inbound.
func (*Adapter) EncodeResponse(resp *adapter.Response) ([]byte, error) {
	var text strings.Builder
	var calls []toolCall
	for _, p := range resp.Content {
		switch p.Type {
		case adapter.PartText:
			text.WriteString(p.Text)
		case adapter.PartToolCall:
			calls = append(calls, encodeToolCall(p.ToolCall))
		}
	}
	msg := &message{Role: "assistant", ToolCalls: calls}
	if text.Len() > 0 || len(calls) == 0 {
		msg.Content = jsonString(text.String()) // null only for pure tool calls
	}
	id := resp.ID
	if id == "" {
		id = adapter.NewID("chatcmpl-")
	}
	finish := finishReason(resp.StopReason)
	return json.Marshal(completion{
		ID:      id,
		Object:  "chat.completion",
		Created: now().Unix(),
		Model:   resp.Model,
		Choices: []choice{{Message: msg, FinishReason: &finish}},
		Usage:   usageOf(resp.Usage),
	})
}

func encodeToolCall(tc *adapter.ToolCall) toolCall {
	args := tc.Arguments
	if args == "" {
		args = "{}"
	}
	return toolCall{ID: tc.ID, Type: "function", Function: function{Name: tc.Name, Arguments: args}}
}

// EncodeError implements adapter.Inbound.
func (*Adapter) EncodeError(status int, message string) []byte {
	return errorJSON(errorType(status), message)
}

func errorJSON(typ, message string) []byte {
	var body struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Param   *string `json:"param"`
			Code    *string `json:"code"`
		} `json:"error"`
	}
	body.Error.Message = message
	body.Error.Type = typ
	b, _ := json.Marshal(body)
	return b
}

func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	}
	return "invalid_request_error"
}

// NewStreamEncoder implements adapter.Inbound.
func (*Adapter) NewStreamEncoder(req *adapter.Request) adapter.StreamEncoder {
	return &streamEncoder{
		id:           adapter.NewID("chatcmpl-"),
		model:        req.Model,
		created:      now().Unix(),
		includeUsage: req.IncludeUsage,
	}
}

type streamEncoder struct {
	id           string
	model        string
	created      int64
	includeUsage bool
	started      bool // the role chunk was sent
	stopped      bool // the finish chunk was sent
	toolUsed     bool
	usage        adapter.Usage
}

func (e *streamEncoder) Encode(ev adapter.Event) []sse.Event {
	var out []sse.Event
	if !e.started {
		if ev.Type == adapter.EventStart {
			if ev.ID != "" {
				e.id = ev.ID
			}
			if ev.Model != "" {
				e.model = ev.Model
			}
		}
		out = append(out, e.roleChunk())
	}
	switch ev.Type {
	case adapter.EventText:
		if ev.Text != "" {
			out = append(out, e.chunk(&delta{Content: &ev.Text}, nil))
		}
	case adapter.EventToolStart:
		e.toolUsed = true
		out = append(out, e.toolChunk(ev.ToolIndex, toolCall{
			ID:       ev.ToolID,
			Type:     "function",
			Function: function{Name: ev.ToolName},
		}))
	case adapter.EventToolArgs:
		if ev.ArgsDelta != "" {
			out = append(out, e.toolChunk(ev.ToolIndex, toolCall{Function: function{Arguments: ev.ArgsDelta}}))
		}
	case adapter.EventStop:
		out = append(out, e.finishChunk(finishReason(ev.StopReason))...)
	case adapter.EventUsage:
		if ev.Usage != nil {
			e.usage = *ev.Usage
		}
	}
	return out
}

func (e *streamEncoder) Finish() []sse.Event {
	var out []sse.Event
	if !e.started {
		out = append(out, e.roleChunk())
	}
	reason := "stop"
	if e.toolUsed {
		reason = "tool_calls"
	}
	out = append(out, e.finishChunk(reason)...)
	if e.includeUsage {
		out = append(out, e.event(completion{Choices: []choice{}, Usage: usageOf(e.usage)}))
	}
	return append(out, sse.Event{Data: "[DONE]"})
}

func (e *streamEncoder) Error(message string) []sse.Event {
	return []sse.Event{{Data: string(errorJSON("api_error", message))}, {Data: "[DONE]"}}
}

func (e *streamEncoder) roleChunk() sse.Event {
	e.started = true
	empty := ""
	return e.chunk(&delta{Role: "assistant", Content: &empty}, nil)
}

func (e *streamEncoder) toolChunk(index int, tc toolCall) sse.Event {
	tc.Index = &index
	return e.chunk(&delta{ToolCalls: []toolCall{tc}}, nil)
}

// finishChunk returns the chunk carrying finish_reason, at most once.
func (e *streamEncoder) finishChunk(reason string) []sse.Event {
	if e.stopped {
		return nil
	}
	e.stopped = true
	return []sse.Event{e.chunk(&delta{}, &reason)}
}

func (e *streamEncoder) chunk(d *delta, finish *string) sse.Event {
	return e.event(completion{Choices: []choice{{Delta: d, FinishReason: finish}}})
}

// event fills in the fields shared by all chunks and renders c.
func (e *streamEncoder) event(c completion) sse.Event {
	c.ID, c.Object, c.Created, c.Model = e.id, "chat.completion.chunk", e.created, e.model
	b, _ := json.Marshal(c) // cannot fail: plain values only
	return sse.Event{Data: string(b)}
}
