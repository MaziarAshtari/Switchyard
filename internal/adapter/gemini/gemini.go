// Package gemini implements the Gemini generateContent wire format as a
// provider-facing protocol (adapter.Upstream).
package gemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/sse"
)

// Adapter converts between the generateContent format and the canonical
// adapter model. It is stateless; streams get their own decoder.
type Adapter struct{}

var _ adapter.Upstream = (*Adapter)(nil)

// New returns a Gemini adapter.
func New() *Adapter { return &Adapter{} }

// Format implements adapter.Upstream.
func (*Adapter) Format() adapter.Format { return adapter.Gemini }

// skipSignature is the documented placeholder for function calls whose real
// thought signature is unknown (it cannot survive a round trip through
// OpenAI or Anthropic clients).
const skipSignature = "skip_thought_signature_validator"

// emptySchema is sent for tools without a parameter schema.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// versionSuffix matches a base URL that already ends in an API version.
var versionSuffix = regexp.MustCompile(`/v\d+((alpha|beta)\d*)?$`)

// apiBase returns baseURL with the API version, adding /v1beta if missing.
func apiBase(baseURL string) string {
	if versionSuffix.MatchString(baseURL) {
		return baseURL
	}
	return baseURL + "/v1beta"
}

// ChatURL implements adapter.Upstream. baseURL is the host, e.g.
// https://generativelanguage.googleapis.com.
func (*Adapter) ChatURL(baseURL, model string, stream bool) string {
	u := apiBase(baseURL) + "/models/" + url.PathEscape(strings.TrimPrefix(model, "models/"))
	if stream {
		return u + ":streamGenerateContent?alt=sse"
	}
	return u + ":generateContent"
}

// ModelsURL implements adapter.Upstream.
func (*Adapter) ModelsURL(baseURL string) string { return apiBase(baseURL) + "/models" }

// SetAuth implements adapter.Upstream.
func (*Adapter) SetAuth(h http.Header, apiKey string) { h.Set("x-goog-api-key", apiKey) }

type request struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Tools             []toolSet         `json:"tools,omitempty"`
	ToolConfig        *toolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	InlineData       *blob             `json:"inlineData,omitempty"`
	FileData         *fileData         `json:"fileData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

type blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type fileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"` // only read from responses
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string            `json:"name"`
	Response map[string]string `json:"response"`
}

type toolSet struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
}

type toolConfig struct {
	FunctionCallingConfig functionCallingConfig `json:"functionCallingConfig"`
}

type functionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// EncodeRequest implements adapter.Upstream. The model is part of the URL,
// not of the body.
func (*Adapter) EncodeRequest(req *adapter.Request, _ string) ([]byte, error) {
	out := request{Contents: encodeContents(req.Messages)}
	if req.System != "" {
		out.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]functionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Parameters
			if isNull(schema) {
				schema = emptySchema
			}
			decls = append(decls, functionDeclaration{Name: t.Name, Description: t.Description, ParametersJSONSchema: schema})
		}
		out.Tools = []toolSet{{FunctionDeclarations: decls}}
	}
	if tc := req.ToolChoice; tc != nil && len(req.Tools) > 0 {
		out.ToolConfig = &toolConfig{FunctionCallingConfig: functionCallingMode(tc)}
	}
	gc := generationConfig{Temperature: req.Temperature, TopP: req.TopP, StopSequences: req.Stop}
	if req.MaxTokens > 0 {
		gc.MaxOutputTokens = req.MaxTokens
	}
	if gc.MaxOutputTokens > 0 || gc.Temperature != nil || gc.TopP != nil || len(gc.StopSequences) > 0 {
		out.GenerationConfig = &gc
	}
	return json.Marshal(out)
}

func functionCallingMode(tc *adapter.ToolChoice) functionCallingConfig {
	switch tc.Mode {
	case "none":
		return functionCallingConfig{Mode: "NONE"}
	case "required":
		return functionCallingConfig{Mode: "ANY"}
	case "tool":
		return functionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{tc.Name}}
	}
	return functionCallingConfig{Mode: "AUTO"}
}

