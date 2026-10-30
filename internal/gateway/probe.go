package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/adapter"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// TestKey checks a key by listing the provider's models through the key's proxy.
func (g *Gateway) TestKey(ctx context.Context, id int64) (proxy.TestResult, error) {
	k, err := g.store.GetKey(ctx, id)
	if err != nil {
		return proxy.TestResult{}, err
	}
	p, err := g.store.LookupProvider(ctx, k.ProviderID)
	if err != nil {
		return proxy.TestResult{}, err
	}
	secret, err := g.store.KeySecret(ctx, id)
	if err != nil {
		return proxy.TestResult{}, err
	}
	client, err := g.clientFor(ctx, p, k)
	if err != nil {
		return proxy.TestResult{Error: err.Error()}, nil
	}
	up := g.upstream[p.Type]
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, up.ModelsURL(p.BaseURL), nil)
	if err != nil {
		return proxy.TestResult{Error: err.Error()}, nil
	}
	g.setUpstreamHeaders(req, p, up, secret)
	start := time.Now()
	resp, err := client.Do(req)
	res := proxy.TestResult{LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	res.StatusCode = resp.StatusCode
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !res.OK {
		res.Error = strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, up.ErrorMessage(body)))
	}
	return res, nil
}

// ModelTestResult is the outcome of a model test request.
type ModelTestResult struct {
	OK         bool            `json:"ok"`
	StatusCode int             `json:"status_code"`
	LatencyMS  int64           `json:"latency_ms"`
	Output     string          `json:"output"`
	Error      string          `json:"error"`
	Attempts   []store.Attempt `json:"attempts"`
}

// ErrNotTestable is returned for model types that cannot be tested cheaply.
var ErrNotTestable = errors.New("testing video models is not supported: generation is slow and expensive")

// TestModel sends a small sample request through the full pipeline, exactly
// as a client request (it is logged too).
func (g *Gateway) TestModel(ctx context.Context, m store.Model) (ModelTestResult, error) {
	if m.Type == "video" {
		return ModelTestResult{}, ErrNotTestable
	}
	ep := endpointByType[m.Type]
	body, contentType := sampleRequest(m)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, ep.path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	entry := g.serve(rec, req, ep)
	res := ModelTestResult{
		StatusCode: rec.Code,
		LatencyMS:  entry.LatencyMS,
		Attempts:   entry.AttemptDetails,
		OK:         rec.Code >= 200 && rec.Code < 300,
	}
	if res.OK {
		res.Output = preview(m.Type, rec)
	} else {
		res.Error = g.upstream[string(adapter.OpenAI)].ErrorMessage(rec.Body.Bytes())
	}
	return res, nil
}

func sampleRequest(m store.Model) ([]byte, string) {
	var v any
	switch m.Type {
	case "chat":
		v = map[string]any{"model": m.Name, "messages": []map[string]string{
			{"role": "user", "content": "Reply with the single word: pong"}}}
	case "embedding":
		v = map[string]any{"model": m.Name, "input": "Switchyard test"}
	case "image":
		v = map[string]any{"model": m.Name, "prompt": "A small red circle on a white background", "n": 1, "size": "1024x1024"}
	case "tts":
		v = map[string]any{"model": m.Name, "input": "Switchyard test.", "voice": "alloy"}
	case "asr":
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		mw.WriteField("model", m.Name)
		fw, _ := mw.CreateFormFile("file", "silence.wav")
		fw.Write(silentWAV())
		mw.Close()
		return buf.Bytes(), mw.FormDataContentType()
	}
	b, _ := json.Marshal(v)
	return b, "application/json"
}

// silentWAV returns one second of 16 kHz mono 16-bit silence.
func silentWAV() []byte {
	const rate, dataLen = 16000, 16000 * 2
	b := make([]byte, 44+dataLen)
	le := binary.LittleEndian
	copy(b[0:], "RIFF")
	le.PutUint32(b[4:], 36+dataLen)
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16)     // fmt chunk size
	le.PutUint16(b[20:], 1)      // PCM
	le.PutUint16(b[22:], 1)      // mono
	le.PutUint32(b[24:], rate)   // sample rate
	le.PutUint32(b[28:], rate*2) // byte rate
	le.PutUint16(b[32:], 2)      // block align
	le.PutUint16(b[34:], 16)     // bits per sample
	copy(b[36:], "data")
	le.PutUint32(b[40:], dataLen)
	return b
}

func preview(modelType string, rec *httptest.ResponseRecorder) string {
	body := rec.Body.Bytes()
	switch modelType {
	case "chat":
		var v struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(body, &v) == nil && len(v.Choices) > 0 {
			return adapter.Truncate(v.Choices[0].Message.Content, 500)
		}
	case "embedding":
		var v struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &v) == nil && len(v.Data) > 0 {
			return fmt.Sprintf("Embedding with %d dimensions", len(v.Data[0].Embedding))
		}
	case "image":
		var v struct {
			Data []json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &v) == nil {
			return fmt.Sprintf("%d image(s) returned", len(v.Data))
		}
	case "tts":
		return fmt.Sprintf("Audio received: %s, %.1f KB", rec.Header().Get("Content-Type"), float64(len(body))/1024)
	case "asr":
		var v struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(body, &v) == nil {
			if v.Text == "" {
				return "(empty transcription of one second of silence)"
			}
			return adapter.Truncate(v.Text, 500)
		}
	}
	return adapter.Truncate(string(body), 500)
}
