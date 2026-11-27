package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// syncVectors chunks the documents and embeds changed sections. Without
// a reachable embedding server the chunks are stored for keyword search
// and the error is returned.
func (m *Module) syncVectors(ctx context.Context, model string) (api.KnowledgeEmbedResult, error) {
	dir, err := m.dir()
	if err != nil {
		return api.KnowledgeEmbedResult{}, err
	}
	man, err := kn.LoadManifest(dir)
	if err != nil {
		return api.KnowledgeEmbedResult{}, err
	}
	store, err := m.openStore()
	if err != nil {
		return api.KnowledgeEmbedResult{}, err
	}
	emb, err := kn.NewEmbedder(m.embedConfig(model))
	if err != nil {
		return api.KnowledgeEmbedResult{}, api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	res, err := kn.SyncVectors(ctx, dir, man, store, emb, setting(m, SettingChunkSize, 500), func(done, total int) {
		m.mu.Lock()
		m.run.Embedded, m.run.EmbedTotal = done, total
		m.mu.Unlock()
	})
	out := api.KnowledgeEmbedResult{Chunks: res.Chunks, Embedded: res.Embedded, Deleted: res.Deleted, Pending: res.Pending, Model: emb.Model(), Error: res.Error}
	if errors.Is(err, kn.ErrModelMissing) || strings.Contains(res.Error, kn.ErrModelMissing.Error()) {
		out.Error = fmt.Sprintf("the model %s is not on the Ollama server – pull it (Knowledge window or: tiegel --headless knowledge pull)", emb.Config().Model)
		err = errors.New(out.Error)
	}
	return out, err
}

// embed is knowledge.embed.
func (m *Module) embed(ctx context.Context, p api.KnowledgeEmbedParams) (api.KnowledgeEmbedResult, error) {
	res, err := m.syncVectors(ctx, p.Model)
	m.publishStatus()
	if err != nil && res.Chunks > 0 {
		// Chunks are stored for keyword search; report the embedding error
		// in the result.
		return res, nil
	}
	return res, err
}

// search is knowledge.search.
func (m *Module) search(ctx context.Context, p api.KnowledgeSearchParams) (api.KnowledgeSearchResult, error) {
	res := api.KnowledgeSearchResult{Hits: []api.KnowledgeHit{}, Mode: "keyword"}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return res, api.Errorf(api.CodeInvalidParams, "query is required")
	}
	k := p.K
	if k <= 0 {
		k = 8
	}
	k = min(k, 50)
	dir, err := m.dir()
	if err != nil {
		return res, err
	}
	store, err := m.openStore()
	if err != nil {
		return res, err
	}
	stats, _ := store.Stats(ctx)
	// Searching never writes: pages not embedded yet (a running build,
	// --no-embed) take part keyword only, the store is the last
	// consistent snapshot.
	live := m.liveChunks(dir)
	var qv []float32
	if !p.KeywordOnly && stats.Embedded > 0 {
		cfg := m.embedConfig("")
		emb, err := kn.NewEmbedder(cfg)
		switch {
		case err != nil:
			res.Note = err.Error()
		case stats.Model != emb.Model():
			res.Note = fmt.Sprintf("the stored vectors are from %s, the setting says %s – run knowledge embed; keyword search only", stats.Model, emb.Model())
		default:
			_, prefix := kn.Prefixes(emb.Model())
			ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
			vs, err := emb.Embed(ectx, []string{prefix + q})
			cancel()
			if err != nil {
				res.Note = "embedding the query failed (" + err.Error() + "); keyword search only"
			} else if len(vs) == 1 {
				qv = vs[0]
			}
		}
	} else if !p.KeywordOnly && (stats.Chunks > 0 || len(live) > 0) {
		res.Note = "no embeddings yet (run knowledge embed); keyword search only"
	}
	results, err := kn.Search(ctx, store, q, kn.SearchOptions{K: k, Query: qv, Extra: live})
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "search: %v", err)
	}
	if qv != nil {
		res.Mode = "hybrid"
	}
	for _, r := range results {
		c := r.Chunk
		res.Hits = append(res.Hits, api.KnowledgeHit{Score: r.Score, Vector: r.Vector, Keyword: r.Keyword, Unit: c.Unit, Level: c.Level, Path: c.MD,
			File: filepath.Join(dir, filepath.FromSlash(c.MD)), Heading: c.Heading, Anchor: c.Anchor, Text: c.Text, Snippet: kn.Snippet(c.Text, 300), Sources: c.Sources})
	}
	return res, nil
}

