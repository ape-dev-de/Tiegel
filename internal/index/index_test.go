//go:build cgo

package index

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// extraFiles are written next to the fixture at test time: the fixture's
// .gitignore keeps them out of the repository.
var extraFiles = map[string]string{
	"vendor/acme/lib/src/Loggable.php": "<?php\nnamespace Acme\\Lib;\n\ninterface Loggable\n{\n    public function log(string $msg): void;\n}\n",
	"ignored/Hidden.php":               "<?php\nclass Hidden {}\n",
	"node_modules/x/y.php":             "<?php\nclass NodeThing {}\n",
	"README.md":                        "# not php\n",
}

// fixture copies testdata/project into a temp dir.
func fixture(t testing.TB) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "project")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range extraFiles {
		write(t, dst, rel, content)
	}
	return dst
}

func write(t testing.TB, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func build(t testing.TB, root string, opts Options) *Index {
	t.Helper()
	opts.Root = root
	ix := New(opts)
	t.Cleanup(ix.Close)
	if _, err := ix.Sync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return ix
}

// at returns path, text and the offset of the n-th occurrence (0-based) of
// marker in rel, plus delta bytes.
func at(t testing.TB, root, rel, marker string, delta int) (string, []byte, int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(src, []byte(marker))
	if i < 0 {
		t.Fatalf("%s: marker %q not found", rel, marker)
	}
	return p, src, i + delta
}

func fqns(locs []Location) []string {
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		out = append(out, l.FQN)
	}
	slices.Sort(out)
	return out
}

