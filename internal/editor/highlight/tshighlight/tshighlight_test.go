package tshighlight

import (
	"context"
	"errors"
	"math/rand"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

const samplePHP = `<?php
declare(strict_types=1);

namespace App\Mail;

use App\Queue\Job;

/**
 * Sends queued mails.
 */
final class Mailer implements Sender
{
    private const MAX_RETRIES = 3;

    public function __construct(private Logger $log) {}

    public function send(Message $msg): bool
    {
        $this->log->info("Sending to {$msg->to}");
        $ok = mail($msg->to, $msg->subject, $msg->body);
        return $ok && $msg->retries < self::MAX_RETRIES;
    }
}
?>
<p class="note">Done</p>
`

func wait(t *testing.T, h *Highlighter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Wait(ctx); err != nil {
		if errors.Is(err, syntax.ErrNoTreeSitter) {
			// Build without cgo (Windows client, D80): nothing to parse here,
			// TestWithoutTreeSitter covers this case.
			t.Skip("needs Tree-sitter (cgo)")
		}
		t.Fatalf("wait: %v", err)
	}
}

// TestWithoutTreeSitter: in a build without cgo a Tree-sitter language
// yields no spans and Wait reports ErrNoTreeSitter instead of hanging.
func TestWithoutTreeSitter(t *testing.T) {
	if syntax.TreeSitter {
		t.Skip("built with Tree-sitter")
	}
	b := buffer.NewString(samplePHP)
	h := New(b, Options{Path: "a.php"})
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Wait(ctx); !errors.Is(err, syntax.ErrNoTreeSitter) {
		t.Fatalf("wait: %v", err)
	}
	if sp := all(h, b); len(sp) != 0 {
		t.Fatalf("spans without Tree-sitter: %v", sp)
	}
}

// fresh returns the spans of a from-scratch parse of src.
func fresh(t *testing.T, lang syntax.Language, src []byte) []highlight.Span {
	t.Helper()
	d, err := syntax.NewDocument(context.Background(), lang, src)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var out []highlight.Span
	for _, sp := range d.Highlights(0, len(src)) {
		if r := Role(sp.Role); r != highlight.Plain {
			out = append(out, highlight.Span{Start: sp.Start, End: sp.End, Role: r})
		}
	}
	return out
}

func freshFolds(t *testing.T, lang syntax.Language, src []byte) []fold.Range {
	t.Helper()
	d, err := syntax.NewDocument(context.Background(), lang, src)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var out []fold.Range
	for _, r := range d.Folds() {
		out = append(out, fold.Range{StartLine: r.StartLine, EndLine: r.EndLine})
	}
	return out
}

func all(h *Highlighter, b *buffer.Buffer) []highlight.Span {
	s := b.Snapshot()
	return slices.Clone(h.Spans(s, 0, s.Len()))
}

func TestRolesCovered(t *testing.T) {
	seen := map[highlight.Role]bool{}
	for _, r := range syntax.Roles() {
		if _, ok := roles[r]; !ok {
			t.Errorf("syntax role %s not mapped", r)
		}
		hr := Role(r)
		if hr.String() != r.String() {
			t.Errorf("syntax role %s maps to %s", r, hr)
		}
		seen[hr] = true
	}
	for _, r := range highlight.Roles() {
		if !seen[r] {
			t.Errorf("highlight role %s is never produced", r)
		}
	}
	if Role(syntax.Role(200)) != highlight.Plain {
		t.Error("unknown role not plain")
	}
}

func TestPlainLanguage(t *testing.T) {
	b := buffer.NewString("just text")
	h := New(b, Options{Path: "notes.txt"})
	defer h.Close()
	if h.Language() != syntax.LangPlain {
		t.Fatalf("language %q", h.Language())
	}
	if sp := h.Spans(b.Snapshot(), 0, b.Len()); sp != nil {
		t.Fatalf("spans for plain text: %v", sp)
	}
	if err := h.Wait(context.Background()); err != ErrPlain {
		t.Fatalf("wait: %v", err)
	}
	h.Close() // idempotent
}

func TestDetectFromContent(t *testing.T) {
	b := buffer.NewString("<?php echo 1;\n")
	h := New(b, Options{})
	defer h.Close()
	if h.Language() != syntax.LangPHP {
		t.Fatalf("language %q", h.Language())
	}
	wait(t, h)
	if len(all(h, b)) == 0 {
		t.Fatal("no spans")
	}
}

func TestInitialParseMatchesFresh(t *testing.T) {
	b := buffer.NewString(samplePHP)
	var calls atomic.Int32
	h := New(b, Options{Path: "Mailer.php", Invalidate: func() { calls.Add(1) }})
	defer h.Close()
	wait(t, h)
	if !h.Ready() || !h.Idle() || h.Err() != nil {
		t.Fatal("not ready")
	}
	// Invalidate runs after the state change that ends Wait, so it may lag.
	for deadline := time.Now().Add(5 * time.Second); calls.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Error("invalidate not called")
	}
	got, want := all(h, b), fresh(t, syntax.LangPHP, []byte(samplePHP))
	if !slices.Equal(got, want) {
		t.Fatalf("spans differ:\n got %v\nwant %v", got, want)
	}
	// A sub-range answers from the cache with overlapping spans only.
	s := b.Snapshot()
	ls, le := s.LineStart(11), s.LineEnd(11)
	for _, sp := range h.Spans(s, ls, le) {
		if sp.End <= ls || sp.Start >= le {
			t.Errorf("span %v outside line [%d,%d)", sp, ls, le)
		}
	}
	// A stale snapshot is answered with plain text.
	old := s
	if _, err := b.Insert(0, []byte(" ")); err != nil {
		t.Fatal(err)
	}
	if h.Spans(old, 0, 10) != nil {
		t.Error("stale snapshot highlighted")
	}
}

