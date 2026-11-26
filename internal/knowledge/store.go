package knowledge

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Record is a chunk with its embedding (nil: not embedded yet).
type Record struct {
	Chunk  Chunk
	Vector []float32
}

// Hit is a search result of a store.
type Hit struct {
	Chunk Chunk
	// Score is the cosine similarity (-1..1).
	Score float64
}

// StoreStats describe a store.
type StoreStats struct {
	// Chunks counts all chunks, Embedded those with a vector.
	Chunks   int `json:"chunks"`
	Embedded int `json:"embedded"`
	// Model and Dim describe the vectors.
	Model string `json:"model,omitempty"`
	Dim   int    `json:"dim,omitempty"`
	// Bytes is the size on disk.
	Bytes int64 `json:"bytes"`
	// Recovered is set when a broken store was dropped on open.
	Recovered string `json:"recovered,omitempty"`
}

// Store keeps chunks and their vectors. The file store below is brute
// force (fine for tens of thousands of chunks); a database backend (e.g.
// Postgres with pgvector) can implement the same interface.
type Store interface {
	// Upsert adds or replaces records by chunk ID. All vectors must have
	// the store's dimension; SetModel resets a store of another model.
	Upsert(ctx context.Context, recs []Record) error
	// Delete removes chunks by ID.
	Delete(ctx context.Context, ids []string) error
	// Search returns the k chunks most similar to the normalized vector
	// q.
	Search(ctx context.Context, q []float32, k int) ([]Hit, error)
	// Chunks returns all chunks with whether they have a vector.
	Chunks(ctx context.Context) ([]Chunk, map[string]bool, error)
	// SetModel declares the embedding model; a different model than the
	// stored one drops all vectors (the chunks stay).
	SetModel(ctx context.Context, model string) error
	Stats(ctx context.Context) (StoreStats, error)
	Close() error
}

// FileStore is a Store in a directory: chunks.json (metadata, in row
// order) and vectors.bin (header, float32 rows, CRC). Writes replace
// both files atomically.
type FileStore struct {
	dir string

	mu        sync.Mutex
	model     string
	dim       int
	chunks    []Chunk
	vecs      [][]float32 // nil rows: not embedded
	byID      map[string]int
	recovered string
}

const vecMagic = "TGVS"

type storeMeta struct {
	Version int     `json:"version"`
	Model   string  `json:"model"`
	Dim     int     `json:"dim"`
	Chunks  []Chunk `json:"chunks"`
	// Rows tells per chunk whether it has a vector (in vectors.bin order).
	Rows []bool `json:"rows"`
}

// OpenFileStore opens (or creates) the store in dir. A broken store is
// dropped and reported in Stats.Recovered.
func OpenFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vector store: %w", err)
	}
	s := &FileStore{dir: dir, byID: map[string]int{}}
	if err := s.load(); err != nil {
		s.model, s.dim, s.chunks, s.vecs, s.byID = "", 0, nil, nil, map[string]int{}
		s.recovered = err.Error()
	}
	return s, nil
}

func (s *FileStore) load() error {
	mb, err := os.ReadFile(filepath.Join(s.dir, "chunks.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta storeMeta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return fmt.Errorf("chunks.json is broken: %w", err)
	}
	if len(meta.Rows) != len(meta.Chunks) {
		return errors.New("chunks.json: rows and chunks differ")
	}
	vb, err := os.ReadFile(filepath.Join(s.dir, "vectors.bin"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	n := 0
	for _, r := range meta.Rows {
		if r {
			n++
		}
	}
	var rows [][]float32
	if n > 0 {
		if rows, err = decodeVectors(vb, meta.Dim, n); err != nil {
			return err
		}
	}
	s.model, s.dim, s.chunks = meta.Model, meta.Dim, meta.Chunks
	s.vecs = make([][]float32, len(meta.Chunks))
	j := 0
	for i, c := range meta.Chunks {
		s.byID[c.ID] = i
		if meta.Rows[i] {
			s.vecs[i] = rows[j]
			j++
		}
	}
	return nil
}

// decodeVectors reads vectors.bin: magic, dim (uint32), count (uint32),
// count*dim float32 (little endian), CRC32 of everything before.
func decodeVectors(b []byte, dim, n int) ([][]float32, error) {
	if len(b) < 16 || string(b[:4]) != vecMagic {
		return nil, errors.New("vectors.bin is broken: bad header")
	}
	body, sum := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errors.New("vectors.bin is broken: checksum mismatch")
	}
	d, c := int(binary.LittleEndian.Uint32(b[4:])), int(binary.LittleEndian.Uint32(b[8:]))
	if d != dim || c != n || len(body) != 12+4*d*c {
		return nil, fmt.Errorf("vectors.bin is broken: %d×%d vectors, expected %d×%d", c, d, n, dim)
	}
	out := make([][]float32, c)
	off := 12
	for i := range out {
		v := make([]float32, d)
		for k := range v {
			v[k] = math.Float32frombits(binary.LittleEndian.Uint32(body[off:]))
			off += 4
		}
		out[i] = v
	}
	return out, nil
}

func encodeVectors(dim int, rows [][]float32) []byte {
	var buf bytes.Buffer
	buf.WriteString(vecMagic)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dim))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(rows)))
	b := make([]byte, 4)
	for _, v := range rows {
		for _, f := range v {
			binary.LittleEndian.PutUint32(b, math.Float32bits(f))
			buf.Write(b)
		}
	}
	sum := crc32.ChecksumIEEE(buf.Bytes())
	_ = binary.Write(&buf, binary.LittleEndian, sum)
	return buf.Bytes()
}