// readDoc reads a file of the knowledge directory (rel is slash
// separated and must stay inside).
func readDoc(dir, rel string) (api.KnowledgeDoc, error) {
	rel = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(strings.TrimSpace(rel))), "/")
	if rel == "" || !strings.HasSuffix(rel, ".md") {
		return api.KnowledgeDoc{}, api.Errorf(api.CodeInvalidParams, "only Markdown files of the knowledge base can be read")
	}
	file := filepath.Join(dir, filepath.FromSlash(rel))
	b, err := os.ReadFile(file)
	if err != nil {
		return api.KnowledgeDoc{}, api.Errorf(api.CodeNotFound, "%s does not exist in the knowledge base", rel)
	}
	return api.KnowledgeDoc{Path: rel, File: file, Content: string(b)}, nil
}

// show is knowledge.show.
func (m *Module) show(_ context.Context, p api.KnowledgeUnitParams) (api.KnowledgeDoc, error) {
	dir, err := m.dir()
	if err != nil {
		return api.KnowledgeDoc{}, err
	}
	if p.Unit == "" {
		return readDoc(dir, "index.md")
	}
	if p.Unit == kn.LevelQuestions {
		return readDoc(dir, "questions.md")
	}
	man, _ := kn.LoadManifest(dir)
	st := man.Items[p.Unit]
	if st == nil {
		// Also accept a title, a group (class, kind) or "process-"-less
		// IDs.
		want := strings.TrimSpace(p.Unit)
		for _, s := range man.Sorted() {
			if strings.EqualFold(s.Title, want) || strings.EqualFold(s.Group, want) || s.ID == "process-"+kn.Slug(want) || s.ID == "component-"+kn.Slug(want) {
				st = s
				break
			}
		}
	}
	if st == nil || st.MD == "" {
		return api.KnowledgeDoc{}, api.Errorf(api.CodeNotFound, "page %q does not exist (knowledge list shows the pages)", p.Unit)
	}
	return readDoc(dir, st.MD)
}

// read is knowledge.read.
func (m *Module) read(ctx context.Context, p api.KnowledgeReadParams) (api.KnowledgeDoc, error) {
	dir, err := m.dir()
	if err != nil {
		return api.KnowledgeDoc{}, err
	}
	if !strings.HasSuffix(p.Path, ".md") && p.Path != "" {
		return m.show(ctx, api.KnowledgeUnitParams{Unit: p.Path})
	}
	if p.Path == "" {
		p.Path = "index.md"
	}
	return readDoc(dir, p.Path)
}

// liveChunks are the chunks of the pages on disk now, cached until the
// manifest or questions.md change.
type liveChunks struct {
	mu     sync.Mutex
	key    string
	chunks []kn.Chunk
}

func stamp(file string) string {
	st, err := os.Stat(file)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d.%d", st.Size(), st.ModTime().UnixNano())
}

func (m *Module) liveChunks(dir string) []kn.Chunk {
	size := setting(m, SettingChunkSize, 500)
	key := fmt.Sprintf("%s|%s|%s|%d", dir, stamp(filepath.Join(dir, "manifest.json")), stamp(filepath.Join(dir, "questions.md")), size)
	l := &m.live
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.key == key {
		return l.chunks
	}
	man, err := kn.LoadManifest(dir)
	if err != nil {
		return nil
	}
	l.key, l.chunks = key, kn.AllChunks(dir, man, size)
	return l.chunks
}