func TestFolds(t *testing.T) {
	b := buffer.NewString(samplePHP)
	h := New(b, Options{Path: "a.php"})
	defer h.Close()
	wait(t, h)
	rs, ok := h.TakeFolds()
	if !ok || len(rs) == 0 {
		t.Fatalf("no folds: %v %v", rs, ok)
	}
	if want := freshFolds(t, syntax.LangPHP, []byte(samplePHP)); !slices.Equal(rs, want) {
		t.Fatalf("folds %v, want %v", rs, want)
	}
	if _, ok := h.TakeFolds(); ok {
		t.Error("unchanged folds delivered twice")
	}
	if all, ver, ok := h.Folds(); !ok || ver != b.Version() || len(all) != len(rs) {
		t.Error("Folds()")
	}
	// Adding a method adds a fold range.
	at := strings.Index(samplePHP, "    public function send")
	if _, err := b.Insert(at, []byte("    public function a(): void\n    {\n        echo 1;\n    }\n\n")); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.TakeFolds(); ok {
		t.Error("folds of the old text delivered after an edit")
	}
	wait(t, h)
	rs2, ok := h.TakeFolds()
	if !ok || len(rs2) != len(rs)+1 {
		t.Fatalf("folds after edit: %v (before %v)", rs2, rs)
	}
}

// edit applies a random edit to b, biased towards typing.
func edit(r *rand.Rand, b *buffer.Buffer) {
	n := b.Len()
	off := r.Intn(n + 1)
	snippets := []string{"$x", " ", "\n", "{", "}", "(", ")", "'", "\"", "/*", "*/", "function f() {}", "// c\n", "class A {}", "1.5", "?>", "<?php "}
	switch r.Intn(4) {
	case 0:
		if n > 0 {
			del := r.Intn(min(8, n-off) + 1)
			_, _ = b.Delete(off, del)
			return
		}
		fallthrough
	default:
		_, _ = b.Insert(off, []byte(snippets[r.Intn(len(snippets))]))
	}
}