// save writes both files (callers hold mu). vectors.bin is written
// first; chunks.json last, so a crash in between is caught by the
// count check on the next open.
func (s *FileStore) save() error {
	meta := storeMeta{Version: 1, Model: s.model, Dim: s.dim, Chunks: s.chunks, Rows: make([]bool, len(s.chunks))}
	var rows [][]float32
	for i, v := range s.vecs {
		if v != nil {
			meta.Rows[i] = true
			rows = append(rows, v)
		}
	}
	if err := WriteFileAtomic(filepath.Join(s.dir, "vectors.bin"), encodeVectors(s.dim, rows)); err != nil {
		return fmt.Errorf("vector store: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(filepath.Join(s.dir, "chunks.json"), mb); err != nil {
		return fmt.Errorf("vector store: %w", err)
	}
	return nil
}

// Upsert implements Store.
func (s *FileStore) Upsert(ctx context.Context, recs []Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		if r.Vector != nil {
			if s.dim == 0 {
				s.dim = len(r.Vector)
			}
			if len(r.Vector) != s.dim {
				return fmt.Errorf("vector store: dimension %d, store has %d", len(r.Vector), s.dim)
			}
		}
	}
	for _, r := range recs {
		var v []float32
		if r.Vector != nil {
			v = Normalize(r.Vector)
		}
		if i, ok := s.byID[r.Chunk.ID]; ok {
			s.chunks[i], s.vecs[i] = r.Chunk, v
			continue
		}
		s.byID[r.Chunk.ID] = len(s.chunks)
		s.chunks = append(s.chunks, r.Chunk)
		s.vecs = append(s.vecs, v)
	}
	return s.save()
}

// Delete implements Store.
func (s *FileStore) Delete(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	var cs []Chunk
	var vs [][]float32
	s.byID = map[string]int{}
	for i, c := range s.chunks {
		if drop[c.ID] {
			continue
		}
		s.byID[c.ID] = len(cs)
		cs = append(cs, c)
		vs = append(vs, s.vecs[i])
	}
	s.chunks, s.vecs = cs, vs
	return s.save()
}

// Search implements Store.
func (s *FileStore) Search(ctx context.Context, q []float32, k int) ([]Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(q) != s.dim || s.dim == 0 {
		return nil, fmt.Errorf("vector store: query dimension %d, store has %d", len(q), s.dim)
	}
	q = Normalize(q)
	var hits []Hit
	for i, v := range s.vecs {
		if v == nil {
			continue
		}
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		hits = append(hits, Hit{Chunk: s.chunks[i], Score: dot(q, v)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Chunks implements Store.
func (s *FileStore) Chunks(context.Context) ([]Chunk, map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	emb := make(map[string]bool, len(s.chunks))
	for i, c := range s.chunks {
		emb[c.ID] = s.vecs[i] != nil
	}
	return append([]Chunk(nil), s.chunks...), emb, nil
}

// SetModel implements Store.
func (s *FileStore) SetModel(_ context.Context, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.model == model {
		return nil
	}
	s.model, s.dim = model, 0
	for i := range s.vecs {
		s.vecs[i] = nil
	}
	return s.save()
}

// Stats implements Store.
func (s *FileStore) Stats(context.Context) (StoreStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StoreStats{Chunks: len(s.chunks), Model: s.model, Dim: s.dim, Recovered: s.recovered}
	for _, v := range s.vecs {
		if v != nil {
			st.Embedded++
		}
	}
	for _, f := range []string{"chunks.json", "vectors.bin"} {
		if fi, err := os.Stat(filepath.Join(s.dir, f)); err == nil {
			st.Bytes += fi.Size()
		}
	}
	return st, nil
}

// Close implements Store.
func (s *FileStore) Close() error { return nil }

// Normalize returns v scaled to length 1 (a copy; zero vectors stay).
func Normalize(v []float32) []float32 {
	var n float64
	for _, f := range v {
		n += float64(f) * float64(f)
	}
	out := make([]float32, len(v))
	if n == 0 {
		copy(out, v)
		return out
	}
	inv := 1 / math.Sqrt(n)
	for i, f := range v {
		out[i] = float32(float64(f) * inv)
	}
	return out
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

var _ io.Closer = (*FileStore)(nil)
