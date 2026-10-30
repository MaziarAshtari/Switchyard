package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"
)

// maxBodyBytes bounds request and upstream response bodies held in memory.
const maxBodyBytes = 64 << 20

func isMultipart(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt == "multipart/form-data"
}

// requestModel returns the "model" field of a JSON or multipart body.
func requestModel(body []byte, contentType string) (string, error) {
	if isMultipart(contentType) {
		return multipartField(body, contentType, "model")
	}
	var v struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", errors.New("request body must be valid JSON")
	}
	return strings.TrimSpace(v.Model), nil
}

func multipartField(body []byte, contentType, name string) (string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return "", errors.New("invalid multipart Content-Type")
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", errors.New("invalid multipart body")
		}
		if part.FormName() == name && part.FileName() == "" {
			v, err := io.ReadAll(io.LimitReader(part, 1024))
			return strings.TrimSpace(string(v)), err
		}
	}
}

// withModel returns body with its "model" field set to model. JSON fields
// other than "model" are preserved verbatim; multipart parts are copied.
func withModel(body []byte, contentType, model string) ([]byte, string, error) {
	if isMultipart(contentType) {
		return multipartWithModel(body, contentType, model)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, "", errors.New("request body must be a JSON object")
	}
	m, _ := json.Marshal(model)
	fields["model"] = m
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, "", err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), "application/json", nil
}

func multipartWithModel(body []byte, contentType, model string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, "", errors.New("invalid multipart Content-Type")
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for {
		part, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", errors.New("invalid multipart body")
		}
		w, err := mw.CreatePart(part.Header)
		if err != nil {
			return nil, "", err
		}
		if part.FormName() == "model" && part.FileName() == "" {
			io.WriteString(w, model)
		} else if _, err := io.Copy(w, part); err != nil {
			return nil, "", errors.New("invalid multipart body")
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// usageOf reads OpenAI-style usage from a non-chat JSON response
// (embeddings: prompt_tokens; images/audio: input_tokens/output_tokens).
func usageOf(body []byte) (in, out *int64) {
	var v struct {
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			InputTokens      *int64 `json:"input_tokens"`
			OutputTokens     *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil || v.Usage == nil {
		return nil, nil
	}
	in, out = v.Usage.InputTokens, v.Usage.OutputTokens
	if in == nil {
		in = v.Usage.PromptTokens
	}
	if out == nil {
		out = v.Usage.CompletionTokens
	}
	return in, out
}

func isTextual(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt == "application/json" || strings.HasPrefix(mt, "text/") || strings.HasSuffix(mt, "+json")
}

// bodyForLog renders a body for the request log: text truncated to limit,
// binary content replaced by a short description.
func bodyForLog(body []byte, contentType string, limit int) *string {
	var s string
	switch {
	case len(body) == 0:
		return nil
	case !isTextual(contentType) || !utf8.Valid(body):
		mt, _, _ := mime.ParseMediaType(contentType)
		if mt == "" {
			mt = "binary"
		}
		s = fmt.Sprintf("[%s body, %d bytes]", mt, len(body))
	case len(body) > limit:
		s = truncateUTF8(string(body), limit) + fmt.Sprintf("\n[truncated, %d bytes total]", len(body))
	default:
		s = string(body)
	}
	return &s
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// captureWriter records what is written to the client: status, size and
// (when enabled) the first bytes of the body for the request log.
type captureWriter struct {
	http.ResponseWriter
	status  int
	size    int64
	capture bool
	limit   int
	buf     bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.capture && c.buf.Len() < c.limit+1 {
		c.buf.Write(p[:min(len(p), c.limit+1-c.buf.Len())])
	}
	n, err := c.ResponseWriter.Write(p)
	c.size += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (Flush).
func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *captureWriter) started() bool { return c.status != 0 }

func (c *captureWriter) logBody() *string {
	if !c.capture || c.size == 0 {
		return nil
	}
	ct := c.Header().Get("Content-Type")
	if !isTextual(ct) {
		s := fmt.Sprintf("[%s body, %d bytes]", ct, c.size)
		return &s
	}
	s := c.buf.String()
	if c.size > int64(c.limit) {
		s = truncateUTF8(s, c.limit) + fmt.Sprintf("\n[truncated, %d bytes total]", c.size)
	}
	return &s
}
