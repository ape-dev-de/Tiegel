package index

import (
	"container/heap"
	"slices"
	"sort"
	"strings"
)

// symEntry is one searchable symbol. It references the File instead of
// copying a Location: big projects have hundreds of thousands of members.
type symEntry struct {
	f     *File
	ci    int32 // class index, -1 for functions and constants
	mi    int32 // member, function or constant index; -1 for a class
	kind  Kind
	name  string // short name as declared
	lname string // folded short name
	lfqn  string // folded FQN; "" for members (see fqn)
}

func (s *symEntry) class() *Class { return &s.f.Classes[s.ci] }

// fqn returns the FQN ("Class::member" for members).
func (s *symEntry) fqn() string {
	switch {
	case s.ci >= 0 && s.mi >= 0:
		return s.class().FQN + "::" + s.name
	case s.ci >= 0:
		return s.class().FQN
	case s.kind == KindFunction:
		return s.f.Funcs[s.mi].FQN
	default:
		return s.f.Consts[s.mi].FQN
	}
}

func (s *symEntry) location() Location {
	switch {
	case s.ci >= 0 && s.mi >= 0:
		return memberLoc(classEntry{f: s.f, c: s.class()}, &s.class().Members[s.mi])
	case s.ci >= 0:
		return classLoc(classEntry{f: s.f, c: s.class()})
	case s.kind == KindFunction:
		fn := &s.f.Funcs[s.mi]
		return Location{Path: s.f.Path, Kind: KindFunction, Name: fn.Name, FQN: fn.FQN, Library: s.f.Library,
			Detail: fn.Detail, Range: fn.Range, NameRange: fn.NameRange}
	default:
		c := &s.f.Consts[s.mi]
		return Location{Path: s.f.Path, Kind: KindConst, Name: c.Name, FQN: c.FQN, Library: s.f.Library,
			Range: c.Range, NameRange: c.NameRange}
	}
}

// SymbolMatch is one result of Symbols.
type SymbolMatch struct {
	Location
	Score int
}

// SymbolQuery selects symbols.
type SymbolQuery struct {
	// Query is matched fuzzily (subsequence, word starts and camel humps
	// preferred) against the short name; with a backslash against the FQN;
	// "Class::member" matches members of matching classes.
	Query string
	// Kinds restricts the result (empty: all).
	Kinds []Kind
	// Limit caps the result (default 100).
	Limit int
	// NoLibraries drops symbols from vendor/.
	NoLibraries bool
}

// symbolsSnapshot returns the search table (types first) and the number of
// type entries; it is rebuilt lazily after changes.
func (ix *Index) symbolsSnapshot() ([]symEntry, int) {
	ix.mu.RLock()
	gen := ix.gen
	ix.mu.RUnlock()
	ix.symMu.Lock()
	defer ix.symMu.Unlock()
	if ix.syms != nil && ix.symGen == gen {
		return ix.syms, ix.symTypes
	}
	ix.mu.RLock()
	gen = ix.gen
	var types []symEntry
	syms := make([]symEntry, 0, len(ix.files)*8)
	for _, f := range ix.files {
		for ci := range f.Classes {
			c := &f.Classes[ci]
			types = append(types, symEntry{f: f, ci: int32(ci), mi: -1, kind: c.Kind, name: c.Name, lname: fold(c.Name), lfqn: fold(c.FQN)})
			for mi := range c.Members {
				m := &c.Members[mi]
				syms = append(syms, symEntry{f: f, ci: int32(ci), mi: int32(mi), kind: m.Kind, name: m.Name, lname: fold(m.Name)})
			}
		}
		for i := range f.Funcs {
			fn := &f.Funcs[i]
			syms = append(syms, symEntry{f: f, ci: -1, mi: int32(i), kind: KindFunction, name: fn.Name, lname: fold(fn.Name), lfqn: fold(fn.FQN)})
		}
		for i := range f.Consts {
			c := &f.Consts[i]
			syms = append(syms, symEntry{f: f, ci: -1, mi: int32(i), kind: KindConst, name: c.Name, lname: fold(c.Name), lfqn: fold(c.FQN)})
		}
	}
	ix.mu.RUnlock()
	all := make([]symEntry, 0, len(types)+len(syms))
	all = append(append(all, types...), syms...)
	ix.syms, ix.symGen, ix.symTypes = all, gen, len(types)
	return all, len(types)
}

