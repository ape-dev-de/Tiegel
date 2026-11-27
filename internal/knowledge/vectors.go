package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SyncResult tells what SyncVectors did.
type SyncResult struct {
	Chunks   int `json:"chunks"`
	Embedded int `json:"embedded"`
	Deleted  int `json:"deleted"`
	// Pending counts chunks without a vector (no embedder or it failed).
	Pending int `json:"pending"`
	// Error is the embedding error (keyword search still works).
	Error string `json:"error,omitempty"`
}

// AllChunks chunks every page of the manifest and questions.md.
func AllChunks(dir string, m *Manifest, target int) []Chunk {
	var out []Chunk
	if b, err := os.ReadFile(filepath.Join(dir, "questions.md")); err == nil {
		out = append(out, ChunkDoc(LevelQuestions, LevelQuestions, "questions.md", nil, string(b), ChunkOptions{Target: target})...)
	}
	for _, st := range m.Sorted() {
		if st.MD == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(st.MD)))
		if err != nil {
			continue
		}
		var sources []string
		for f := range st.Files {
			if !strings.HasPrefix(f, "@") {
				sources = append(sources, f)
			}
		}
		sort.Strings(sources)
		out = append(out, ChunkDoc(st.ID, st.Level, st.MD, sources, string(b), ChunkOptions{Target: target})...)
	}
	return out
}

// SyncVectors brings the store in line with the documents: removed
// chunks are deleted, new and changed chunks are stored and – with an
// embedder – embedded (also all chunks when the model changed). Without
// an embedder, or when embedding fails, chunks are stored without vector
// so that keyword search works. progress gets done/total of embedding.
func SyncVectors(ctx context.Context, dir string, m *Manifest, s Store, e Embedder, target int, progress func(done, total int)) (SyncResult, error) {
	var res SyncResult
	if e != nil {
		if err := s.SetModel(ctx, e.Model()); err != nil {
			return res, err
		}
	}
	chunks := AllChunks(dir, m, target)
	old, emb, err := s.Chunks(ctx)
	if err != nil {
		return res, err
	}
	oldHash := map[string]string{}
	for _, c := range old {
		oldHash[c.ID] = c.Hash
	}
	want := map[string]bool{}
	var todo []Chunk
	for _, c := range chunks {
		want[c.ID] = true
		if oldHash[c.ID] == c.Hash && (emb[c.ID] || e == nil) {
			continue
		}
		todo = append(todo, c)
	}
	var del []string
	for _, c := range old {
		if !want[c.ID] {
			del = append(del, c.ID)
		}
	}
	if len(del) > 0 {
		if err := s.Delete(ctx, del); err != nil {
			return res, err
		}
	}
	res.Deleted, res.Chunks = len(del), len(chunks)
	if e == nil {
		recs := make([]Record, len(todo))
		for i, c := range todo {
			recs[i] = Record{Chunk: c}
		}
		if len(recs) > 0 {
			if err := s.Upsert(ctx, recs); err != nil {
				return res, err
			}
		}
	} else {
		docPrefix, _ := Prefixes(e.Model())
		const step = 32
		for i := 0; i < len(todo); i += step {
			batch := todo[i:min(i+step, len(todo))]
			texts := make([]string, len(batch))
			for j, c := range batch {
				texts[j] = docPrefix(lastHeading(c.Heading)) + c.Text
			}
			vecs, err := e.Embed(ctx, texts)
			recs := make([]Record, len(batch))
			for j, c := range batch {
				recs[j] = Record{Chunk: c}
				if err == nil {
					recs[j].Vector = vecs[j]
				}
			}
			if uerr := s.Upsert(ctx, recs); uerr != nil {
				return res, uerr
			}
			if err != nil {
				// Store the rest without vectors; keyword search works.
				var rest []Record
				for _, c := range todo[i+len(batch):] {
					rest = append(rest, Record{Chunk: c})
				}
				if len(rest) > 0 {
					_ = s.Upsert(ctx, rest)
				}
				res.Error = err.Error()
				break
			}
			res.Embedded += len(batch)
			if progress != nil {
				progress(res.Embedded, len(todo))
			}
		}
	}
	st, err := s.Stats(ctx)
	if err != nil {
		return res, err
	}
	res.Pending = st.Chunks - st.Embedded
	if res.Error != "" {
		return res, fmt.Errorf("embedding: %s", res.Error)
	}
	return res, nil
}

func lastHeading(h string) string {
	if i := strings.LastIndex(h, " › "); i >= 0 {
		return h[i+len(" › "):]
	}
	return h
}
