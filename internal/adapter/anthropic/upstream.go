package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// defaultMaxTokens is used when the client sent no limit; Anthropic requires one.
const defaultMaxTokens = 8192

// emptySchema is sent for tools without a parameter schema.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// ChatURL implements adapter.Upstream. baseURL is the host, e.g.
// https://api.anthropic.com.
func (*Adapter) ChatURL(baseURL, _ string, _ bool) string { return baseURL + "/v1/messages" }

// ModelsURL implements adapter.Upstream.
func (*Adapter) ModelsURL(baseURL string) string { return baseURL + "/v1/models" }

// SetAuth implements adapter.Upstream. A client's anthropic-version header,
// already copied by the gateway, is kept.
func (*Adapter) SetAuth(h http.Header, apiKey string) {
	h.Set("x-api-key", apiKey)
	if h.Get("anthropic-version") == "" {
		h.Set("anthropic-version", "2023-06-01")
	}
}

// EncodeRequest implements adapter.Upstream.
func (*Adapter) EncodeRequest(req *adapter.Request, model string) ([]byte, error) {
	out := messagesRequest{
		Model:         model,
		MaxTokens:     req.MaxTokens,
		Messages:      encodeMessages(req.Messages),
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        req.Stream,
	}
	if out.MaxTokens <= 0 {
		out.MaxTokens = defaultMaxTokens
	}
	if req.System != "" {
		out.System = jsonString(req.System)
	}
	if req.Temperature != nil {
		// Anthropic accepts 0..1 (OpenAI allows up to 2), and newer models
		// reject temperature together with top_p.
		t := min(*req.Temperature, 1)
		out.Temperature, out.TopP = &t, nil
	}
	for _, t := range req.Tools {
		schema := t.Parameters
		if isNull(schema) {
			schema = emptySchema
		}
		out.Tools = append(out.Tools, tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	// tool_choice is rejected without tools (e.g. when only untranslatable
	// tools were requested).
	if tc := req.ToolChoice; tc != nil && len(out.Tools) > 0 {
		out.ToolChoice = encodeToolChoice(tc)
	}
	return json.Marshal(out)
}

func encodeToolChoice(tc *adapter.ToolChoice) *toolChoice {
	switch tc.Mode {
	case "none":
		return &toolChoice{Type: "none"}
	case "required":
		return &toolChoice{Type: "any"}
	case "tool":
		return &toolChoice{Type: "tool", Name: tc.Name}
	}
	return &toolChoice{Type: "auto"}
}

// encodeMessages converts the conversation. Consecutive messages with the
// same role are merged and tool results lead their user message, as the API
// requires.
func encodeMessages(msgs []adapter.Message) []message {
	out := []message{}
	var ids toolIDs
	for _, m := range msgs {
		role := "user"
		if m.Role == adapter.RoleAssistant {
			role = "assistant"
		}
		var blocks content
		for _, p := range m.Parts {
			switch p.Type {
			case adapter.PartText:
				if p.Text != "" {
					blocks = append(blocks, block{Type: "text", Text: p.Text})
				}
			case adapter.PartImage:
				blocks = append(blocks, imageBlock(p.Image))
			case adapter.PartToolCall:
				blocks = append(blocks, block{
					Type:  "tool_use",
					ID:    ids.call(p.ToolCall.ID),
					Name:  p.ToolCall.Name,
					Input: jsonObject(p.ToolCall.Arguments),
				})
			case adapter.PartToolResult:
				b := block{Type: "tool_result", ToolUseID: ids.result(p.ToolResult.CallID), IsError: p.ToolResult.IsError}
				if p.ToolResult.Content != "" {
					b.Content = jsonString(p.ToolResult.Content)
				}
				blocks = append(blocks, b)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
		} else {
			out = append(out, message{Role: role, Content: blocks})
		}
	}
	for i := range out {
		if out[i].Role == "user" {
			out[i].Content = toolResultsFirst(out[i].Content)
		}
	}
	return out
}

func imageBlock(img *adapter.Image) block {
	if img.Data != "" {
		return block{Type: "image", Source: &source{Type: "base64", MediaType: img.MediaType, Data: img.Data}}
	}
	return block{Type: "image", Source: &source{Type: "url", URL: img.URL}}
}

func toolResultsFirst(blocks content) content {
	var results, rest content
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results = append(results, b)
		} else {
			rest = append(rest, b)
		}
	}
	return append(results, rest...)
}

// toolIDs makes tool call IDs acceptable to Anthropic ([A-Za-z0-9_-]+). An
// empty ID becomes "toolu_<n>", counting calls and results without ID
// separately, so the n-th such call pairs with the n-th such result.
type toolIDs struct{ calls, results int }

func (t *toolIDs) call(id string) string { return sanitizeID(id, &t.calls) }

func (t *toolIDs) result(id string) string { return sanitizeID(id, &t.results) }

func sanitizeID(id string, empty *int) string {
	if id == "" {
		id = "toolu_" + strconv.Itoa(*empty)
		*empty++
		return id
	}
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
			return r
		}
		return '_'
	}, id)
}

