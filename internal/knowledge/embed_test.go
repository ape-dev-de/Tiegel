package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeVec maps a text to a deterministic 4-dim vector by keywords.
func fakeVec(s string) []float32 {
	s = strings.ToLower(s)
	v := []float32{0.01, 0.01, 0.01, 0.01}
	for i, w := range []string{"order", "mail", "user", "payment"} {
		if strings.Contains(s, w) {
			v[i] = 1
		}
	}
	return v
}

// fakeOllama is an httptest Ollama: /api/embed, /api/show, /api/pull.
type fakeOllama struct {
	mu      sync.Mutex
	models  map[string]bool
	inputs  [][]string
	fail500 int32 // number of 500 answers before success
}

func (f *fakeOllama) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if atomic.AddInt32(&f.fail500, -1) >= 0 {
			http.Error(w, `{"error":"busy"}`, 500)
			return
		}
		f.mu.Lock()
		ok := f.models[req.Model]
		f.inputs = append(f.inputs, req.Input)
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":"model %q not found, try pulling it first"}`, req.Model)
			return
		}
		var out [][]float32
		for _, in := range req.Input {
			out = append(out, fakeVec(in))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.models[req.Model] {
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":"model '%s' not found"}`, req.Model)
			return
		}
		_, _ = w.Write([]byte(`{"details":{}}`))
	})
	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "bad" {
			fmt.Fprintln(w, `{"error":"pull model manifest: file does not exist"}`)
			return
		}
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"status":"pulling abc","total":100,"completed":50}`)
		fmt.Fprintln(w, `{"status":"success"}`)
		f.mu.Lock()
		f.models[req.Model] = true
		f.mu.Unlock()
	})
	return mux
}

func TestOllamaEmbedder(t *testing.T) {
	fo := &fakeOllama{models: map[string]bool{"m": true}}
	srv := httptest.NewServer(fo.handler())
	defer srv.Close()
	ctx := context.Background()
	e, err := NewEmbedder(EmbedConfig{Endpoint: srv.URL + "/", Model: "m", BatchSize: 2})
	if err != nil || e.Model() != "ollama:m" || e.Config().Timeout != 60*time.Second {
		t.Fatal(err)
	}
	vs, err := e.Embed(ctx, []string{"order", "mail", "user"})
	if err != nil || len(vs) != 3 || vs[1][1] != 1 || len(fo.inputs) != 2 {
		t.Fatal(vs, err, fo.inputs)
	}
	// Retries on 500.
	atomic.StoreInt32(&fo.fail500, 2)
	if _, err := e.Embed(ctx, []string{"x"}); err != nil {
		t.Fatal("no retry:", err)
	}
	atomic.StoreInt32(&fo.fail500, 5)
	if _, err := e.Embed(ctx, []string{"x"}); err == nil || !strings.Contains(err.Error(), "HTTP 500: busy") {
		t.Fatal(err)
	}
	atomic.StoreInt32(&fo.fail500, 0)
	// Missing model: no retry, ErrModelMissing.
	miss, _ := NewEmbedder(EmbedConfig{Endpoint: srv.URL, Model: "gone"})
	n := len(fo.inputs)
	if _, err := miss.Embed(ctx, []string{"x"}); !errors.Is(err, ErrModelMissing) || len(fo.inputs) != n+1 {
		t.Fatal(err)
	}
	if ok, err := miss.HasModel(ctx); ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := e.HasModel(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// Pull with progress.
	var got []string
	if err := miss.Pull(ctx, func(p PullProgress) { got = append(got, p.Status) }); err != nil || len(got) != 3 {
		t.Fatal(err, got)
	}
	if ok, _ := miss.HasModel(ctx); !ok {
		t.Fatal("pulled model missing")
	}
	bad, _ := NewEmbedder(EmbedConfig{Endpoint: srv.URL, Model: "bad"})
	if err := bad.Pull(ctx, nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatal(err)
	}
	// Server down.
	down, _ := NewEmbedder(EmbedConfig{Endpoint: "http://127.0.0.1:1", Model: "m", Retries: -1})
	if _, err := down.Embed(ctx, []string{"x"}); err == nil {
		t.Fatal("down")
	}
	if _, err := down.HasModel(ctx); err == nil {
		t.Fatal("down")
	}
	if err := down.Pull(ctx, nil); err == nil {
		t.Fatal("down")
	}
	// Config errors.
	if _, err := NewEmbedder(EmbedConfig{Provider: "x"}); err == nil {
		t.Fatal("provider")
	}
	if _, err := NewEmbedder(EmbedConfig{Provider: ProviderOpenAI}); err == nil {
		t.Fatal("endpoint")
	}
	if d, _ := NewEmbedder(EmbedConfig{}); d.Config().Endpoint != DefaultEndpoint || d.Config().Model != DefaultModel {
		t.Fatal(d.Config())
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		if r.URL.Path != "/v1/embeddings" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"message":"no route"}}`))
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "denied" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		type d struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []d
		for i := len(req.Input) - 1; i >= 0; i-- { // out of order
			data = append(data, d{Index: i, Embedding: fakeVec(req.Input[i])})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	ctx := context.Background()
	for _, ep := range []string{srv.URL, srv.URL + "/v1"} {
		e, _ := NewEmbedder(EmbedConfig{Provider: ProviderOpenAI, Endpoint: ep, Model: "text-embedding-3-small", APIKey: "sk-test"})
		vs, err := e.Embed(ctx, []string{"order", "mail"})
		if err != nil || vs[0][0] != 1 || vs[1][1] != 1 || auth.Load() != "Bearer sk-test" {
			t.Fatal(ep, vs, err)
		}
		if ok, err := e.HasModel(ctx); !ok || err != nil {
			t.Fatal("has model")
		}
		if err := e.Pull(ctx, nil); err == nil {
			t.Fatal("pull on openai")
		}
	}
	e, _ := NewEmbedder(EmbedConfig{Provider: ProviderOpenAI, Endpoint: srv.URL, Model: "denied"})
	if _, err := e.Embed(ctx, []string{"x"}); err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatal(err)
	}
	e, _ = NewEmbedder(EmbedConfig{Provider: ProviderOpenAI, Endpoint: srv.URL + "/other", Model: "m"})
	if _, err := e.Embed(ctx, []string{"x"}); err == nil || !strings.Contains(err.Error(), "no route") {
		t.Fatal(err)
	}
	// Wrong vector count and invalid JSON.
	odd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "embed") && r.URL.Query().Get("x") == "" {
			_, _ = w.Write([]byte(`{"embeddings":[[1,2]]}`))
		}
	}))
	defer odd.Close()
	o, _ := NewEmbedder(EmbedConfig{Endpoint: odd.URL, Model: "m"})
	if _, err := o.Embed(ctx, []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "1 vectors for 2") {
		t.Fatal(err)
	}
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer junk.Close()
	j, _ := NewEmbedder(EmbedConfig{Endpoint: junk.URL, Model: "m"})
	if _, err := j.Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("junk accepted")
	}
	if err := j.Pull(ctx, nil); err == nil || !strings.Contains(err.Error(), "without success") {
		t.Fatal(err)
	}
	doc, q := Prefixes("ollama:embeddinggemma-2:270m")
	if doc("") != "title: none | text: " || doc("X") != "title: X | text: " || q != "task: search result | query: " {
		t.Fatal("prefixes")
	}
	if doc, q := Prefixes("openai:text-embedding-3-small"); doc("x") != "" || q != "" {
		t.Fatal("no prefixes")
	}
}

