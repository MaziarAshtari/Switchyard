package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// emptySchema is sent for tools without a parameter schema.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// ChatURL implements adapter.Upstream. baseURL includes the version, e.g.
// https://api.openai.com/v1.
func (*Adapter) ChatURL(baseURL, _ string, _ bool) string { return baseURL + "/chat/completions" }

// ModelsURL implements adapter.Upstream.
func (*Adapter) ModelsURL(baseURL string) string { return baseURL + "/models" }

// SetAuth implements adapter.Upstream.
func (*Adapter) SetAuth(h http.Header, apiKey string) { h.Set("Authorization", "Bearer "+apiKey) }

// EncodeRequest implements adapter.Upstream.
func (*Adapter) EncodeRequest(req *adapter.Request, model string) ([]byte, error) {
	out := chatRequest{
		Model:       model,
		Messages:    encodeMessages(req),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
		Stream:      req.Stream,
	}
	if req.MaxTokens > 0 {
		out.MaxCompletionTokens = req.MaxTokens
	}
	if req.Stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if isNull(params) {
			params = emptySchema
		}
		out.Tools = append(out.Tools, tool{Type: "function", Function: toolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		}})
	}
	// OpenAI rejects tool_choice without tools (e.g. when only untranslatable
	// server tools were requested).
	if req.ToolChoice != nil && len(out.Tools) > 0 {
		out.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	return json.Marshal(out)
}

func encodeMessages(req *adapter.Request) []message {
	out := []message{}
	if req.System != "" {
		out = append(out, message{Role: "system", Content: jsonString(req.System)})
	}
	for _, m := range req.Messages {
		if m.Role == adapter.RoleAssistant {
			if msg, ok := encodeAssistant(m); ok {
				out = append(out, msg)
			}
			continue
		}
		// Tool results become tool messages, which must directly follow the
		// assistant message with the calls; the rest forms one user message.
		var parts []contentPart
		for _, p := range m.Parts {
			switch p.Type {
			case adapter.PartToolResult:
				out = append(out, message{
					Role:       "tool",
					ToolCallID: p.ToolResult.CallID,
					Content:    jsonString(p.ToolResult.Content),
				})
			case adapter.PartText:
				if p.Text != "" {
					parts = append(parts, contentPart{Type: "text", Text: p.Text})
				}
			case adapter.PartImage:
				parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: imageURLOf(p.Image)}})
			}
		}
		switch {
		case len(parts) == 1 && parts[0].Type == "text":
			out = append(out, message{Role: "user", Content: jsonString(parts[0].Text)})
		case len(parts) > 0:
			content, _ := json.Marshal(parts) // cannot fail: plain values only
			out = append(out, message{Role: "user", Content: content})
		}
	}
	return out
}

func encodeAssistant(m adapter.Message) (message, bool) {
	var text strings.Builder
	var calls []toolCall
	for _, p := range m.Parts {
		switch p.Type {
		case adapter.PartText:
			text.WriteString(p.Text)
		case adapter.PartToolCall:
			calls = append(calls, encodeToolCall(p.ToolCall))
		}
	}
	if text.Len() == 0 && len(calls) == 0 {
		return message{}, false
	}
	msg := message{Role: "assistant", ToolCalls: calls}
	if text.Len() > 0 {
		msg.Content = jsonString(text.String())
	}
	return msg, true
}

func encodeToolChoice(tc *adapter.ToolChoice) json.RawMessage {
	switch tc.Mode {
	case "none", "required":
		return jsonString(tc.Mode)
	case "tool":
		b, _ := json.Marshal(tool{Type: "function", Function: toolFunction{Name: tc.Name}})
		return b
	}
	return jsonString("auto")
}

// responseBody is a chat.completion response or a stream chunk.
type responseBody struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      message `json:"message"`
		Delta        message `json:"delta"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage          `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// DecodeResponse implements adapter.Upstream.
