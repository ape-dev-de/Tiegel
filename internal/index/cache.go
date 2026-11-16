package index

import (
	"bufio"
	"compress/gzip"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// CacheVersion is the version of the cache format. Bump it whenever File
// or the extraction changes; older caches are ignored and rebuilt.
const CacheVersion = 1

// CacheFile is the file name of the cache inside the cache directory.
const CacheFile = "php-index.gob.gz"

// ErrCacheMismatch is returned by Load for caches of another version,
// project or option set.
var ErrCacheMismatch = errors.New("index: cache does not match")

type cacheData struct {
	Version int
	Root    string
	Options string // fingerprint of the options that influence content
	Files   []*File
}

func (ix *Index) fingerprint() string {
	o := ix.opts
	ex := slices.Clone(o.Exclude)
	slices.Sort(ex)
	exts := slices.Clone(o.Extensions)
	slices.Sort(exts)
	return fmt.Sprintf("lib=%v ign=%v ex=%q ext=%q max=%d", !o.SkipLibraries, o.IncludeIgnored, ex, exts, o.MaxFileSize)
}

// Save writes the index to dir/CacheFile atomically (gob, gzip).
func (ix *Index) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, CacheFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("index: cache: %w", err)
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 1<<16)
	zw, _ := gzip.NewWriterLevel(bw, gzip.BestSpeed)
	ix.mu.RLock()
	data := cacheData{Version: CacheVersion, Root: ix.opts.Root, Options: ix.fingerprint(), Files: make([]*File, 0, len(ix.files))}
	for _, f := range ix.files {
		data.Files = append(data.Files, f)
	}
	err = gob.NewEncoder(zw).Encode(&data)
	ix.mu.RUnlock()
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = bw.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("index: write cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, CacheFile)); err != nil {
		return fmt.Errorf("index: write cache: %w", err)
	}
	return nil
}

// Load replaces the index content with the cache in dir. It fails with
// ErrCacheMismatch for another version, root or option set, and with an
// fs.ErrNotExist error if there is no cache. A following Sync brings the
// loaded state up to date (only changed files are parsed).
func (ix *Index) Load(dir string) error {
	f, err := os.Open(filepath.Join(dir, CacheFile))
	if err != nil {
		return fmt.Errorf("index: read cache: %w", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return fmt.Errorf("index: read cache: %w", err)
	}
	var data cacheData
	if err := gob.NewDecoder(zr).Decode(&data); err != nil {
		return fmt.Errorf("index: read cache: %w", err)
	}
	if data.Version != CacheVersion || data.Root != ix.opts.Root || data.Options != ix.fingerprint() {
		return ErrCacheMismatch
	}
	ix.Reset()
	ix.replaceAll(data.Files)
	return nil
}
