package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// DecodeRequest implements adapter.Inbound.
func (*Adapter) DecodeRequest(body []byte) (*adapter.Request, error) {
	var in messagesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, invalidRequest(err)
	}
	if in.Messages == nil {
		return nil, errors.New("messages must be an array")
	}
	system, err := flattenText(in.System, "\n\n")
	if err != nil {
		return nil, fmt.Errorf("system: %w", err)
	}
	req := &adapter.Request{
		Model:       in.Model,
		System:      system,
		MaxTokens:   in.MaxTokens,
		Temperature: in.Temperature,
		TopP:        in.TopP,
		Stop:        in.StopSequences,
		Stream:      in.Stream,
		ToolChoice:  decodeToolChoice(in.ToolChoice),
	}
	for _, t := range in.Tools {
		// Server tools (web search, code execution, ...) have no input schema
		// and cannot be translated.
		if !isNull(t.InputSchema) {
			req.Tools = append(req.Tools, adapter.Tool{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
		}
	}
	for i, m := range in.Messages {
		role := adapter.RoleUser
		if m.Role == "assistant" {
			role = adapter.RoleAssistant
		}
		parts, err := decodeBlocks(m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		req.Messages = append(req.Messages, adapter.Message{Role: role, Parts: parts})
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

// decodeBlocks converts message blocks. Thinking and unknown blocks are
// dropped; documents become a placeholder.
func decodeBlocks(blocks content) ([]adapter.Part, error) {
	var parts []adapter.Part
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, adapter.Part{Type: adapter.PartText, Text: b.Text})
			}
		case "image":
			if img := decodeImage(b.Source); img != nil {
				parts = append(parts, adapter.Part{Type: adapter.PartImage, Image: img})
			}
		case "document":
			parts = append(parts, adapter.Part{Type: adapter.PartText, Text: "[document omitted]"})
		case "tool_use":
			parts = append(parts, adapter.Part{Type: adapter.PartToolCall, ToolCall: &adapter.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: compactJSON(b.Input),
			}})
		case "tool_result":
			text, err := flattenText(b.Content, "\n")
			if err != nil {
				return nil, fmt.Errorf("tool_result content: %w", err)
			}
			parts = append(parts, adapter.Part{Type: adapter.PartToolResult, ToolResult: &adapter.ToolResult{
				CallID:  b.ToolUseID,
				Content: text,
				IsError: b.IsError,
			}})
		}
	}
	return parts, nil
}

func decodeImage(s *source) *adapter.Image {
	switch {
	case s == nil:
		return nil
	case s.Type == "base64":
		return &adapter.Image{MediaType: s.MediaType, Data: s.Data}
	case s.Type == "url":
		return &adapter.Image{URL: s.URL}
	}
	return nil
}

// flattenText turns a string or an array of blocks into text joined by sep.
// Images and documents become placeholders.
func flattenText(raw json.RawMessage, sep string) (string, error) {
	if isNull(raw) {
		return "", nil
	}
	var blocks content
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", errors.New("must be a string or an array of content blocks")
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			texts = append(texts, "[image omitted]")
		case "document":
			texts = append(texts, "[document omitted]")
		}
	}
	return strings.Join(texts, sep), nil
}

func decodeToolChoice(tc *toolChoice) *adapter.ToolChoice {
	if tc == nil {
		return nil
	}
	switch tc.Type {
	case "auto", "none":
		return &adapter.ToolChoice{Mode: tc.Type}
	case "any":
		return &adapter.ToolChoice{Mode: "required"}
	case "tool":
		return &adapter.ToolChoice{Mode: "tool", Name: tc.Name}
	}
	return nil
}

// messageResponse is a complete message, also used in message_start.
type messageResponse struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Role         string   `json:"role"`
	Model        string   `json:"model"`
	Content      []any    `json:"content"`
	StopReason   *string  `json:"stop_reason"`
	StopSequence *string  `json:"stop_sequence"`
	Usage        outUsage `json:"usage"`
}

// textBlock is a text block, or a text_delta in streams.
type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// EncodeResponse implements adapter.Inbound.
func (*Adapter) EncodeResponse(resp *adapter.Response) ([]byte, error) {
	blocks := []any{}
	toolUsed := false
	for _, p := range resp.Content {
		switch {
		case p.Type == adapter.PartText && p.Text != "":
			blocks = append(blocks, textBlock{Type: "text", Text: p.Text})
		case p.Type == adapter.PartToolCall:
			toolUsed = true
			blocks = append(blocks, toolUseBlock{
				Type:  "tool_use",
				ID:    p.ToolCall.ID,
				Name:  p.ToolCall.Name,
				Input: jsonObject(p.ToolCall.Arguments),
			})
		}
	}
	id := resp.ID
	if id == "" {
		id = adapter.NewID("msg_")
	}
	stop := finalStopReason(resp.StopReason, toolUsed)
	return json.Marshal(messageResponse{
		ID:         id,
		Type:       "message",
		Role:       "assistant",
		Model:      resp.Model,
		Content:    blocks,
		StopReason: &stop,
		Usage:      outUsage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens},
	})
}