func TestSyncVectors(t *testing.T) {
	fo := &fakeOllama{models: map[string]bool{"m1": true, "m2": true}}
	srv := httptest.NewServer(fo.handler())
	defer srv.Close()
	ctx := context.Background()
	dir := t.TempDir()
	m, _ := LoadManifest(dir)
	put := func(id, body string) {
		md := "units/" + id + ".md"
		write(t, dir, map[string]string{md: Meta{Unit: id, Title: id}.FrontMatter() + body})
		m.Units[id] = &UnitState{ID: id, Title: id, MD: md, Status: StatusDone, Files: map[string]string{"src/" + id + ".php": "h"}}
	}
	put("orders", "# Orders\n\n## Overview\n\nOrders are stored.\n\n## Data flows\n\n### Place\n\nThe order is saved.\n")
	put("mail", "# Mail\n\n## Overview\n\nMail is sent.\n")
	store, _ := OpenFileStore(filepath.Join(dir, "vectors"))
	// Without embedder: keyword chunks only.
	res, err := SyncVectors(ctx, dir, m, store, nil, 300, nil)
	if err != nil || res.Chunks == 0 || res.Pending != res.Chunks || res.Embedded != 0 {
		t.Fatal(res, err)
	}
	e1, _ := NewEmbedder(EmbedConfig{Endpoint: srv.URL, Model: "m1"})
	var progress int
	res, err = SyncVectors(ctx, dir, m, store, e1, 300, func(done, total int) { progress = done })
	if err != nil || res.Embedded != res.Chunks || res.Pending != 0 || progress != res.Chunks {
		t.Fatal(res, err)
	}
	if !strings.HasPrefix(fo.inputs[len(fo.inputs)-1][0], "title: ") && !strings.Contains(fo.inputs[0][0], "Orders") {
		t.Log(fo.inputs)
	}
	// Nothing changed → nothing embedded.
	calls := len(fo.inputs)
	res, _ = SyncVectors(ctx, dir, m, store, e1, 300, nil)
	if res.Embedded != 0 || len(fo.inputs) != calls {
		t.Fatal("re-embedded unchanged chunks", res)
	}
	// One document changed, one removed.
	put("orders", "# Orders\n\n## Overview\n\nOrders are stored and a payment is made.\n")
	delete(m.Units, "mail")
	res, _ = SyncVectors(ctx, dir, m, store, e1, 300, nil)
	if res.Embedded != 1 || res.Deleted == 0 {
		t.Fatal(res)
	}
	hits, _ := Search(ctx, store, "payment", SearchOptions{Query: fakeVec("payment")})
	if len(hits) == 0 || hits[0].Chunk.Unit != "orders" || hits[0].Vector < 0.5 {
		t.Fatalf("%+v", hits)
	}
	// Model change re-embeds everything.
	e2, _ := NewEmbedder(EmbedConfig{Endpoint: srv.URL, Model: "m2"})
	res, _ = SyncVectors(ctx, dir, m, store, e2, 300, nil)
	if res.Embedded != res.Chunks {
		t.Fatal("model change", res)
	}
	// Embedding fails → chunks stored without vectors, error returned.
	put("users", "# Users\n\n## Overview\n\nUser data.\n")
	bad, _ := NewEmbedder(EmbedConfig{Endpoint: srv.URL, Model: "m2", Retries: -1})
	atomic.StoreInt32(&fo.fail500, 100)
	res, err = SyncVectors(ctx, dir, m, store, bad, 300, nil)
	atomic.StoreInt32(&fo.fail500, 0)
	if err == nil || res.Pending == 0 || res.Error == "" {
		t.Fatal(res, err)
	}
	cs, _, _ := store.Chunks(ctx)
	found := false
	for _, c := range cs {
		found = found || c.Unit == "users"
	}
	if !found {
		t.Fatal("chunks of the failed batch were dropped")
	}
	// A missing document is skipped.
	m.Units["ghost"] = &UnitState{ID: "ghost", MD: "units/ghost.md"}
	if cs := AllChunks(dir, m, 300); len(cs) == 0 {
		t.Fatal("chunks")
	}
	_ = os.Remove(filepath.Join(dir, "units", "users.md"))
}
