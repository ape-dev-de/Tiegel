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
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// FileStore is a Store in a directory: chunks.json (metadata in row
// order, model, dimension and the name of the vector file) and
// vectors-<generation>.bin (header, float32 rows, CRC).
//
// Readers never wait for writers and never see a half-written store
// (D565): the store is an immutable snapshot behind an atomic pointer; a
// write copies it, saves a new vector file, then replaces chunks.json
// atomically (the commit point) and swaps the snapshot. The previous
// vector file is kept for readers in other processes (the MCP server)
// that loaded the old chunks.json a moment ago. Readers reload when
// chunks.json changed on disk (another process wrote it).
type FileStore struct {
	dir string

	// wmu serializes writers (and reloads).
	wmu  sync.Mutex
	snap atomic.Pointer[snapshot]
}

// snapshot is an immutable state of the store.
type snapshot struct {
	model     string
	dim       int
	chunks    []Chunk
	vecs      [][]float32 // nil rows: not embedded
	byID      map[string]int
	recovered string
	// vectors is the vector file, stamp the chunks.json it came from.
	vectors string
	stamp   fileStamp
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

func stampOf(file string) fileStamp {
	st, err := os.Stat(file)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{st.Size(), st.ModTime()}
}

const vecMagic = "TGVS"

// legacyVectors is the vector file of stores written before D565.
const legacyVectors = "vectors.bin"

type storeMeta struct {
	Version int     `json:"version"`
	Model   string  `json:"model"`
	Dim     int     `json:"dim"`
	Chunks  []Chunk `json:"chunks"`
	// Rows tells per chunk whether it has a vector (in vector file order).
	Rows []bool `json:"rows"`
	// Vectors names the vector file ("" = vectors.bin).
	Vectors string `json:"vectors,omitempty"`
}

// OpenFileStore opens (or creates) the store in dir. A broken store is
// dropped and reported in Stats.Recovered.
func OpenFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vector store: %w", err)
	}
	s := &FileStore{dir: dir}
	s.snap.Store(s.load())
	return s, nil
}