func (*Adapter) DecodeResponse(body []byte) (*adapter.Response, error) {
	var in responseBody
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("openai: invalid response: %w", err)
	}
	if e := bodyError(in.Error, body); e != nil {
		return nil, e
	}
	if len(in.Choices) == 0 {
		return nil, errors.New("openai: response has no choices")
	}
	c := in.Choices[0]
	resp := &adapter.Response{ID: in.ID, Model: in.Model, StopReason: stopReason(c.FinishReason)}
	parts, _ := parseContent(c.Message.Content)
	if text := joinText(parts, ""); text != "" {
		resp.Content = append(resp.Content, adapter.Part{Type: adapter.PartText, Text: text})
	}
	for _, tc := range c.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = adapter.NewID("call_")
		}
		resp.Content = append(resp.Content, toolCallPart(id, tc.Function))
	}
	// Some compatible servers report "stop" together with tool calls.
	if len(c.Message.ToolCalls) > 0 && resp.StopReason == adapter.StopEnd {
		resp.StopReason = adapter.StopToolUse
	}
	if in.Usage != nil {
		resp.Usage = adapter.Usage{InputTokens: in.Usage.PromptTokens, OutputTokens: in.Usage.CompletionTokens}
	}
	return resp, nil
}

// NewStreamDecoder implements adapter.Upstream.
func (*Adapter) NewStreamDecoder() adapter.StreamDecoder {
	return &streamDecoder{tools: map[int]streamTool{}}
}

type streamDecoder struct {
	started bool
	stopped bool
	tools   map[int]streamTool // by upstream tool call index
	next    int                // next canonical ToolIndex
}

type streamTool struct {
	index      int    // canonical ToolIndex
	upstreamID string // ID sent by the upstream (may be empty)
}

func (d *streamDecoder) Decode(ev sse.Event) ([]adapter.Event, error) {
	data := strings.TrimSpace(ev.Data)
	switch data {
	case "":
		return nil, nil
	case "[DONE]":
		return nil, io.EOF
	}
	var chunk responseBody
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("openai: invalid stream chunk: %w", err)
	}
	if e := bodyError(chunk.Error, []byte(data)); e != nil {
		return nil, e
	}
	var out []adapter.Event
	if !d.started {
		d.started = true
		out = append(out, adapter.Event{Type: adapter.EventStart, ID: chunk.ID, Model: chunk.Model})
	}
	if len(chunk.Choices) > 0 {
		c := chunk.Choices[0]
		parts, _ := parseContent(c.Delta.Content)
		if text := joinText(parts, ""); text != "" {
			out = append(out, adapter.Event{Type: adapter.EventText, Text: text})
		}
		for pos, tc := range c.Delta.ToolCalls {
			out = append(out, d.toolEvents(pos, tc)...)
		}
		if c.FinishReason != "" && !d.stopped {
			d.stopped = true
			reason := stopReason(c.FinishReason)
			if reason == adapter.StopEnd && d.next > 0 {
				reason = adapter.StopToolUse
			}
			out = append(out, adapter.Event{Type: adapter.EventStop, StopReason: reason})
		}
	}
	if chunk.Usage != nil {
		out = append(out, adapter.Event{Type: adapter.EventUsage, Usage: &adapter.Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
		}})
	}
	return out, nil
}

// toolEvents converts one tool call delta. pos is its position in the delta
// array, used when the upstream omits "index".
func (d *streamDecoder) toolEvents(pos int, tc toolCall) []adapter.Event {
	key := pos
	if tc.Index != nil {
		key = *tc.Index
	}
	var out []adapter.Event
	t, ok := d.tools[key]
	// A different ID under a known index means a new call (some servers send
	// every call with the same index).
	if !ok || (tc.ID != "" && t.upstreamID != "" && tc.ID != t.upstreamID) {
		t = streamTool{index: d.next, upstreamID: tc.ID}
		d.tools[key] = t
		d.next++
		id := tc.ID
		if id == "" {
			id = adapter.NewID("call_")
		}
		out = append(out, adapter.Event{Type: adapter.EventToolStart, ToolIndex: t.index, ToolID: id, ToolName: tc.Function.Name})
	}
	if tc.Function.Arguments != "" {
		out = append(out, adapter.Event{Type: adapter.EventToolArgs, ToolIndex: t.index, ArgsDelta: tc.Function.Arguments})
	}
	return out
}
