package knowledge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedding providers.
const (
	ProviderOllama = "ollama"
	ProviderOpenAI = "openai-compatible"
)

// Defaults of the embedding settings.
const (
	DefaultEndpoint = "http://localhost:11434"
	// DefaultModel is the text-only 270M EmbeddingGemma 2 (the multi
	// platform tag; Ollama picks q8_0 or the MLX build).
	DefaultModel = "embeddinggemma-2:270m"
)

// ErrModelMissing means the embedding model is not available on the
// server (Ollama: not pulled).
var ErrModelMissing = errors.New("embedding model not available")

// Embedder turns texts into vectors.
type Embedder interface {
	// Embed returns one vector per text (in order).
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model names the model (stored with the vectors).
	Model() string
}

// EmbedConfig configures NewEmbedder.
type EmbedConfig struct {
	// Provider is ollama or openai-compatible.
	Provider string
	// Endpoint is the base URL (Ollama: http://localhost:11434; OpenAI
	// compatible: https://api.example.com or …/v1).
	Endpoint string
	Model    string
	// APIKey is sent as bearer token (OpenAI compatible).
	APIKey string
	// BatchSize is the number of texts per request (default 16).
	BatchSize int
	// Timeout bounds one request (default 60s); Retries counts retries
	// of failed requests (default 2).
	Timeout time.Duration
	Retries int
	// Client replaces http.DefaultClient (tests).
	Client *http.Client
}

// HTTPEmbedder implements Embedder for Ollama and OpenAI compatible
// servers.
type HTTPEmbedder struct {
	cfg EmbedConfig
}

// NewEmbedder returns the embedder of cfg.
func NewEmbedder(cfg EmbedConfig) (*HTTPEmbedder, error) {
	if cfg.Provider == "" {
		cfg.Provider = ProviderOllama
	}
	if cfg.Provider != ProviderOllama && cfg.Provider != ProviderOpenAI {
		return nil, fmt.Errorf("unknown embedding provider %q", cfg.Provider)
	}
	if cfg.Endpoint == "" {
		if cfg.Provider != ProviderOllama {
			return nil, errors.New("the embedding endpoint is not set")
		}
		cfg.Endpoint = DefaultEndpoint
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 16
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	} else if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &HTTPEmbedder{cfg: cfg}, nil
}

// Model implements Embedder.
func (e *HTTPEmbedder) Model() string { return e.cfg.Provider + ":" + e.cfg.Model }

// Config returns the effective configuration.
func (e *HTTPEmbedder) Config() EmbedConfig { return e.cfg }

// Embed implements Embedder: texts go in batches, failed requests are
// retried with backoff (not on client errors).
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += e.cfg.BatchSize {
		batch := texts[i:min(i+e.cfg.BatchSize, len(texts))]
		var vs [][]float32
		var err error
		for attempt := 0; ; attempt++ {
			vs, err = e.embedBatch(ctx, batch)
			if err == nil || attempt >= e.cfg.Retries || !retryable(err) || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(200<<attempt) * time.Millisecond):
			}
		}
		if err != nil {
			return nil, err
		}
		if len(vs) != len(batch) {
			return nil, fmt.Errorf("embedding: %d vectors for %d texts", len(vs), len(batch))
		}
		out = append(out, vs...)
	}
	return out, nil
}

// httpError is a non-2xx answer.
type httpError struct {
	status int
	msg    string
}

func (h *httpError) Error() string {
	return fmt.Sprintf("embedding server: HTTP %d: %s", h.status, h.msg)
}

func retryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.status >= 500 || he.status == http.StatusTooManyRequests
	}
	return !errors.Is(err, ErrModelMissing)
}

func (e *HTTPEmbedder) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	if e.cfg.Provider == ProviderOllama {
		var res struct {
			Embeddings [][]float32 `json:"embeddings"`
		}
		if err := e.post(ctx, "/api/embed", map[string]any{"model": e.cfg.Model, "input": batch}, &res); err != nil {
			return nil, err
		}
		return res.Embeddings, nil
	}
	var res struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := e.post(ctx, openAIPath(e.cfg.Endpoint), map[string]any{"model": e.cfg.Model, "input": batch}, &res); err != nil {
		return nil, err
	}
	out := make([][]float32, len(res.Data))
	for i, d := range res.Data {
		idx := d.Index
		if idx < 0 || idx >= len(out) || out[idx] != nil {
			idx = i
		}
		out[idx] = d.Embedding
	}
	return out, nil
}

func openAIPath(endpoint string) string {
	if strings.HasSuffix(endpoint, "/v1") {
		return "/embeddings"
	}
	return "/v1/embeddings"
}

func (e *HTTPEmbedder) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	}
	resp, err := e.cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("embedding server %s: %w", e.cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		var eb struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil && len(eb.Error) > 0 {
			var s string
			var o struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(eb.Error, &s) == nil {
				msg = s
			} else if json.Unmarshal(eb.Error, &o) == nil && o.Message != "" {
				msg = o.Message
			}
		}
		if resp.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(msg), "not found") {
			return fmt.Errorf("%w: %s", ErrModelMissing, msg)
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &httpError{status: resp.StatusCode, msg: msg}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("embedding: unexpected answer: %w", err)
	}
	return nil
}

// HasModel asks an Ollama server whether the model is pulled (POST
// /api/show). For other providers it returns true.
func (e *HTTPEmbedder) HasModel(ctx context.Context) (bool, error) {
	if e.cfg.Provider != ProviderOllama {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var res map[string]any
	err := e.post(ctx, "/api/show", map[string]any{"model": e.cfg.Model}, &res)
	if errors.Is(err, ErrModelMissing) {
		return false, nil
	}
	var he *httpError
	if errors.As(err, &he) && he.status == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

// PullProgress reports a running pull.
type PullProgress struct {
	Status    string `json:"status"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// Pull downloads the model on an Ollama server (POST /api/pull, the
// same as `ollama pull <model>`). Only call it on an explicit user
// action.
func (e *HTTPEmbedder) Pull(ctx context.Context, progress func(PullProgress)) error {
	if e.cfg.Provider != ProviderOllama {
		return errors.New("only Ollama models can be pulled")
	}
	b, _ := json.Marshal(map[string]any{"model": e.cfg.Model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint+"/api/pull", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama %s: %w", e.cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{status: resp.StatusCode, msg: strings.TrimSpace(string(msg))}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	ok := false
	for sc.Scan() {
		var p struct {
			PullProgress
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue
		}
		if p.Error != "" {
			return fmt.Errorf("ollama pull %s: %s", e.cfg.Model, p.Error)
		}
		if progress != nil {
			progress(p.PullProgress)
		}
		ok = ok || p.Status == "success"
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("ollama pull: %w", err)
	}
	if !ok {
		return fmt.Errorf("ollama pull %s ended without success", e.cfg.Model)
	}
	return nil
}

// Prefixes returns the instruction prefixes of a model for documents
// and queries (EmbeddingGemma expects task prefixes; other models get
// none).
func Prefixes(model string) (doc func(title string) string, query string) {
	if strings.Contains(strings.ToLower(model), "embeddinggemma") {
		return func(title string) string {
			if title == "" {
				title = "none"
			}
			return "title: " + title + " | text: "
		}, "task: search result | query: "
	}
	return func(string) string { return "" }, ""
}