// finalStopReason maps r; when it is unknown, a tool call implies tool_use.
func finalStopReason(r adapter.StopReason, toolUsed bool) string {
	if r == "" && toolUsed {
		return "tool_use"
	}
	return stopReasonString(r)
}

// EncodeError implements adapter.Inbound.
func (*Adapter) EncodeError(status int, message string) []byte {
	b, _ := json.Marshal(errorResponse{Type: "error", Error: apiError{Type: errorType(status), Message: message}})
	return b
}

// NewStreamEncoder implements adapter.Inbound.
func (*Adapter) NewStreamEncoder(req *adapter.Request) adapter.StreamEncoder {
	return &streamEncoder{id: adapter.NewID("msg_"), model: req.Model, tools: map[int]int{}}
}

type streamEncoder struct {
	id      string
	model   string
	started bool        // message_start was sent
	open    string      // type of the open content block, "" if none
	next    int         // index of the next content block
	tools   map[int]int // ToolIndex -> content block index
	stop    adapter.StopReason
	usage   adapter.Usage
}

type blockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type blockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type blockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type jsonDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

func (e *streamEncoder) Encode(ev adapter.Event) []sse.Event {
	switch ev.Type {
	case adapter.EventStart:
		if !e.started {
			if ev.ID != "" {
				e.id = ev.ID
			}
			if ev.Model != "" {
				e.model = ev.Model
			}
		}
	case adapter.EventUsage:
		if ev.Usage != nil {
			e.usage = *ev.Usage
		}
	case adapter.EventStop:
		e.stop = ev.StopReason
	}
	out := e.start()
	switch ev.Type {
	case adapter.EventText:
		if ev.Text == "" {
			break
		}
		if e.open != "text" {
			out = append(out, e.openBlock("text", textBlock{Type: "text"})...)
		}
		out = append(out, newEvent("content_block_delta", blockDelta{
			Type:  "content_block_delta",
			Index: e.next - 1,
			Delta: textBlock{Type: "text_delta", Text: ev.Text},
		}))
	case adapter.EventToolStart:
		e.tools[ev.ToolIndex] = e.next
		out = append(out, e.openBlock("tool_use", toolUseBlock{
			Type:  "tool_use",
			ID:    ev.ToolID,
			Name:  ev.ToolName,
			Input: json.RawMessage("{}"),
		})...)
	case adapter.EventToolArgs:
		if index, ok := e.tools[ev.ToolIndex]; ok && ev.ArgsDelta != "" {
			out = append(out, newEvent("content_block_delta", blockDelta{
				Type:  "content_block_delta",
				Index: index,
				Delta: jsonDelta{Type: "input_json_delta", PartialJSON: ev.ArgsDelta},
			}))
		}
	}
	return out
}

func (e *streamEncoder) Finish() []sse.Event {
	out := append(e.start(), e.closeBlock()...)
	var md struct {
		Type  string `json:"type"`
		Delta struct {
			StopReason   string  `json:"stop_reason"`
			StopSequence *string `json:"stop_sequence"`
		} `json:"delta"`
		Usage outUsage `json:"usage"`
	}
	md.Type = "message_delta"
	md.Delta.StopReason = finalStopReason(e.stop, len(e.tools) > 0)
	md.Usage = outUsage{InputTokens: e.usage.InputTokens, OutputTokens: e.usage.OutputTokens}
	return append(out,
		newEvent("message_delta", md),
		newEvent("message_stop", struct {
			Type string `json:"type"`
		}{"message_stop"}),
	)
}

func (e *streamEncoder) Error(message string) []sse.Event {
	return append(e.start(), newEvent("error", errorResponse{Type: "error", Error: apiError{Type: "api_error", Message: message}}))
}

// start returns message_start the first time it is called.
func (e *streamEncoder) start() []sse.Event {
	if e.started {
		return nil
	}
	e.started = true
	msg := messageResponse{
		ID:      e.id,
		Type:    "message",
		Role:    "assistant",
		Model:   e.model,
		Content: []any{},
		Usage:   outUsage{InputTokens: e.usage.InputTokens},
	}
	return []sse.Event{newEvent("message_start", struct {
		Type    string          `json:"type"`
		Message messageResponse `json:"message"`
	}{"message_start", msg})}
}

// openBlock closes the open block and starts block b of type typ.
func (e *streamEncoder) openBlock(typ string, b any) []sse.Event {
	out := e.closeBlock()
	out = append(out, newEvent("content_block_start", blockStart{Type: "content_block_start", Index: e.next, ContentBlock: b}))
	e.open = typ
	e.next++
	return out
}

func (e *streamEncoder) closeBlock() []sse.Event {
	if e.open == "" {
		return nil
	}
	e.open = ""
	return []sse.Event{newEvent("content_block_stop", blockStop{Type: "content_block_stop", Index: e.next - 1})}
}

// newEvent renders a named SSE event. name must equal the payload's "type".
func newEvent(name string, payload any) sse.Event {
	b, _ := json.Marshal(payload) // cannot fail: plain values and valid raw JSON only
	return sse.Event{Name: name, Data: string(b)}
}
