package mermaid

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden SVGs: TIEGEL_UPDATE_GOLDEN=1 go test ./internal/mermaid rewrites
// testdata/*.svg from testdata/*.mmd.

func TestGoldenSVG(t *testing.T) {
	files, err := filepath.Glob("testdata/*.mmd")
	if err != nil || len(files) < 8 {
		t.Fatalf("testdata: %v %d", err, len(files))
	}
	update := os.Getenv("TIEGEL_UPDATE_GOLDEN") != ""
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Render(string(src), Options{})
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		got := d.SVG(LightTheme)
		golden := strings.TrimSuffix(f, ".mmd") + ".svg"
		if update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%s: %v (run with TIEGEL_UPDATE_GOLDEN=1)", golden, err)
		}
		if got != string(want) {
			t.Errorf("%s differs from the golden SVG (run with TIEGEL_UPDATE_GOLDEN=1 and review)", f)
		}
	}
}

func renderFile(t *testing.T, name string) *Diagram {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Render(string(src), Options{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

// TestLayoutInvariants checks every graph diagram: nodes do not overlap,
// edges start and end on node borders, everything is inside the canvas,
// and the layout is deterministic.
func TestLayoutInvariants(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.mmd")
	for _, f := range files {
		name := filepath.Base(f)
		d := renderFile(t, name)
		again := renderFile(t, name)
		a, _ := json.Marshal(d)
		b, _ := json.Marshal(again)
		if string(a) != string(b) {
			t.Errorf("%s: layout is not deterministic", name)
		}
		if d.Width <= 0 || d.Height <= 0 {
			t.Errorf("%s: size %vx%v", name, d.Width, d.Height)
		}
		for _, it := range d.Items {
			for _, s := range it.Path {
				for _, p := range s.Pts {
					if p.X < -0.5 || p.Y < -0.5 || p.X > d.Width+0.5 || p.Y > d.Height+0.5 {
						t.Errorf("%s: point %v outside %vx%v", name, p, d.Width, d.Height)
					}
				}
			}
		}
		if d.Kind == "sequence" || d.Kind == "pie" {
			continue
		}
		var boxes []NodeInfo
		byID := map[string]NodeInfo{}
		for _, n := range d.Nodes {
			if n.Shape == "subgraph" || n.Shape == "cluster" {
				continue
			}
			boxes = append(boxes, n)
			byID[n.ID] = n
		}
		for i := range boxes {
			for j := i + 1; j < len(boxes); j++ {
				if boxes[i].Box.Overlaps(boxes[j].Box) {
					t.Errorf("%s: nodes %s and %s overlap", name, boxes[i].ID, boxes[j].ID)
				}
			}
		}
		for _, e := range d.Edges {
			if e.From == e.To || len(e.Points) < 2 {
				continue
			}
			for _, end := range []struct {
				id string
				p  Point
			}{{e.From, e.Points[0]}, {e.To, e.Points[len(e.Points)-1]}} {
				n, ok := byID[end.id]
				if !ok {
					continue
				}
				if !onBorder(n, end.p) {
					t.Errorf("%s: edge %s→%s end %v not on the border of %s %+v", name, e.From, e.To, end.p, n.Shape, n.Box)
				}
			}
		}
	}
}

// onBorder reports whether p lies on the outline of n (within 1.5 units).
func onBorder(n NodeInfo, p Point) bool {
	b := n.Box
	const eps = 1.5
	inX := p.X >= b.X-eps && p.X <= b.X+b.W+eps
	inY := p.Y >= b.Y-eps && p.Y <= b.Y+b.H+eps
	if !inX || !inY {
		return false
	}
	c := b.Center()
	dx, dy := math.Abs(p.X-c.X)/(b.W/2), math.Abs(p.Y-c.Y)/(b.H/2)
	switch n.Shape {
	case shapeDiamond:
		return math.Abs(dx+dy-1) < 0.06
	case shapeCircle, shapeDoubleCircle, shapeStart, shapeEnd:
		return math.Abs(math.Hypot(dx, dy)-1) < 0.06
	}
	// Rectangular outlines: on an edge of the box.
	return math.Abs(p.X-b.X) < eps || math.Abs(p.X-b.X-b.W) < eps || math.Abs(p.Y-b.Y) < eps || math.Abs(p.Y-b.Y-b.H) < eps
}

func TestFlowchartParsing(t *testing.T) {
	d, err := Render(`graph LR;
  A[Start]-->B(Round) & C{Check}
  B -- text label --> D
  C -.->|dotted| D
  C ==> E((Circle)):::hot
  D --o F
  E --x F
  F <--> A
  G~~~H
  I@{ shape: cyl, label: "Store" }
  %% comment
  click A callback
  linkStyle 0 stroke:#ff0000
  classDef hot fill:#f00
  style B fill:#00ff00,stroke-dasharray: 5 5`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]string{}
	labels := map[string]string{}
	for _, n := range d.Nodes {
		shapes[n.ID], labels[n.ID] = n.Shape, n.Label
	}
	if shapes["A"] != shapeRect || shapes["B"] != shapeRound || shapes["C"] != shapeDiamond ||
		shapes["E"] != shapeCircle || shapes["I"] != shapeCylinder || labels["I"] != "Store" || labels["A"] != "Start" {
		t.Fatalf("shapes %v labels %v", shapes, labels)
	}
	var got []string
	for _, e := range d.Edges {
		got = append(got, e.From+">"+e.To+":"+e.Label)
	}
	want := "A>B:,A>C:,B>D:text label,C>D:dotted,C>E:,D>F:,E>F:,F>A:,G>H:"
	if strings.Join(got, ",") != want {
		t.Fatalf("edges\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	svg := d.SVG(DarkTheme)
	for _, s := range []string{"#ff0000", "#00ff00", "stroke-dasharray"} {
		if !strings.Contains(svg, s) {
			t.Errorf("svg lacks %s", s)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]int{
		"":                                    0,
		"gantt\n  title x":                    1,
		"flowchart XY\nA-->B":                 1,
		"flowchart TD\n  A-->B\n  B-->":       3,
		"flowchart TD\n  subgraph x\n  A":     3,
		"flowchart TD\n  end":                 2,
		"flowchart TD\n  A[\"open":            2,
		"flowchart TD\n  A -->|x B":           2,
		"sequenceDiagram\n  A->>B: hi\n  end": 3,
		"sequenceDiagram\n  loop x\n  A->>B":  3,
		"sequenceDiagram\n  Note over A":      2,
		"sequenceDiagram\n  A something B":    2,
		"sequenceDiagram\n  else":             2,
		"sequenceDiagram\n  activate":         2,
		"sequenceDiagram\n  Note behind A: x": 2,
		"stateDiagram-v2\n  }":                2,
		"stateDiagram-v2\n  state X {":        2,
		"classDiagram\n  class A {\n  +x":     2,
		"classDiagram\n  what is this":        2,
		"erDiagram\n  A {\n  int x":           2,
		"erDiagram\n  A something B":          2,
		"pie\n  \"a\" : x":                    2,
		"pie\n  title only":                   1,
	}
	for src, line := range cases {
		_, err := Render(src, Options{})
		var me *Error
		if !errors.As(err, &me) {
			t.Errorf("%q: error %v is not a *Error", src, err)
			continue
		}
		if me.Line != line {
			t.Errorf("%q: line %d, want %d (%v)", src, me.Line, line, me)
		}
		if me.Error() == "" {
			t.Error("empty message")
		}
	}
}

func TestKind(t *testing.T) {
	for src, want := range map[string]string{
		"---\ntitle: T\n---\nflowchart TD\nA": "flowchart", "%% c\nsequenceDiagram": "sequence",
		"stateDiagram": "state", "classDiagram": "class", "erDiagram": "er", "pie": "pie", "gitGraph": "", "": "",
	} {
		if got := Kind(src); got != want {
			t.Errorf("Kind(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestFrontMatterTitle(t *testing.T) {
	d, err := Render("---\ntitle: My Flow\n---\nflowchart TD\nA-->B", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.SVG(LightTheme), ">My Flow<") {
		t.Fatal("title missing")
	}
}

func TestSequenceModel(t *testing.T) {
	d := renderFile(t, "seq_basic.mmd")
	if len(d.Nodes) != 4 || d.Nodes[0].Shape != "actor" || d.Nodes[1].Label != "Browser" {
		t.Fatalf("participants %+v", d.Nodes)
	}
	if len(d.Edges) != 10 {
		t.Fatalf("%d messages", len(d.Edges))
	}
	// Messages go downwards in source order.
	for i := 1; i < len(d.Edges); i++ {
		if d.Edges[i].Points[0].Y <= d.Edges[i-1].Points[0].Y {
			t.Errorf("message %d not below %d", i, i-1)
		}
	}
	svg := d.SVG(LightTheme)
	for _, s := range []string{">alt<", ">[invalid]<", ">loop<", ">par<", ">10<", ">indexed lookup<"} {
		if !strings.Contains(svg, s) {
			t.Errorf("svg lacks %s", s)
		}
	}
}

func TestSequenceExtras(t *testing.T) {
	d, err := Render(`sequenceDiagram
  title: Extras
  box Aqua Group
    participant A as Alice
    participant B
  end
  create participant C
  autonumber 10 5
  A->>C: x
  rect rgb(200, 220, 255)
    A-xB: lost
  end
  critical ok
    B--)A: async
  option bad
    B<<->>A: both
  end
  break stop
    A->B: plain
  end
  opt maybe
    A-->B: dashed
  end
  Note left of A: left note
  activate B
  B->>B: self
  deactivate B
  destroy C
  autonumber off`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	svg := d.SVG(LightTheme)
	for _, s := range []string{"#00ffff", "#c8dcff", ">10<", ">15<", ">Group<", ">Extras<", ">critical<", ">left note<"} {
		if !strings.Contains(svg, s) {
			t.Errorf("svg lacks %s", s)
		}
	}
}

func TestStateClassERPie(t *testing.T) {
	d := renderFile(t, "state_basic.mmd")
	kinds := map[string]bool{}
	for _, n := range d.Nodes {
		kinds[n.Shape] = true
	}
	for _, k := range []string{"start", "end", shapeDiamond, "note", "cluster", shapeRound} {
		if !kinds[k] {
			t.Errorf("state diagram lacks %s (%v)", k, kinds)
		}
	}
	c := renderFile(t, "class_basic.mmd")
	svg := c.SVG(LightTheme)
	for _, s := range []string{"«abstract»", "Repository&lt;T&gt;", "+find(id) T", ">houses<", ">*<", `font-style="italic"`} {
		if !strings.Contains(svg, s) {
			t.Errorf("class svg lacks %s", s)
		}
	}
	er := renderFile(t, "er_basic.mmd")
	if len(er.Nodes) != 5 || len(er.Edges) != 4 || !strings.Contains(er.SVG(LightTheme), ">street and city<") {
		t.Errorf("er %d nodes %d edges", len(er.Nodes), len(er.Edges))
	}
	p := renderFile(t, "pie_basic.mmd")
	if len(p.Nodes) != 4 || !strings.Contains(p.SVG(LightTheme), ">46.3%<") {
		t.Errorf("pie %+v", p.Nodes)
	}
	full, err := Render("pie\n\"all\" : 3", Options{})
	if err != nil || len(full.Nodes) != 1 {
		t.Fatalf("full pie: %v", err)
	}
}

func TestMoreFlowchartFeatures(t *testing.T) {
	d, err := Render(`flowchart BT
  subgraph outer [Outer]
    direction LR
    subgraph inner
      a --> b
    end
    c
  end
  outer --> d
  d --> d
  e[/"Quoted / slash"/] --> f[\x/]
  e -- long text that is quite wide indeed and wraps --> f
  class a,zz hot
  style zz fill:red
  zz --> a
  linkStyle default stroke:blue,stroke-width:3px`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var clusters int
	for _, n := range d.Nodes {
		if n.Shape == "subgraph" {
			clusters++
		}
	}
	if clusters != 2 {
		t.Fatalf("%d subgraphs", clusters)
	}
	for _, e := range d.Edges {
		if e.From == "outer" || e.To == "outer" {
			t.Fatalf("edge to a subgraph not redirected: %+v", e)
		}
	}
	for _, dir := range []string{"RL", "LR", "TB", "BT"} {
		if _, err := Render("flowchart "+dir+"\n subgraph s\n a{{A}} --> b([B])\n end\n b --> c[(C)]\n c --> a\n c --> c", Options{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStyleParsing(t *testing.T) {
	for in, want := range map[string]string{
		"#abc": "#aabbcc", "#AABBCC": "#aabbcc", "#11223344": "#11223344", "red": "#ff0000",
		"rgb(255, 0, 10)": "#ff000a", "rgba(0,0,0,0.5)": "#00000080", "rgb(100%, 0%, 0%)": "#ff0000",
		"#zzz": "", "nope": "", "rgb(1,2)": "", "": "", "#12345": "", "white !important": "#ffffff",
	} {
		if got := parseColor(in); got != want {
			t.Errorf("parseColor(%q) = %q, want %q", in, got, want)
		}
	}
	st := parseStyle("fill:rgb(1,2,3), stroke:#000;stroke-width:4px,color:#fff, bogus")
	if st.color("fill") != "#010203" || st.width() != 4 || st.color("color") != "#ffffff" {
		t.Fatalf("style %v", st)
	}
	if contrastText("#ffffff") != "#1f2328" || contrastText("#000000") != "#ffffff" || contrastText("x") != "" {
		t.Fatal("contrast")
	}
	if _, _, _, _, ok := RGBA("#12"); ok {
		t.Fatal("RGBA accepted junk")
	}
}

func TestLabelLines(t *testing.T) {
	for in, want := range map[string]string{
		`"a<br/>b"`:            "a|b",
		"`**bold** and *it*`":  "bold and it",
		"x #quot;q#quot; #35;": `x "q" #`,
		"a &amp; b":            "a & b",
		"<b>x</b>":             "x",
		"fa:fa-car Car":        "Car",
		"snake_case_name":      "snake_case_name",
	} {
		if got := strings.Join(labelLines(in), "|"); got != want {
			t.Errorf("labelLines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDashesAndFlatten(t *testing.T) {
	var p path
	p.move(0, 0)
	p.line(100, 0)
	p.quad(110, 0, 110, 10)
	p.cube(Point{110, 20}, Point{100, 30}, Point{90, 30})
	p.close()
	pl := Flatten(p)
	if len(pl) != 1 || pl[0][0] != (Point{0, 0}) || pl[0][len(pl[0])-1] != (Point{0, 0}) {
		t.Fatalf("flatten %v", pl)
	}
	ds := Dashes(linePath(Point{0, 0}, Point{100, 0}), 10, 10)
	moves := 0
	for _, s := range ds {
		if s.Op == Move {
			moves++
		}
	}
	if moves != 5 {
		t.Fatalf("%d dashes, want 5", moves)
	}
	if got := Dashes(p, 0, 0); len(got) != len(p) {
		t.Fatal("solid pattern changed the path")
	}
	if on, off := DashPattern(Solid, 1); on != 0 || off != 0 {
		t.Fatal("solid has a pattern")
	}
	if PathData(p) == "" {
		t.Fatal("empty path data")
	}
}

func TestEstimateAndMeasurer(t *testing.T) {
	if EstimateWidth("WWW", 10, false) <= EstimateWidth("iii", 10, false) || EstimateWidth("日本", 10, true) < 20 {
		t.Fatal("estimate")
	}
	calls := 0
	d, err := Render("flowchart TD\nA[Hello]", Options{Measure: func(s string, size float64, bold bool) float64 {
		calls++
		return float64(len(s)) * 20
	}, FontSize: 20})
	if err != nil || calls == 0 || d.Nodes[0].Box.W < 100 {
		t.Fatalf("measurer not used: %v calls=%d %+v", err, calls, d.Nodes)
	}
}

func TestSyntaxHelpers(t *testing.T) {
	if itoa(0) != "0" || itoa(1234) != "1234" {
		t.Fatal("itoa")
	}
	if got := splitStatements(srcLine{1, `A["x;y"] --> B; B --> C;`}); len(got) != 2 {
		t.Fatalf("split %v", got)
	}
	r := Rect{0, 0, 10, 10}
	if !r.Overlaps(Rect{5, 5, 10, 10}) || r.Overlaps(Rect{10, 0, 5, 5}) {
		t.Fatal("overlaps")
	}
}