// load reads the store from disk; a broken store yields an empty
// snapshot with recovered set.
func (s *FileStore) load() *snapshot {
	var sn *snapshot
	var err error
	// A writer in another process may remove the vector file between
	// reading chunks.json and the vector file: read again then.
	for range 3 {
		if sn, err = s.read(); err == nil || !errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	if err != nil {
		return &snapshot{byID: map[string]int{}, recovered: err.Error(), stamp: stampOf(filepath.Join(s.dir, "chunks.json"))}
	}
	return sn
}

func (s *FileStore) read() (*snapshot, error) {
	cj := filepath.Join(s.dir, "chunks.json")
	stamp := stampOf(cj)
	sn := &snapshot{byID: map[string]int{}, stamp: stamp}
	mb, err := os.ReadFile(cj)
	if errors.Is(err, os.ErrNotExist) {
		return sn, nil
	}
	if err != nil {
		return nil, err
	}
	var meta storeMeta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return nil, fmt.Errorf("chunks.json is broken: %w", err)
	}
	if len(meta.Rows) != len(meta.Chunks) {
		return nil, errors.New("chunks.json: rows and chunks differ")
	}
	vf := meta.Vectors
	if vf == "" || strings.ContainsAny(vf, `/\`) {
		vf = legacyVectors
	}
	n := 0
	for _, r := range meta.Rows {
		if r {
			n++
		}
	}
	var rows [][]float32
	if n > 0 {
		vb, err := os.ReadFile(filepath.Join(s.dir, vf))
		if err != nil {
			return nil, fmt.Errorf("vector store: %w", err)
		}
		if rows, err = decodeVectors(vb, meta.Dim, n); err != nil {
			return nil, err
		}
	}
	sn.model, sn.dim, sn.chunks, sn.vectors = meta.Model, meta.Dim, meta.Chunks, vf
	sn.vecs = make([][]float32, len(meta.Chunks))
	j := 0
	for i, c := range meta.Chunks {
		sn.byID[c.ID] = i
		if meta.Rows[i] {
			sn.vecs[i] = rows[j]
			j++
		}
	}
	return sn, nil
}

// current returns the snapshot readers use: the last one, reloaded when
// another process changed chunks.json (unless a write is running – then
// the writer's snapshot follows shortly).
func (s *FileStore) current() *snapshot {
	sn := s.snap.Load()
	if stampOf(filepath.Join(s.dir, "chunks.json")) == sn.stamp {
		return sn
	}
	if !s.wmu.TryLock() {
		return sn
	}
	defer s.wmu.Unlock()
	if sn = s.snap.Load(); stampOf(filepath.Join(s.dir, "chunks.json")) != sn.stamp {
		sn = s.load()
		s.snap.Store(sn)
	}
	return sn
}

// decodeVectors reads a vector file: magic, dim (uint32), count
// (uint32), count*dim float32 (little endian), CRC32 of everything
// before.
func decodeVectors(b []byte, dim, n int) ([][]float32, error) {
	if len(b) < 16 || string(b[:4]) != vecMagic {
		return nil, errors.New("vector file is broken: bad header")
	}
	body, sum := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errors.New("vector file is broken: checksum mismatch")
	}
	d, c := int(binary.LittleEndian.Uint32(b[4:])), int(binary.LittleEndian.Uint32(b[8:]))
	if d != dim || c != n || len(body) != 12+4*d*c {
		return nil, fmt.Errorf("vector file is broken: %d×%d vectors, expected %d×%d", c, d, n, dim)
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

var genCounter atomic.Uint64

// commit saves sn (callers hold wmu) and makes it the current snapshot:
// a new vector file first, then chunks.json (the commit point); vector
// files older than the previous one are removed.
func (s *FileStore) commit(sn *snapshot) error {
	prev := s.snap.Load()
	meta := storeMeta{Version: 2, Model: sn.model, Dim: sn.dim, Chunks: sn.chunks, Rows: make([]bool, len(sn.chunks))}
	var rows [][]float32
	for i, v := range sn.vecs {
		if v != nil {
			meta.Rows[i] = true
			rows = append(rows, v)
		}
	}
	meta.Vectors = fmt.Sprintf("vectors-%x-%x.bin", time.Now().UnixNano(), genCounter.Add(1))
	if err := WriteFileAtomic(filepath.Join(s.dir, meta.Vectors), encodeVectors(sn.dim, rows)); err != nil {
		return fmt.Errorf("vector store: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(filepath.Join(s.dir, "chunks.json"), mb); err != nil {
		_ = os.Remove(filepath.Join(s.dir, meta.Vectors))
		return fmt.Errorf("vector store: %w", err)
	}
	sn.vectors = meta.Vectors
	sn.stamp = stampOf(filepath.Join(s.dir, "chunks.json"))
	s.snap.Store(sn)
	keep := map[string]bool{meta.Vectors: true, prev.vectors: true}
	if ms, _ := filepath.Glob(filepath.Join(s.dir, "vectors*.bin")); len(ms) > 0 {
		for _, m := range ms {
			if !keep[filepath.Base(m)] {
				_ = os.Remove(m)
			}
		}
	}
	return nil
}

// edit returns a copy of the current snapshot to change (callers hold
// wmu; the snapshot is reloaded first when another process wrote).
func (s *FileStore) edit() *snapshot {
	cur := s.snap.Load()
	if stampOf(filepath.Join(s.dir, "chunks.json")) != cur.stamp {
		cur = s.load()
		s.snap.Store(cur)
	}
	sn := &snapshot{model: cur.model, dim: cur.dim, chunks: slices.Clone(cur.chunks), vecs: slices.Clone(cur.vecs), byID: make(map[string]int, len(cur.byID)), vectors: cur.vectors}
	for k, v := range cur.byID {
		sn.byID[k] = v
	}
	return sn
}

// Upsert implements Store.
func (s *FileStore) Upsert(ctx context.Context, recs []Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	sn := s.edit()
	for _, r := range recs {
		if r.Vector != nil {
			if sn.dim == 0 {
				sn.dim = len(r.Vector)
			}
			if len(r.Vector) != sn.dim {
				return fmt.Errorf("vector store: dimension %d, store has %d", len(r.Vector), sn.dim)
			}
		}
	}
	for _, r := range recs {
		var v []float32
		if r.Vector != nil {
			v = Normalize(r.Vector)
		}
		if i, ok := sn.byID[r.Chunk.ID]; ok {
			sn.chunks[i], sn.vecs[i] = r.Chunk, v
			continue
		}
		sn.byID[r.Chunk.ID] = len(sn.chunks)
		sn.chunks = append(sn.chunks, r.Chunk)
		sn.vecs = append(sn.vecs, v)
	}
	return s.commit(sn)
}

// Delete implements Store.
func (s *FileStore) Delete(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	cur := s.edit()
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	sn := &snapshot{model: cur.model, dim: cur.dim, byID: map[string]int{}, vectors: cur.vectors}
	for i, c := range cur.chunks {
		if drop[c.ID] {
			continue
		}
		sn.byID[c.ID] = len(sn.chunks)
		sn.chunks = append(sn.chunks, c)
		sn.vecs = append(sn.vecs, cur.vecs[i])
	}
	return s.commit(sn)
}

// Search implements Store.
func (s *FileStore) Search(ctx context.Context, q []float32, k int) ([]Hit, error) {
	sn := s.current()
	if len(q) != sn.dim || sn.dim == 0 {
		return nil, fmt.Errorf("vector store: query dimension %d, store has %d", len(q), sn.dim)
	}
	q = Normalize(q)
	var hits []Hit
	for i, v := range sn.vecs {
		if v == nil {
			continue
		}
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		hits = append(hits, Hit{Chunk: sn.chunks[i], Score: dot(q, v)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Chunks implements Store.
func (s *FileStore) Chunks(context.Context) ([]Chunk, map[string]bool, error) {
	sn := s.current()
	emb := make(map[string]bool, len(sn.chunks))
	for i, c := range sn.chunks {
		emb[c.ID] = sn.vecs[i] != nil
	}
	return slices.Clone(sn.chunks), emb, nil
}

// SetModel implements Store.
func (s *FileStore) SetModel(_ context.Context, model string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.snap.Load().model == model && s.snap.Load().stamp == stampOf(filepath.Join(s.dir, "chunks.json")) {
		return nil
	}
	sn := s.edit()
	if sn.model == model {
		return nil
	}
	sn.model, sn.dim = model, 0
	sn.vecs = make([][]float32, len(sn.chunks))
	return s.commit(sn)
}

// Stats implements Store.
func (s *FileStore) Stats(context.Context) (StoreStats, error) {
	sn := s.current()
	st := StoreStats{Chunks: len(sn.chunks), Model: sn.model, Dim: sn.dim, Recovered: sn.recovered}
	for _, v := range sn.vecs {
		if v != nil {
			st.Embedded++
		}
	}
	files := []string{"chunks.json"}
	if sn.vectors != "" {
		files = append(files, sn.vectors)
	}
	for _, f := range files {
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