func TestEditsMatchFreshParse(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	b := buffer.NewString(samplePHP)
	h := New(b, Options{Path: "x.php", Debounce: time.Millisecond})
	defer h.Close()
	wait(t, h)
	for round := 0; round < 30; round++ {
		for i := 0; i < 1+r.Intn(5); i++ {
			edit(r, b)
			// Spans in between must never panic or exceed the document.
			s := b.Snapshot()
			for _, sp := range h.Spans(s, 0, s.Len()) {
				if sp.Start < 0 || sp.End > s.Len() || sp.End <= sp.Start {
					t.Fatalf("bad span %v (len %d)", sp, s.Len())
				}
			}
		}
		wait(t, h)
		src := b.Bytes()
		if got, want := all(h, b), fresh(t, syntax.LangPHP, src); !slices.Equal(got, want) {
			t.Fatalf("round %d: incremental spans differ from fresh parse\nsrc: %q\n got %v\nwant %v", round, src, got, want)
		}
		if got, want := h.folds, freshFolds(t, syntax.LangPHP, src); !slices.Equal(got, want) {
			t.Fatalf("round %d: folds %v, want %v", round, got, want)
		}
	}
}

// TestConcurrentTyping types on the "UI" goroutine while the background
// goroutine parses (run with -race).
func TestConcurrentTyping(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	b := buffer.NewString(strings.Repeat(samplePHP, 20))
	h := New(b, Options{Path: "big.php", Debounce: time.Microsecond})
	defer h.Close()
	for i := 0; i < 400; i++ {
		edit(r, b)
		s := b.Snapshot()
		h.Spans(s, 0, min(s.Len(), 4096))
		h.TakeFolds()
		if i%50 == 0 {
			time.Sleep(time.Millisecond) // let parses finish in between
		}
	}
	wait(t, h)
	if got, want := all(h, b), fresh(t, syntax.LangPHP, b.Bytes()); !slices.Equal(got, want) {
		t.Fatal("spans differ from fresh parse after concurrent typing")
	}
}

func TestEditsDuringInitialParse(t *testing.T) {
	b := buffer.NewString(strings.Repeat(samplePHP, 50))
	h := New(b, Options{Path: "a.php", Debounce: time.Millisecond})
	defer h.Close()
	// Edit immediately, most likely before the first parse finished.
	for i := 0; i < 20; i++ {
		if _, err := b.Insert(6, []byte("$a = 1;\n")); err != nil {
			t.Fatal(err)
		}
	}
	wait(t, h)
	if got, want := all(h, b), fresh(t, syntax.LangPHP, b.Bytes()); !slices.Equal(got, want) {
		t.Fatal("spans differ after edits during the initial parse")
	}
}

func TestSpansShiftWhileParsePending(t *testing.T) {
	b := buffer.NewString("<?php\n$foo = 'bar';\n")
	h := New(b, Options{Path: "a.php", Debounce: time.Hour}) // never re-parse
	defer h.Close()
	wait(t, h)
	before := all(h, b)
	if _, err := b.Insert(0, []byte("\n\n")); err != nil {
		t.Fatal(err)
	}
	after := all(h, b)
	if len(after) != len(before) {
		t.Fatalf("span count changed: %v → %v", before, after)
	}
	for i := range before {
		if after[i].Start != before[i].Start+2 || after[i].Role != before[i].Role {
			t.Fatalf("span %d not shifted: %v → %v", i, before[i], after[i])
		}
	}
	// Typing at the end of a variable extends its color.
	at := strings.Index(b.String(), "$foo") + 4
	if _, err := b.Insert(at, []byte("d")); err != nil {
		t.Fatal(err)
	}
	var v highlight.Span
	for _, sp := range all(h, b) {
		if sp.Role == highlight.Variable {
			v = sp
		}
	}
	if v.End-v.Start != 5 {
		t.Fatalf("variable span %v not grown", v)
	}
	// A cache refill (new window) maps through the queued edits as well.
	shifted := all(h, b)
	h.mu.Lock()
	h.cache.valid = false
	h.mu.Unlock()
	if got := all(h, b); !slices.Equal(got, shifted) {
		t.Fatalf("refill %v differs from shifted cache %v", got, shifted)
	}
}