// Symbols searches the index (Go to Class / Go to Symbol).
func (ix *Index) Symbols(q SymbolQuery) []SymbolMatch {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	query := strings.TrimSpace(q.Query)
	query = strings.TrimPrefix(query, `\`)
	query = strings.TrimPrefix(query, "$")
	var kinds [16]bool
	for _, k := range q.Kinds {
		if int(k) < len(kinds) {
			kinds[k] = true
		}
	}
	anyKind := len(q.Kinds) == 0
	lq := fold(query)
	classPart, memberPart, scoped := strings.Cut(lq, "::")
	byFQN := strings.Contains(lq, `\`)
	syms, nTypes := ix.symbolsSnapshot()
	if !anyKind && !slices.ContainsFunc(q.Kinds, func(k Kind) bool { return !k.IsType() }) {
		syms = syms[:nTypes] // Go to Class: only the type entries
	}
	top := topK{syms: syms, limit: q.Limit}
	for i := range syms {
		s := &syms[i]
		if !anyKind && !kinds[s.kind] || q.NoLibraries && s.f.Library {
			continue
		}
		score := -1
		switch {
		case scoped:
			if s.ci < 0 || s.mi < 0 {
				continue
			}
			c := s.class()
			cs := fuzzyScore(classPart, fold(c.Name), c.Name)
			if cs < 0 {
				continue
			}
			ms := fuzzyScore(memberPart, s.lname, s.name)
			if ms < 0 {
				continue
			}
			score = cs + ms
		case byFQN:
			lfqn := s.lfqn
			if lfqn == "" {
				lfqn = fold(s.fqn())
			}
			score = fuzzyScore(lq, lfqn, s.fqn())
		default:
			score = fuzzyScore(lq, s.lname, s.name)
		}
		if score < 0 {
			continue
		}
		if s.kind.IsType() {
			score += 2 // types first among equals
		}
		top.offer(scored{score: score, i: i})
	}
	return top.result()
}

type scored struct {
	score int
	i     int
}

// topK keeps the best limit matches in a min-heap (worst on top), so a
// query matching most symbols does not sort all of them.
type topK struct {
	syms  []symEntry
	limit int
	h     []scored
}

// better reports whether a ranks before b.
func (t *topK) better(a, b scored) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	sa, sb := &t.syms[a.i], &t.syms[b.i]
	if sa.f.Library != sb.f.Library {
		return !sa.f.Library
	}
	if len(sa.name) != len(sb.name) {
		return len(sa.name) < len(sb.name)
	}
	if fa, fb := sa.fqn(), sb.fqn(); fa != fb {
		return fa < fb
	}
	return sa.f.Path < sb.f.Path
}

func (t *topK) Len() int           { return len(t.h) }
func (t *topK) Less(i, j int) bool { return t.better(t.h[j], t.h[i]) }
func (t *topK) Swap(i, j int)      { t.h[i], t.h[j] = t.h[j], t.h[i] }
func (t *topK) Push(x any)         { t.h = append(t.h, x.(scored)) }
func (t *topK) Pop() any {
	x := t.h[len(t.h)-1]
	t.h = t.h[:len(t.h)-1]
	return x
}

func (t *topK) offer(s scored) {
	if len(t.h) < t.limit {
		heap.Push(t, s)
		return
	}
	if t.better(s, t.h[0]) {
		t.h[0] = s
		heap.Fix(t, 0)
	}
}

func (t *topK) result() []SymbolMatch {
	sort.Slice(t.h, func(i, j int) bool { return t.better(t.h[i], t.h[j]) })
	out := make([]SymbolMatch, len(t.h))
	for k, s := range t.h {
		out[k] = SymbolMatch{Location: t.syms[s.i].location(), Score: s.score}
	}
	return out
}

func isWordStart(orig string, i int) bool {
	if i == 0 {
		return true
	}
	p, c := orig[i-1], orig[i]
	switch p {
	case '\\', '_', ':', '-', '$':
		return true
	}
	return p >= 'a' && p <= 'z' && c >= 'A' && c <= 'Z' || (p < '0' || p > '9') && c >= '0' && c <= '9'
}

// fuzzyScore matches query (lower case) as a subsequence of cand (lower
// case; orig is the original spelling for camel humps) and returns a score,
// or -1 if it does not match. Exact and prefix matches rank highest, then
// matches on word starts and consecutive runs.
func fuzzyScore(query, cand, orig string) int {
	if query == "" {
		return 0
	}
	if len(orig) != len(cand) {
		orig = cand
	}
	if len(query) > len(cand) {
		return -1
	}
	if query == cand {
		return 1000
	}
	if strings.HasPrefix(cand, query) {
		return 800 - min(len(cand)-len(query), 100)
	}
	score := 0
	qi := 0
	prev := -2
	for i := 0; i < len(cand) && qi < len(query); i++ {
		if cand[i] != query[qi] {
			continue
		}
		// prefer a later word start for this query char if the current
		// position is mid-word and not continuing a run
		if prev != i-1 && !isWordStart(orig, i) {
			if j := nextWordStart(cand, orig, i, query[qi]); j >= 0 && remainingMatches(query[qi+1:], cand[j+1:]) {
				i = j
			}
		}
		switch {
		case prev == i-1:
			score += 6
		case isWordStart(orig, i):
			score += 8
		default:
			score++
		}
		if i == 0 {
			score += 10
		}
		prev = i
		qi++
	}
	if qi < len(query) {
		return -1
	}
	if strings.Contains(cand, query) {
		score += 50
	}
	return score - min(len(cand)-len(query), 50)/5
}

func nextWordStart(cand, orig string, from int, c byte) int {
	for j := from + 1; j < len(cand); j++ {
		if cand[j] == c && isWordStart(orig, j) {
			return j
		}
	}
	return -1
}

// remainingMatches reports whether q is a subsequence of s.
func remainingMatches(q, s string) bool {
	qi := 0
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] == q[qi] {
			qi++
		}
	}
	return qi == len(q)
}