// modelStatus is knowledge.modelStatus.
func (m *Module) modelStatus(ctx context.Context, _ struct{}) (api.KnowledgeModelStatus, error) {
	cfg := m.embedConfig("")
	st := api.KnowledgeModelStatus{Provider: cfg.Provider, Endpoint: cfg.Endpoint, Model: cfg.Model}
	if st.Endpoint == "" && cfg.Provider == kn.ProviderOllama {
		st.Endpoint = kn.DefaultEndpoint
	}
	if cfg.Provider == kn.ProviderOllama {
		st.PullCommand = "ollama pull " + cfg.Model
	}
	m.mu.Lock()
	st.Pulling, st.PullStatus = m.pulling, m.pullStatus
	if m.pullErr != "" && !m.pulling {
		st.Error = m.pullErr
	}
	m.mu.Unlock()
	emb, err := kn.NewEmbedder(cfg)
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	if cfg.Provider != kn.ProviderOllama {
		// Asking would send a request with the key; report the settings.
		st.Reachable, st.Available = true, true
		return st, nil
	}
	ok, err := emb.HasModel(ctx)
	if err != nil {
		st.Error = "Ollama is not reachable at " + st.Endpoint + " (is it running? `ollama serve`)"
		return st, nil
	}
	st.Reachable, st.Available = true, ok
	return st, nil
}

// pullModel is knowledge.pullModel: `ollama pull <model>` through the
// Ollama server, only on an explicit user action.
func (m *Module) pullModel(ctx context.Context, p api.KnowledgePullParams) (api.KnowledgeModelStatus, error) {
	cfg := m.embedConfig("")
	emb, err := kn.NewEmbedder(cfg)
	if err != nil {
		return api.KnowledgeModelStatus{}, api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	if cfg.Provider != kn.ProviderOllama {
		return api.KnowledgeModelStatus{}, api.Errorf(api.CodeInvalidParams, "only Ollama models can be pulled")
	}
	m.mu.Lock()
	if m.pulling {
		m.mu.Unlock()
		return api.KnowledgeModelStatus{}, api.Errorf(api.CodeConflict, "a pull is running")
	}
	m.pulling, m.pullStatus, m.pullErr = true, "starting", ""
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		last := time.Time{}
		err := emb.Pull(pctx, func(pp kn.PullProgress) {
			s := pp.Status
			if pp.Total > 0 {
				s = fmt.Sprintf("%s %d%%", pp.Status, pp.Completed*100/pp.Total)
			}
			m.mu.Lock()
			m.pullStatus = s
			m.mu.Unlock()
			if time.Since(last) > 500*time.Millisecond {
				last = time.Now()
				m.publishStatus()
			}
		})
		m.mu.Lock()
		m.pulling = false
		if err != nil {
			m.pullErr, m.pullStatus = err.Error(), ""
		} else {
			m.pullStatus = "success"
		}
		m.mu.Unlock()
		m.publishStatus()
	}()
	if p.Wait {
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	return m.modelStatus(ctx, struct{}{})
}

// mcpConfig is knowledge.mcpConfig.
func (m *Module) mcpConfig(context.Context, struct{}) (api.KnowledgeMCPConfig, error) {
	cmd, args, err := m.mcpCommand()
	if err != nil {
		return api.KnowledgeMCPConfig{}, err
	}
	full := append([]string{cmd}, args...)
	oc, _ := json.MarshalIndent(map[string]any{"mcp": map[string]any{"tiegel-knowledge": map[string]any{"type": "local", "command": full, "enabled": true}}}, "", "  ")
	cl, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"tiegel-knowledge": map[string]any{"command": cmd, "args": args}}}, "", "  ")
	return api.KnowledgeMCPConfig{Command: cmd, Args: args, Opencode: string(oc), Claude: string(cl)}, nil
}