// responseBody is a Messages response, an error body, or the message of
// message_start.
type responseBody struct {
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	Model      string    `json:"model"`
	Content    []block   `json:"content"`
	StopReason string    `json:"stop_reason"`
	Usage      *usage    `json:"usage"`
	Error      *apiError `json:"error"`
}

// DecodeResponse implements adapter.Upstream.
func (*Adapter) DecodeResponse(body []byte) (*adapter.Response, error) {
	var in responseBody
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("anthropic: invalid response: %w", err)
	}
	if in.Type == "error" || in.Error != nil {
		return nil, bodyError(in.Error, body)
	}
	var totals usageTotals
	resp := &adapter.Response{
		ID:         in.ID,
		Model:      in.Model,
		StopReason: stopReason(in.StopReason),
		Usage:      totals.update(in.Usage),
	}
	for _, b := range in.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				resp.Content = append(resp.Content, adapter.Part{Type: adapter.PartText, Text: b.Text})
			}
		case "tool_use":
			resp.Content = append(resp.Content, adapter.Part{Type: adapter.PartToolCall, ToolCall: &adapter.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: compactJSON(b.Input),
			}})
		}
	}
	return resp, nil
}

// NewStreamDecoder implements adapter.Upstream.
func (*Adapter) NewStreamDecoder() adapter.StreamDecoder {
	return &streamDecoder{tools: map[int]int{}}
}

type streamDecoder struct {
	started bool
	tools   map[int]int // content block index -> ToolIndex
	usage   usageTotals
}

type streamEvent struct {
	Type         string       `json:"type"`
	Message      responseBody `json:"message"`
	Index        int          `json:"index"`
	ContentBlock block        `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *usage    `json:"usage"`
	Error *apiError `json:"error"`
}

func (d *streamDecoder) Decode(ev sse.Event) ([]adapter.Event, error) {
	data := strings.TrimSpace(ev.Data)
	switch data {
	case "":
		return nil, nil
	case "[DONE]": // not sent by Anthropic, but by some compatible servers
		return nil, io.EOF
	}
	var se streamEvent
	if err := json.Unmarshal([]byte(data), &se); err != nil {
		return nil, fmt.Errorf("anthropic: invalid stream event: %w", err)
	}
	if se.Type == "" {
		se.Type = ev.Name
	}
	switch se.Type {
	case "message_start":
		var out []adapter.Event
		if !d.started {
			d.started = true
			out = append(out, adapter.Event{Type: adapter.EventStart, ID: se.Message.ID, Model: se.Message.Model})
		}
		if se.Message.Usage != nil {
			u := d.usage.update(se.Message.Usage)
			out = append(out, adapter.Event{Type: adapter.EventUsage, Usage: &u})
		}
		return out, nil
	case "content_block_start":
		switch cb := se.ContentBlock; cb.Type {
		case "text":
			if cb.Text != "" {
				return []adapter.Event{{Type: adapter.EventText, Text: cb.Text}}, nil
			}
		case "tool_use":
			index := len(d.tools)
			d.tools[se.Index] = index
			id := cb.ID
			if id == "" {
				id = adapter.NewID("toolu_")
			}
			return []adapter.Event{{Type: adapter.EventToolStart, ToolIndex: index, ToolID: id, ToolName: cb.Name}}, nil
		}
	case "content_block_delta":
		switch se.Delta.Type {
		case "text_delta":
			if se.Delta.Text != "" {
				return []adapter.Event{{Type: adapter.EventText, Text: se.Delta.Text}}, nil
			}
		case "input_json_delta":
			if index, ok := d.tools[se.Index]; ok && se.Delta.PartialJSON != "" {
				return []adapter.Event{{Type: adapter.EventToolArgs, ToolIndex: index, ArgsDelta: se.Delta.PartialJSON}}, nil
			}
		}
	case "message_delta":
		var out []adapter.Event
		if se.Delta.StopReason != "" {
			out = append(out, adapter.Event{Type: adapter.EventStop, StopReason: stopReason(se.Delta.StopReason)})
		}
		if se.Usage != nil {
			u := d.usage.update(se.Usage)
			out = append(out, adapter.Event{Type: adapter.EventUsage, Usage: &u})
		}
		return out, nil
	case "message_stop":
		return nil, io.EOF
	case "error":
		return nil, bodyError(se.Error, []byte(data))
	}
	return nil, nil // ping, content_block_stop, thinking and server tool blocks
}