// encodeContents converts the conversation. Consecutive messages with the
// same role are merged because Gemini expects alternating roles.
func encodeContents(msgs []adapter.Message) []content {
	out := []content{}
	names := map[string]string{} // tool call ID -> function name
	for _, m := range msgs {
		role := "user"
		if m.Role == adapter.RoleAssistant {
			role = "model"
		}
		var parts []part
		for _, p := range m.Parts {
			switch p.Type {
			case adapter.PartText:
				if p.Text != "" {
					parts = append(parts, part{Text: p.Text})
				}
			case adapter.PartImage:
				parts = append(parts, imagePart(p.Image))
			case adapter.PartToolCall:
				names[p.ToolCall.ID] = p.ToolCall.Name
				parts = append(parts, part{FunctionCall: &functionCall{
					Name: p.ToolCall.Name,
					Args: jsonObject(p.ToolCall.Arguments),
				}})
			case adapter.PartToolResult:
				parts = append(parts, part{FunctionResponse: functionResult(p.ToolResult, names)})
			}
		}
		if len(parts) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
		} else {
			out = append(out, content{Role: role, Parts: parts})
		}
	}
	for i := range out {
		if out[i].Role != "model" {
			continue
		}
		for j := range out[i].Parts {
			if out[i].Parts[j].FunctionCall != nil {
				out[i].Parts[j].ThoughtSignature = skipSignature
				break
			}
		}
	}
	return out
}

// functionResult converts a tool result. Gemini identifies the call by
// function name, looked up from the earlier tool call with the same ID.
func functionResult(r *adapter.ToolResult, names map[string]string) *functionResponse {
	name := names[r.CallID]
	if name == "" {
		name = r.Name
	}
	if name == "" {
		name = "tool"
	}
	key := "content"
	if r.IsError {
		key = "error"
	}
	return &functionResponse{Name: name, Response: map[string]string{key: r.Content}}
}

func imagePart(img *adapter.Image) part {
	if img.Data != "" {
		return part{InlineData: &blob{MimeType: img.MediaType, Data: img.Data}}
	}
	mimeType := img.MediaType
	if mimeType == "" {
		mimeType = guessImageType(img.URL)
	}
	return part{FileData: &fileData{MimeType: mimeType, FileURI: img.URL}}
}

func guessImageType(rawURL string) string {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		p = u.Path
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "image/jpeg"
}

