//go:build !cgo

package index

import "github.com/ape-dev-de/tiegel/internal/syntax"

// TreeSitter reports whether this build can parse PHP (cgo). Without it
// the index can hold and query data (e.g. from the cache) but not parse
// files: Sync, UpdateText and the position based queries fail with
// syntax.ErrNoTreeSitter.
const TreeSitter = false

type parser struct{}

func newParser() (*parser, error) { return nil, syntax.ErrNoTreeSitter }

func (p *parser) close() {}

func (p *parser) parse(path string, _ []byte) *File { return &File{Path: path} }

func (p *parser) locate(path string, _ []byte, _ int) (*File, Ref, bool) {
	return &File{Path: path}, Ref{}, false
}