func TestEnvLanguage(t *testing.T) {
	b := buffer.NewString("# comment\nAPP_ENV=dev\n")
	h := New(b, Options{Path: ".env", Debounce: time.Millisecond})
	defer h.Close()
	if h.Language() != syntax.LangEnv {
		t.Fatalf("language %q", h.Language())
	}
	wait(t, h)
	if _, err := b.Insert(b.Len(), []byte("DEBUG=true\n")); err != nil {
		t.Fatal(err)
	}
	wait(t, h)
	if got, want := all(h, b), fresh(t, syntax.LangEnv, b.Bytes()); !slices.Equal(got, want) || len(got) == 0 {
		t.Fatalf("env spans %v, want %v", got, want)
	}
}

// TestDockerfileLanguage: lexed languages highlight in every build and
// follow edits (a heredoc opened by an edit restyles the following lines).
func TestDockerfileLanguage(t *testing.T) {
	b := buffer.NewString("FROM alpine\nRUN echo\nCMD x\n")
	h := New(b, Options{Path: "docker/Dockerfile.dev", Debounce: time.Millisecond})
	defer h.Close()
	if h.Language() != syntax.LangDockerfile {
		t.Fatalf("language %q", h.Language())
	}
	wait(t, h)
	if got := all(h, b); len(got) == 0 || got[0] != (highlight.Span{Start: 0, End: 4, Role: highlight.Keyword}) {
		t.Fatalf("spans %v", got)
	}
	if _, err := b.Insert(len("FROM alpine\nRUN"), []byte(" <<X")); err != nil {
		t.Fatal(err)
	}
	wait(t, h)
	if got, want := all(h, b), fresh(t, syntax.LangDockerfile, b.Bytes()); !slices.Equal(got, want) {
		t.Fatalf("spans %v, want %v", got, want)
	}
	// An explicit language (the tab's, from files.associations) wins.
	h2 := New(buffer.NewString("FROM x"), Options{Path: "image.txt", Language: syntax.LangDockerfile})
	defer h2.Close()
	if h2.Language() != syntax.LangDockerfile {
		t.Fatalf("explicit language %q", h2.Language())
	}
}

func TestCloseDuringParse(t *testing.T) {
	b := buffer.NewString(strings.Repeat(samplePHP, 200))
	h := New(b, Options{Path: "a.php"})
	h.Close()
	h.Close()
	if h.Spans(b.Snapshot(), 0, 10) != nil {
		t.Error("spans after close")
	}
	if _, err := b.Insert(0, []byte("x")); err != nil { // detached: no panic
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Wait(ctx); err != nil {
		t.Fatalf("wait after close: %v", err)
	}
}

func TestPositionMapping(t *testing.T) {
	q := queued{start: 10, oldEnd: 14, newEnd: 12} // replace 4 bytes by 2
	cases := []struct{ p, start, end int }{{5, 5, 5}, {10, 10, 10}, {12, 12, 12}, {14, 12, 12}, {20, 18, 18}}
	for _, c := range cases {
		if got := mapStart(c.p, q); got != c.start {
			t.Errorf("mapStart(%d) = %d, want %d", c.p, got, c.start)
		}
		if got := mapEnd(c.p, q); got != c.end {
			t.Errorf("mapEnd(%d) = %d, want %d", c.p, got, c.end)
		}
	}
	ins := queued{start: 10, oldEnd: 10, newEnd: 13}
	if unmapStart(11, ins) != 10 || unmapEnd(11, ins) != 10 || unmapStart(15, ins) != 12 || unmapEnd(5, ins) != 5 {
		t.Error("unmap")
	}
	spans := []highlight.Span{{Start: 0, End: 10, Role: highlight.Keyword}, {Start: 10, End: 14, Role: highlight.String}}
	got := shiftSpans(spans, q)
	if len(got) != 2 || got[0].End != 10 || got[1] != (highlight.Span{Start: 10, End: 12, Role: highlight.String}) {
		t.Errorf("replaced span: %v", got)
	}
	if got := shiftSpans([]highlight.Span{{Start: 11, End: 13}}, q); len(got) != 0 {
		t.Errorf("span inside deleted text kept: %v", got)
	}
	if &got[0] == &spans[0] {
		t.Error("shift mutated in place")
	}
	if w := window(nil, 0, 10); len(w) != 0 {
		t.Error("window of nil")
	}
}