type response struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string    `json:"modelVersion"`
	ResponseID   string    `json:"responseId"`
	Error        *apiError `json:"error"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *response) usage() *adapter.Usage {
	if r.UsageMetadata == nil {
		return nil
	}
	m := r.UsageMetadata
	return &adapter.Usage{InputTokens: m.PromptTokenCount, OutputTokens: m.CandidatesTokenCount + m.ThoughtsTokenCount}
}

func (r *response) blocked() bool {
	return len(r.Candidates) == 0 && r.PromptFeedback != nil && r.PromptFeedback.BlockReason != ""
}

// parseResponse decodes a response or stream chunk. An error object, also in
// the array form [{"error":{...}}], is returned as *adapter.Error.
func parseResponse(body []byte) (*response, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var list []response
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("gemini: invalid response: %w", err)
		}
		for _, r := range list {
			if r.Error != nil {
				return nil, bodyError(r.Error, body)
			}
		}
		return nil, errors.New("gemini: unexpected JSON array response")
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("gemini: invalid response: %w", err)
	}
	if r.Error != nil {
		return nil, bodyError(r.Error, body)
	}
	return &r, nil
}

func bodyError(e *apiError, body []byte) *adapter.Error {
	status := http.StatusBadGateway
	if e.Code >= 400 && e.Code <= 599 {
		status = e.Code
	}
	msg := e.Message
	if msg == "" {
		msg = rawMessage(body)
	}
	return &adapter.Error{Status: status, Message: msg}
}

// DecodeResponse implements adapter.Upstream.
func (*Adapter) DecodeResponse(body []byte) (*adapter.Response, error) {
	r, err := parseResponse(body)
	if err != nil {
		return nil, err
	}
	resp := &adapter.Response{ID: r.ResponseID, Model: r.ModelVersion}
	if u := r.usage(); u != nil {
		resp.Usage = *u
	}
	if r.blocked() {
		resp.StopReason = adapter.StopContentFilter
		return resp, nil
	}
	if len(r.Candidates) == 0 {
		return nil, errors.New("gemini: response has no candidates")
	}
	c := r.Candidates[0]
	toolCalled := false
	for _, p := range c.Content.Parts {
		switch {
		case p.Thought:
		case p.FunctionCall != nil:
			toolCalled = true
			resp.Content = append(resp.Content, adapter.Part{Type: adapter.PartToolCall, ToolCall: toolCall(p.FunctionCall)})
		case p.Text != "":
			resp.Content = append(resp.Content, adapter.Part{Type: adapter.PartText, Text: p.Text})
		}
	}
	resp.StopReason = stopReason(c.FinishReason, toolCalled)
	return resp, nil
}

func toolCall(fc *functionCall) *adapter.ToolCall {
	id := fc.ID
	if id == "" {
		id = adapter.NewID("call_")
	}
	return &adapter.ToolCall{ID: id, Name: fc.Name, Arguments: compactJSON(fc.Args)}
}

// stopReason maps a finishReason. Gemini reports STOP for function calls, so
// a reason that maps to end becomes tool_use when a tool was called.
func stopReason(reason string, toolCalled bool) adapter.StopReason {
	switch reason {
	case "MAX_TOKENS":
		return adapter.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return adapter.StopContentFilter
	}
	if toolCalled {
		return adapter.StopToolUse
	}
	return adapter.StopEnd
}

// NewStreamDecoder implements adapter.Upstream.
func (*Adapter) NewStreamDecoder() adapter.StreamDecoder { return &streamDecoder{} }

// streamDecoder decodes streamGenerateContent?alt=sse, where every event is a
// complete response chunk. The stream has no end marker, so Decode never
// returns io.EOF.
type streamDecoder struct {
	started bool
	stopped bool
	tools   int // tool calls emitted so far
}

func (d *streamDecoder) Decode(ev sse.Event) ([]adapter.Event, error) {
	data := strings.TrimSpace(ev.Data)
	if data == "" || data == "[DONE]" {
		return nil, nil
	}
	r, err := parseResponse([]byte(data))
	if err != nil {
		return nil, err
	}
	var out []adapter.Event
	if !d.started {
		d.started = true
		out = append(out, adapter.Event{Type: adapter.EventStart, ID: r.ResponseID, Model: r.ModelVersion})
	}
	finish := ""
	if len(r.Candidates) > 0 {
		c := r.Candidates[0]
		finish = c.FinishReason
		for _, p := range c.Content.Parts {
			switch {
			case p.Thought:
			case p.FunctionCall != nil:
				tc := toolCall(p.FunctionCall)
				out = append(out,
					adapter.Event{Type: adapter.EventToolStart, ToolIndex: d.tools, ToolID: tc.ID, ToolName: tc.Name},
					adapter.Event{Type: adapter.EventToolArgs, ToolIndex: d.tools, ArgsDelta: tc.Arguments},
				)
				d.tools++
			case p.Text != "":
				out = append(out, adapter.Event{Type: adapter.EventText, Text: p.Text})
			}
		}
	}
	if !d.stopped && (finish != "" || r.blocked()) {
		d.stopped = true
		reason := adapter.StopContentFilter
		if !r.blocked() {
			reason = stopReason(finish, d.tools > 0)
		}
		out = append(out, adapter.Event{Type: adapter.EventStop, StopReason: reason})
	}
	if u := r.usage(); u != nil {
		out = append(out, adapter.Event{Type: adapter.EventUsage, Usage: u})
	}
	return out, nil
}

// ErrorMessage implements adapter.Upstream. It understands
// {"error":{"message":...}} and [{"error":{"message":...}}].
func (*Adapter) ErrorMessage(body []byte) string {
	var e *adapter.Error
	if _, err := parseResponse(body); errors.As(err, &e) {
		return e.Message
	}
	return rawMessage(body)
}

func rawMessage(body []byte) string {
	return adapter.Truncate(strings.TrimSpace(string(body)), 500)
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
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
