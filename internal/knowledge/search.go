package knowledge

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Result is a hybrid search result.
type Result struct {
	Chunk Chunk `json:"chunk"`
	// Score is the combined score (0..1); Vector and Keyword its parts.
	Score   float64 `json:"score"`
	Vector  float64 `json:"vector,omitempty"`
	Keyword float64 `json:"keyword,omitempty"`
}

// SearchOptions configure Search.
type SearchOptions struct {
	K int
	// Query is the vector of the query (nil: keyword search only).
	Query []float32
	// KeywordWeight is the share of the keyword score (default 0.3; 1
	// without a query vector).
	KeywordWeight float64
	// Extra are chunks of the current pages: those missing from the
	// store or with another text take part keyword only (their vector
	// part counts half of their keyword score).
	Extra []Chunk
}

// Search runs a hybrid search over the store: cosine similarity of the
// embeddings plus a BM25 keyword score over the same chunks, each
// normalized to 0..1 before mixing.
func Search(ctx context.Context, s Store, query string, o SearchOptions) ([]Result, error) {
	if o.K <= 0 {
		o.K = 8
	}
	chunks, _, err := s.Chunks(ctx)
	if err != nil {
		return nil, err
	}
	// Pages written after the last embedding (e.g. during a running
	// build) take part with their current text, keyword only.
	stale := map[string]bool{}
	if len(o.Extra) > 0 {
		at := make(map[string]int, len(chunks))
		for i, c := range chunks {
			at[c.ID] = i
		}
		for _, c := range o.Extra {
			i, ok := at[c.ID]
			switch {
			case !ok:
				at[c.ID] = len(chunks)
				chunks = append(chunks, c)
				stale[c.ID] = true
			case chunks[i].Hash != c.Hash:
				chunks[i] = c
				stale[c.ID] = true
			}
		}
	}
	kw := BM25(chunks, query)
	vec := map[string]float64{}
	if o.Query != nil {
		hits, err := s.Search(ctx, o.Query, max(o.K*4, 32))
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			if !stale[h.Chunk.ID] {
				vec[h.Chunk.ID] = h.Score
			}
		}
	}
	w := o.KeywordWeight
	if w <= 0 {
		w = 0.3
	}
	if len(vec) == 0 {
		w = 1
	}
	kmax := 0.0
	for _, v := range kw {
		kmax = math.Max(kmax, v)
	}
	var out []Result
	for _, c := range chunks {
		k, v := kw[c.ID], vec[c.ID]
		if k == 0 && v == 0 {
			continue
		}
		kn := 0.0
		if kmax > 0 {
			kn = k / kmax
		}
		// Cosine of unrelated texts is around 0.2-0.4 for most models;
		// clamp negatives.
		vn := math.Max(0, v)
		if stale[c.ID] {
			vn = kn / 2
		}
		r := Result{Chunk: c, Vector: round3(v), Keyword: round3(kn), Score: round3((1-w)*vn + w*kn)}
		if r.Score > 0 {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > o.K {
		out = out[:o.K]
	}
	return out, nil
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// Tokenize splits text into lower case words; identifiers are also
// split at camel case and underscores ("OrderService" → order, service,
// orderservice).
func Tokenize(s string) []string {
	var out []string
	word := func(w string) {
		if len(w) < 2 {
			return
		}
		lw := strings.ToLower(w)
		out = append(out, lw)
		parts := splitCamel(w)
		if len(parts) > 1 {
			for _, p := range parts {
				if len(p) >= 2 {
					out = append(out, strings.ToLower(p))
				}
			}
		}
	}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		for _, p := range strings.Split(f, "_") {
			word(p)
		}
	}
	return out
}

func splitCamel(w string) []string {
	var parts []string
	start := 0
	rs := []rune(w)
	for i := 1; i < len(rs); i++ {
		if unicode.IsUpper(rs[i]) && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
			parts = append(parts, string(rs[start:i]))
			start = i
		}
	}
	return append(parts, string(rs[start:]))
}

// BM25 scores chunks for the query (Okapi BM25, k1 1.2, b 0.75; heading
// words count twice).
func BM25(chunks []Chunk, query string) map[string]float64 {
	q := Tokenize(query)
	out := map[string]float64{}
	if len(q) == 0 || len(chunks) == 0 {
		return out
	}
	const k1, b = 1.2, 0.75
	tfs := make([]map[string]int, len(chunks))
	lens := make([]int, len(chunks))
	df := map[string]int{}
	total := 0
	for i, c := range chunks {
		toks := Tokenize(c.Text + " " + c.Heading + " " + c.Heading)
		tf := map[string]int{}
		for _, t := range toks {
			tf[t]++
		}
		for t := range tf {
			df[t]++
		}
		tfs[i], lens[i] = tf, len(toks)
		total += len(toks)
	}
	avg := float64(total) / float64(len(chunks))
	n := float64(len(chunks))
	seen := map[string]bool{}
	for _, t := range q {
		if seen[t] {
			continue
		}
		seen[t] = true
		if df[t] == 0 {
			continue
		}
		idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
		for i, tf := range tfs {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			out[chunks[i].ID] += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(lens[i])/avg))
		}
	}
	return out
}