func wantFQNs(t *testing.T, what string, locs []Location, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	slices.Sort(want)
	if got := fqns(locs); !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func TestExtraction(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	st := ix.Stats()
	if st.Files != 12 || st.Libraries != 1 {
		t.Fatalf("stats %+v", st)
	}
	f, ok := ix.File(filepath.Join(root, "src/Shape/AbstractShape.php"))
	if !ok {
		t.Fatal("AbstractShape not indexed")
	}
	if len(f.Scopes) != 2 || f.Scopes[1].Namespace != `App\Shape` {
		t.Fatalf("scopes %+v", f.Scopes)
	}
	uses := f.Scopes[1].Uses
	want := []Use{
		{UseClass, "ShapeContract", `App\Contracts\Shape`},
		{UseClass, "Describes", `App\Util\Describes`},
		{UseClass, "H", `App\Util\Helper`},
		{UseClass, "Loggable", `Acme\Lib\Loggable`},
	}
	if !slices.Equal(uses, want) {
		t.Fatalf("uses %+v", uses)
	}
	c := f.Classes[0]
	if c.FQN != `App\Shape\AbstractShape` || !c.Abstract || c.Kind != KindClass ||
		!slices.Equal(c.Implements, []string{`App\Contracts\Shape`, `Acme\Lib\Loggable`}) ||
		!slices.Equal(c.Traits, []string{`App\Util\Describes`}) {
		t.Fatalf("class %+v", c)
	}
	var area, label, kind *Member
	for i := range c.Members {
		switch c.Members[i].Name {
		case "area":
			area = &c.Members[i]
		case "label":
			label = &c.Members[i]
		case "KIND":
			kind = &c.Members[i]
		}
	}
	if area == nil || !area.Abstract || area.Visibility != "public" || area.Detail != "(): float" {
		t.Fatalf("area %+v", area)
	}
	if label == nil || label.Kind != KindProperty || label.Visibility != "protected" || label.Detail != "string" {
		t.Fatalf("label %+v", label)
	}
	if kind == nil || kind.Kind != KindClassConst {
		t.Fatalf("KIND %+v", kind)
	}
	if area.NameRange.Start.Line != 14 || area.NameRange.Start.Col != 30 {
		t.Fatalf("area position %+v", area.NameRange.Start)
	}

	f, _ = ix.File(filepath.Join(root, "src/Model/Color.php"))
	e := f.Classes[0]
	if e.Kind != KindEnum || e.Detail != "string" || len(e.Members) != 4 || e.Members[0].Kind != KindEnumCase || !slices.Equal(e.Implements, []string{`App\Contracts\HasName`}) {
		t.Fatalf("enum %+v", e)
	}
	f, _ = ix.File(filepath.Join(root, "src/Util/Helper.php"))
	if len(f.Funcs) != 1 || f.Funcs[0].FQN != `App\Util\helper` || len(f.Consts) != 1 || f.Consts[0].FQN != `App\Util\VERSION` {
		t.Fatalf("functions/consts %+v %+v", f.Funcs, f.Consts)
	}
	f, _ = ix.File(filepath.Join(root, "src/Shape/Circle.php"))
	if m := f.Classes[0].Members; len(m) != 3 || m[1].Name != "r" || m[1].Kind != KindProperty || m[1].Visibility != "private" {
		t.Fatalf("promoted %+v", m)
	}
	if !f.Classes[0].Final {
		t.Fatal("final")
	}
	f, _ = ix.File(filepath.Join(root, "src/Contracts/Shape.php"))
	if c := f.Classes[0]; c.Kind != KindInterface || !slices.Equal(c.Extends, []string{`App\Contracts\HasName`, "Countable"}) {
		t.Fatalf("interface %+v", c)
	}
	if _, ok := ix.File(filepath.Join(root, "ignored/Hidden.php")); ok {
		t.Fatal(".gitignore not respected")
	}
	if _, ok := ix.File(filepath.Join(root, "vendor/acme/lib/src/Loggable.php")); !ok {
		t.Fatal("vendor should be indexed although ignored")
	}
}

func TestUseVariants(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.php", `<?php
namespace A {
    use X\Y as Z, P\Q;
    use function F\g, F\h as hh;
    use const C\K;
    use G\{A1, B\C2 as D2, function f3, const K3};
    use function G2\{g4, g5};
    class Foo extends Z implements Q\R, namespace\Local, \Glob {}
}
namespace {
    function top() {}
    class Bar extends Missing {}
}
`)
	ix := build(t, root, Options{})
	f, _ := ix.File(filepath.Join(root, "a.php"))
	if len(f.Scopes) != 3 {
		t.Fatalf("scopes %+v", f.Scopes)
	}
	got := f.Scopes[1].Uses
	want := []Use{
		{UseClass, "Z", `X\Y`}, {UseClass, "Q", `P\Q`},
		{UseFunction, "g", `F\g`}, {UseFunction, "hh", `F\h`},
		{UseConst, "K", `C\K`},
		{UseClass, "A1", `G\A1`}, {UseClass, "D2", `G\B\C2`}, {UseFunction, "f3", `G\f3`}, {UseConst, "K3", `G\K3`},
		{UseFunction, "g4", `G2\g4`}, {UseFunction, "g5", `G2\g5`},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("uses\n got %+v\nwant %+v", got, want)
	}
	c := f.Classes[0]
	if c.FQN != `A\Foo` || !slices.Equal(c.Extends, []string{`X\Y`}) || !slices.Equal(c.Implements, []string{`P\Q\R`, `A\Local`, `Glob`}) {
		t.Fatalf("class %+v", c)
	}
	if f.Classes[1].FQN != "Bar" || f.Funcs[0].FQN != "top" {
		t.Fatalf("global %+v %+v", f.Classes[1], f.Funcs)
	}
}

func TestDefinition(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	type tc struct {
		file, marker string
		delta        int
		want         []string
	}
	cases := []tc{
		{"src/Other/Usage.php", "new Circle", 5, []string{`App\Shape\Circle`}},
		{"src/Other/Usage.php", "Sq::unit", 1, []string{`App\Shape\Square`}},
		{"src/Other/Usage.php", "Sq::unit", 5, []string{`App\Shape\Square::unit`}},
		{"src/Other/Usage.php", "ShapeInterface $s", 3, []string{`App\Contracts\Shape`}},
		{"src/Other/Usage.php", "): Shape", 4, []string{`App\Model\Shape`}},
		{"src/Other/Usage.php", "new Shape()", 6, []string{`App\Model\Shape`}},
		{"src/Other/Usage.php", "helper(VERSION)", 2, []string{`App\Util\helper`}},
		{"src/Other/Usage.php", "VERSION);", 2, []string{`App\Util\VERSION`}},
		{"src/Other/Usage.php", "Color::Red", 8, []string{`App\Model\Color::Red`}},
		{"src/Other/Usage.php", "ShapeInterface::SIDES", 17, []string{`App\Contracts\Shape::SIDES`}},
		{"src/Other/Usage.php", `use App\Shape\Circle`, 16, []string{`App\Shape\Circle`}},
		{"src/Other/Usage.php", `use function App\Util\helper`, 25, []string{`App\Util\helper`}},
		{"src/Other/Usage.php", `NotFound`, 1, nil},
		{"src/Shape/AbstractShape.php", "$this->describe", 8, []string{`App\Util\Describes::describe`}},
		{"src/Shape/AbstractShape.php", "$this->area", 8, []string{`App\Shape\AbstractShape::area`}},
		{"src/Shape/AbstractShape.php", "self::KIND", 7, []string{`App\Shape\AbstractShape::KIND`}},
		{"src/Shape/AbstractShape.php", "$this->label", 8, []string{`App\Shape\AbstractShape::label`}},
		{"src/Shape/AbstractShape.php", "H::write", 0, []string{`App\Util\Helper`}},
		{"src/Shape/AbstractShape.php", "H::write", 4, []string{`App\Util\Helper::write`}},
		{"src/Shape/AbstractShape.php", "implements ShapeContract", 12, []string{`App\Contracts\Shape`}},
		{"src/Shape/AbstractShape.php", "Loggable\n", 2, []string{`Acme\Lib\Loggable`}},
		{"src/Shape/AbstractShape.php", "use Describes", 6, []string{`App\Util\Describes`}},
		{"src/Shape/AbstractShape.php", "static::class", 2, []string{`App\Shape\AbstractShape`}},
		{"src/Shape/Circle.php", "parent::count", 10, []string{`App\Shape\AbstractShape::count`}},
		{"src/Shape/Circle.php", "parent::count", 2, []string{`App\Shape\AbstractShape`}},
		{"src/Shape/Circle.php", "extends AbstractShape", 10, []string{`App\Shape\AbstractShape`}},
		{"src/Shape/Circle.php", "$this->r", 7, []string{`App\Shape\Circle::r`}},
		{"src/Shape/Square.php", "Contracts\\Shape $s", 12, []string{`App\Contracts\Shape`}},
		{"src/Shape/Square.php", "Contracts\\HasName", 1, []string{`App\Contracts\HasName`}},
		{"src/Shape/Square.php", "new static", 5, []string{`App\Shape\Square`}},
		{"src/Shape/RoundedSquare.php", "parent::area", 9, []string{`App\Shape\Square::area`}},
		{"src/Util/Describes.php", "$this->name", 8, nil}, // trait: no class context to resolve
		{"src/Util/Helper.php", "\\strlen", 2, nil},
		{"src/Util/Helper.php", "class Helper", 7, []string{`App\Util\Helper`}},
		{"src/Util/Helper.php", "function helper", 10, []string{`App\Util\helper`}},
		{"src/Util/Helper.php", "VERSION = ", 2, []string{`App\Util\VERSION`}},
		{"src/Model/Color.php", "self::Red", 7, []string{`App\Model\Color::Red`}},
		{"src/Model/Color.php", "case Blue", 6, []string{`App\Model\Color::Blue`}},
		{"src/Model/Color.php", "): self", 4, []string{`App\Model\Color`}},
		{"src/Model/Color.php", "$this->value", 8, nil},
		{"src/Shape/Square.php", "<?php", 1, nil},
	}
	for _, c := range cases {
		path, src, off := at(t, root, c.file, c.marker, c.delta)
		locs, err := ix.Definition(path, src, off)
		wantFQNs(t, c.file+" "+c.marker, locs, err, c.want...)
	}
	// the location points at the declaration name
	path, src, off := at(t, root, "src/Other/Usage.php", "new Circle", 5)
	locs, _ := ix.Definition(path, src, off)
	if l := locs[0]; !strings.HasSuffix(l.Path, filepath.FromSlash("src/Shape/Circle.php")) || l.NameRange.Start.Line != 4 || l.NameRange.Start.Col != 13 || l.Kind != KindClass {
		t.Fatalf("location %+v", l)
	}
	// the cursor right behind a name
	path, src, off = at(t, root, "src/Other/Usage.php", "new Circle(", 10)
	locs, _ = ix.Definition(path, src, off)
	wantFQNs(t, "end of name", locs, nil, `App\Shape\Circle`)
	if _, err := ix.Definition(path, src, len(src)+5); err == nil {
		t.Fatal("offset out of range")
	}
}

func TestImplementations(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	cases := []struct {
		file, marker string
		delta        int
		want         []string
	}{
		// interface → implementing classes, transitively through parents
		{"src/Contracts/Shape.php", "interface Shape", 10, []string{`App\Shape\AbstractShape`, `App\Shape\Circle`, `App\Shape\Square`, `App\Shape\RoundedSquare`}},
		// interface inherited by another interface and an enum
		{"src/Contracts/HasName.php", "interface HasName", 10, []string{`App\Shape\AbstractShape`, `App\Shape\Circle`, `App\Shape\Square`, `App\Shape\RoundedSquare`, `App\Model\Color`}},
		// abstract class → subclasses
		{"src/Shape/AbstractShape.php", "class AbstractShape", 6, []string{`App\Shape\Circle`, `App\Shape\Square`, `App\Shape\RoundedSquare`}},
		// interface method → implementing methods
		{"src/Contracts/Shape.php", "function area", 9, []string{`App\Shape\AbstractShape::area`, `App\Shape\Circle::area`, `App\Shape\Square::area`, `App\Shape\RoundedSquare::area`}},
		{"src/Contracts/HasName.php", "function name", 9, []string{`App\Shape\AbstractShape::name`, `App\Shape\Square::name`, `App\Model\Color::name`}},
		// abstract method
		{"src/Shape/AbstractShape.php", "function area", 9, []string{`App\Shape\Circle::area`, `App\Shape\Square::area`, `App\Shape\RoundedSquare::area`}},
		// from a call site
		{"src/Shape/AbstractShape.php", "$this->area", 8, []string{`App\Shape\Circle::area`, `App\Shape\Square::area`, `App\Shape\RoundedSquare::area`}},
		// trait → users
		{"src/Util/Describes.php", "trait Describes", 6, []string{`App\Shape\AbstractShape`}},
		// vendor interface implemented in the project
		{"vendor/acme/lib/src/Loggable.php", "function log", 9, []string{`App\Shape\AbstractShape::log`}},
		// same short name, other namespace: unrelated
		{"src/Model/Shape.php", "class Shape", 6, nil},
		{"src/Model/Shape.php", "function area", 9, nil},
		{"src/Shape/Circle.php", "final class Circle", 12, nil},
		{"src/Util/Helper.php", "function helper", 10, nil},
	}
	for _, c := range cases {
		path, src, off := at(t, root, c.file, c.marker, c.delta)
		locs, err := ix.Implementations(path, src, off)
		wantFQNs(t, "impl "+c.file+" "+c.marker, locs, err, c.want...)
	}
	wantFQNs(t, "ClassImplementations", ix.ClassImplementations(`\App\Shape\Square`), nil, `App\Shape\RoundedSquare`)
}

func TestSuper(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	cases := []struct {
		file, marker string
		delta        int
		want         []string
	}{
		{"src/Shape/RoundedSquare.php", "function area", 9, []string{`App\Shape\Square::area`, `App\Contracts\Shape::area`}},
		{"src/Shape/Square.php", "function name", 9, []string{`App\Shape\AbstractShape::name`, `App\Contracts\HasName::name`}},
		{"src/Shape/AbstractShape.php", "function log", 9, []string{`Acme\Lib\Loggable::log`}},
		{"src/Model/Color.php", "function name", 9, []string{`App\Contracts\HasName::name`}},
		{"src/Shape/Circle.php", "class Circle", 6, []string{`App\Shape\AbstractShape`}},
		{"src/Shape/AbstractShape.php", "class AbstractShape", 6, []string{`App\Contracts\Shape`, `Acme\Lib\Loggable`}},
		{"src/Model/Shape.php", "function area", 9, nil},
	}
	for _, c := range cases {
		path, src, off := at(t, root, c.file, c.marker, c.delta)
		locs, err := ix.Super(path, src, off)
		wantFQNs(t, "super "+c.file+" "+c.marker, locs, err, c.want...)
	}
}

func TestClassAtAndHierarchy(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	for _, c := range []struct {
		file, marker string
		delta        int
		want         string
	}{
		{"src/Shape/Square.php", "return $this->side", 0, `App\Shape\Square`},
		{"src/Shape/Square.php", "class Square", 7, `App\Shape\Square`},
		{"src/Shape/RoundedSquare.php", "parent::area", 9, `App\Shape\Square`},
		{"src/Other/Usage.php", "new Circle", 5, `App\Shape\Circle`},
		{"src/Other/Usage.php", "<?php", 0, ""},
	} {
		path, src, off := at(t, root, c.file, c.marker, c.delta)
		if got, err := ix.ClassAt(path, src, off); err != nil || got != c.want {
			t.Errorf("ClassAt %s %q = %q, %v; want %q", c.file, c.marker, got, err, c.want)
		}
	}

	h, ok := ix.TypeHierarchy(`app\shape\square`)
	if !ok || h.Class.FQN != `App\Shape\Square` {
		t.Fatalf("hierarchy %+v", h)
	}
	if len(h.Supertypes) != 1 || h.Supertypes[0].FQN != `App\Shape\AbstractShape` {
		t.Fatalf("supertypes %+v", h.Supertypes)
	}
	up := h.Supertypes[0].Children
	if len(up) != 2 || up[0].FQN != `App\Contracts\Shape` || up[1].FQN != `Acme\Lib\Loggable` {
		t.Fatalf("supertypes level 2 %+v", up)
	}
	// \Countable is not indexed but listed
	var countable bool
	for _, n := range up[0].Children {
		if n.FQN == "Countable" && n.Path == "" {
			countable = true
		}
	}
	if !countable {
		t.Fatalf("unindexed supertype missing: %+v", up[0].Children)
	}
	if len(h.Subtypes) != 1 || h.Subtypes[0].FQN != `App\Shape\RoundedSquare` {
		t.Fatalf("subtypes %+v", h.Subtypes)
	}
	h, _ = ix.TypeHierarchy(`App\Contracts\HasName`)
	if len(h.Subtypes) != 2 || len(h.Subtypes[0].Children) != 1 { // Color; Shape interface → AbstractShape
		var names []string
		for _, s := range h.Subtypes {
			names = append(names, s.FQN)
		}
		t.Fatalf("HasName subtypes %q", names)
	}
	if _, ok := ix.TypeHierarchy("Nope"); ok {
		t.Fatal("unknown class")
	}
}

func TestGutter(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	marks := ix.Gutter(filepath.Join(root, "src/Shape/Square.php"))
	byName := map[string]GutterMark{}
	for _, m := range marks {
		byName[m.FQN] = m
	}
	sq := byName[`App\Shape\Square`]
	if sq.Line != 6 || len(sq.Up) != 1 || len(sq.Down) != 1 || sq.Down[0].FQN != `App\Shape\RoundedSquare` {
		t.Fatalf("class mark %+v", sq)
	}
	area := byName[`App\Shape\Square::area`]
	if area.Line != 10 || len(area.Up) != 2 || len(area.Down) != 1 || area.DownTotal != 1 {
		t.Fatalf("area mark %+v", area)
	}
	if _, ok := byName[`App\Shape\Square::unit`]; ok {
		t.Fatal("unit has no relations")
	}
	if _, ok := byName[`App\Shape\Square::name`]; !ok {
		t.Fatal("name overrides")
	}
	for i := 1; i < len(marks); i++ {
		if marks[i].Line < marks[i-1].Line {
			t.Fatal("marks not sorted")
		}
	}
	if ix.Gutter("/nope.php") != nil {
		t.Fatal("unknown file")
	}
	// the trait method is "up" for nothing but "down" for nothing either;
	// the interface has implementations
	marks = ix.Gutter(filepath.Join(root, "src/Contracts/Shape.php"))
	if len(marks) != 2 || marks[0].DownTotal != 4 || len(marks[0].Up) != 1 {
		t.Fatalf("interface marks %+v", marks)
	}
}

func TestSymbols(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	res := ix.Symbols(SymbolQuery{Query: "shape", Kinds: []Kind{KindClass, KindInterface}})
	if len(res) < 3 || !strings.EqualFold(res[0].Name, "Shape") {
		t.Fatalf("shape: %+v", res)
	}
	res = ix.Symbols(SymbolQuery{Query: "RS", Kinds: []Kind{KindClass}})
	if len(res) == 0 || res[0].FQN != `App\Shape\RoundedSquare` {
		t.Fatalf("camel humps: %+v", res)
	}
	res = ix.Symbols(SymbolQuery{Query: `contracts\shape`})
	if len(res) == 0 || res[0].FQN != `App\Contracts\Shape` {
		t.Fatalf("fqn: %+v", res)
	}
	res = ix.Symbols(SymbolQuery{Query: "Square::ar"})
	if len(res) == 0 || res[0].FQN != `App\Shape\Square::area` {
		t.Fatalf("scoped: %+v", res)
	}
	res = ix.Symbols(SymbolQuery{Query: "log"})
	if len(res) == 0 {
		t.Fatal("log")
	}
	res = ix.Symbols(SymbolQuery{Query: "log", NoLibraries: true})
	for _, r := range res {
		if r.Library {
			t.Fatalf("library symbol %+v", r)
		}
	}
	res = ix.Symbols(SymbolQuery{Query: "", Limit: 3})
	if len(res) != 3 {
		t.Fatalf("limit %d", len(res))
	}
	if res := ix.Symbols(SymbolQuery{Query: "zzzzqqq"}); len(res) != 0 {
		t.Fatalf("no match %+v", res)
	}
	res = ix.Symbols(SymbolQuery{Query: "$label"})
	if len(res) != 1 || res[0].Kind != KindProperty {
		t.Fatalf("property %+v", res)
	}
	if fuzzyScore("abc", "abc", "abc") <= fuzzyScore("abc", "abcd", "abcd") {
		t.Fatal("exact before prefix")
	}
	if fuzzyScore("us", "userservice", "UserService") <= fuzzyScore("us", "abusing", "abusing") {
		t.Fatal("word starts")
	}
	if fuzzyScore("ü", "größe", "Größe") >= 0 || fuzzyScore("x", "", "") != -1 {
		t.Fatal("no match")
	}
}

func TestIncremental(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	ctx := context.Background()
	impls := func() []string {
		path, src, off := at(t, root, "src/Contracts/HasName.php", "interface HasName", 10)
		locs, err := ix.Implementations(path, src, off)
		if err != nil {
			t.Fatal(err)
		}
		return fqns(locs)
	}
	before := impls()

	// a new file implementing the interface
	write(t, root, "src/Model/Person.php", "<?php\nnamespace App\\Model;\n\nclass Person implements \\App\\Contracts\\HasName\n{\n    public function name(): string { return ''; }\n}\n")
	res, err := ix.Sync(ctx, nil)
	if err != nil || len(res.Changed) != 1 {
		t.Fatalf("sync %+v %v", res, err)
	}
	if got := impls(); !slices.Contains(got, `App\Model\Person`) {
		t.Fatalf("Person missing: %q", got)
	}
	gen := ix.Generation()
	// an unchanged second sync parses nothing
	if res, _ := ix.Sync(ctx, nil); len(res.Changed) != 0 || len(res.Removed) != 0 || ix.Generation() != gen {
		t.Fatalf("idle sync %+v", res)
	}

	// edit: the class no longer implements the interface
	p := filepath.Join(root, "src/Model/Person.php")
	write(t, root, "src/Model/Person.php", "<?php\nnamespace App\\Model;\n\nclass Person\n{\n    public function name(): string { return ''; }\n}\n")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, future, future)
	if changed, err := ix.UpdateFile(p); err != nil || !changed {
		t.Fatalf("UpdateFile %v %v", changed, err)
	}
	if got := impls(); !slices.Equal(got, before) {
		t.Fatalf("after edit %q, want %q", got, before)
	}
	if changed, _ := ix.UpdateFile(p); changed {
		t.Fatal("same content reported as change")
	}

	// unsaved document text
	changed, err := ix.UpdateText(p, []byte("<?php\nnamespace App\\Model;\nclass Person extends \\App\\Shape\\Square {}\n"))
	if err != nil || !changed {
		t.Fatal(err)
	}
	if got := ix.ClassImplementations(`App\Shape\Square`); len(got) != 2 {
		t.Fatalf("doc text %+v", got)
	}
	if changed, _ := ix.UpdateText(p, []byte("<?php\nnamespace App\\Model;\nclass Person extends \\App\\Shape\\Square {}\n")); changed {
		t.Fatal("same text")
	}
	// a Sync keeps the document state (disk unchanged)
	if res, _ := ix.Sync(ctx, nil); len(res.Changed) != 0 {
		t.Fatalf("sync overwrote doc text %+v", res)
	}
	// back to disk
	if changed, _ := ix.UpdateFile(p); !changed {
		t.Fatal("revert")
	}
	if got := ix.ClassImplementations(`App\Shape\Square`); len(got) != 1 {
		t.Fatalf("reverted %+v", got)
	}

	// deletion
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	res, _ = ix.Sync(ctx, nil)
	if len(res.Removed) != 1 || res.Removed[0] != p {
		t.Fatalf("removed %+v", res)
	}
	if _, ok := ix.File(p); ok {
		t.Fatal("still indexed")
	}
	if changed, _ := ix.UpdateFile(p); changed {
		t.Fatal("missing file")
	}
	if ix.Remove(p) {
		t.Fatal("remove twice")
	}
	// non-php file
	write(t, root, "src/x.txt", "class X {}")
	if changed, _ := ix.UpdateFile(filepath.Join(root, "src/x.txt")); changed {
		t.Fatal("txt indexed")
	}
	// a new file through UpdateText (not indexed before)
	np := filepath.Join(root, "src/New.php")
	write(t, root, "src/New.php", "<?php class NewOne {}")
	if changed, _ := ix.UpdateText(np, []byte("<?php class NewTwo {}")); !changed {
		t.Fatal("new via text")
	}
	if r := ix.Symbols(SymbolQuery{Query: "NewTwo"}); len(r) != 1 {
		t.Fatal("NewTwo")
	}
}

func TestOptions(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{SkipLibraries: true, IncludeIgnored: true, Exclude: []string{"src/Model", "Usage.php"}})
	if _, ok := ix.File(filepath.Join(root, "vendor/acme/lib/src/Loggable.php")); ok {
		t.Fatal("vendor indexed")
	}
	if _, ok := ix.File(filepath.Join(root, "ignored/Hidden.php")); !ok {
		t.Fatal("ignored not indexed")
	}
	if _, ok := ix.File(filepath.Join(root, "src/Model/Color.php")); ok {
		t.Fatal("excluded dir")
	}
	if _, ok := ix.File(filepath.Join(root, "src/Other/Usage.php")); ok {
		t.Fatal("excluded file")
	}
	if _, ok := ix.File(filepath.Join(root, "node_modules/x/y.php")); ok {
		t.Fatal("node_modules")
	}
	if changed, _ := ix.UpdateFile(filepath.Join(root, "src/Model/Color.php")); changed {
		t.Fatal("UpdateFile ignores excludes")
	}
	if changed, _ := ix.UpdateFile(filepath.Join(root, "vendor/acme/lib/src/Loggable.php")); changed {
		t.Fatal("UpdateFile ignores SkipLibraries")
	}
	if ix.Options().Workers <= 0 || !ix.Accepts("A.PHP") || ix.Accepts("a.js") {
		t.Fatal("options")
	}
	ix2 := New(Options{Root: filepath.Join(root, "missing")})
	defer ix2.Close()
	if _, err := ix2.Sync(context.Background(), nil); err == nil {
		t.Fatal("missing root")
	}
	f := filepath.Join(root, "src/Util/Helper.php")
	ix3 := New(Options{Root: f})
	defer ix3.Close()
	if _, err := ix3.Sync(context.Background(), nil); err == nil {
		t.Fatal("root is a file")
	}
}

func TestCache(t *testing.T) {
	root := fixture(t)
	ix := build(t, root, Options{})
	dir := t.TempDir()
	if err := ix.Save(dir); err != nil {
		t.Fatal(err)
	}
	ix2 := New(Options{Root: root})
	defer ix2.Close()
	if err := ix2.Load(dir); err != nil {
		t.Fatal(err)
	}
	if ix2.Stats() != ix.Stats() {
		t.Fatalf("stats %+v vs %+v", ix2.Stats(), ix.Stats())
	}
	res, err := ix2.Sync(context.Background(), nil)
	if err != nil || len(res.Changed) != 0 || len(res.Removed) != 0 {
		t.Fatalf("sync after load %+v %v", res, err)
	}
	if got := ix2.ClassImplementations(`App\Shape\AbstractShape`); len(got) != 3 {
		t.Fatalf("after load %+v", got)
	}
	// load replaces existing content
	if err := ix2.Load(dir); err != nil || ix2.Stats() != ix.Stats() {
		t.Fatal("reload")
	}
	// mismatches
	if err := New(Options{Root: root, SkipLibraries: true}).Load(dir); err != ErrCacheMismatch {
		t.Fatalf("options: %v", err)
	}
	if err := New(Options{Root: t.TempDir()}).Load(dir); err != ErrCacheMismatch {
		t.Fatalf("root: %v", err)
	}
	if err := New(Options{Root: root}).Load(t.TempDir()); err == nil {
		t.Fatal("missing cache")
	}
	write(t, dir, CacheFile, "garbage")
	if err := New(Options{Root: root}).Load(dir); err == nil {
		t.Fatal("garbage cache")
	}
	if err := ix.Save(filepath.Join(dir, CacheFile, "sub")); err == nil {
		t.Fatal("save below a file")
	}
}

func TestSyncProgressAndCancel(t *testing.T) {
	root := fixture(t)
	ix := New(Options{Root: root, Workers: 2})
	defer ix.Close()
	var phases []string
	if _, err := ix.Sync(context.Background(), func(p Progress) { phases = append(phases, p.Phase) }); err != nil {
		t.Fatal(err)
	}
	if phases[0] != "scanning" || phases[len(phases)-1] != "ready" {
		t.Fatalf("phases %q", phases)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ix2 := New(Options{Root: root})
	defer ix2.Close()
	if _, err := ix2.Sync(ctx, nil); err == nil {
		t.Fatal("cancelled sync")
	}
	ix2.Close()
	if _, err := ix2.UpdateText(filepath.Join(root, "a.php"), []byte("<?php")); err == nil {
		t.Fatal("closed index parses")
	}
	if _, err := ix2.Definition("a.php", []byte("<?php"), 0); err == nil {
		t.Fatal("closed index locates")
	}
}
