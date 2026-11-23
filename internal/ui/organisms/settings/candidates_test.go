package settings

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func candidatePages() []Page {
	return []Page{
		{ID: "core:Project", Title: "Project", Section: "Project", Extra: ExtraProject},
		{ID: "plugin:phpunit", Title: "PHPUnit", Section: "Tests", Module: &Module{ID: "phpunit", Enabled: true}, Fields: []Field{
			{Key: "phpunit.executable", Kind: KindPath, Label: "PHPUnit script", Value: "", Source: "default", ProjectScope: true,
				CandidateSource: "PHPUnit scripts", Candidates: []Candidate{
					{Value: "/p/project/vendor/bin/phpunit", Label: "project/vendor/bin/phpunit", Detail: "in the PHP root", Recommended: true},
					{Value: "/p/tools/bin/phpunit", Label: "tools/bin/phpunit"},
				}},
			{Key: "docker.composeFiles", Kind: KindList, Label: "Compose files", Value: []any{}, Source: "default", ProjectScope: true,
				CandidateSource: "compose files", Candidates: []Candidate{{Value: []any{"../../docker-compose.yaml"}, Label: "docker-compose.yaml in ../..", Recommended: true}}},
			{Key: "phpunit.configuration", Kind: KindPath, Label: "Configuration", Value: "x.xml", Source: "project", ProjectScope: true,
				CandidateSource: "phpunit.xml files", CandidatesLoading: true},
		}},
		{ID: "plugins", Title: "All Plugins", Section: "Plugins", Extra: ExtraPlugins},
	}
}

func TestCandidateField(t *testing.T) {
	p := &Panel{Pages: candidatePages(), Selected: "plugin:phpunit"}
	p.Discovered.Items = []runtime.Discovered{{ID: "compose:x:app", Label: "PHP (compose x/app)", Kind: runtime.KindCompose, Where: "../../docker-compose.yaml", Mapping: "project → /var/www", Score: 80, Reasons: []string{"mounts the PHP root"}}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	st := p.fields["phpunit.executable"]
	if st == nil || st.cand == nil || st.free(p.Pages[1].Fields[0]) {
		t.Fatal("no candidate dropdown")
	}
	// Recommended first, then the current value ("Default") and Custom….
	opts := st.cand.Options
	if len(opts) != 4 || opts[0].Badge != "recommended" || opts[2].Label != "Default" || opts[3].Label != customLabel {
		t.Fatalf("options %+v", opts)
	}
	if st.candidateIndex(p.Pages[1].Fields[0]) != 2 {
		t.Fatal("current value not selected")
	}
	// Open the dropdown and click the first option.
	st.cand.Open = true
	h.Frame(w)
	evs = nil
	for y := 60; y < 600 && len(evs) == 0; y += 4 {
		st.cand.Open = true
		h.Frame(w)
		h.Click(image.Pt(220+40+150, y))
		h.Frame(w)
		var keep []Event
		for _, e := range evs {
			if se, ok := e.(SetEvent); ok && se.Key == "phpunit.executable" {
				keep = append(keep, e)
			}
		}
		evs = keep
		if st.custom {
			st.custom = false
			evs = nil
		}
	}
	if len(evs) == 0 {
		t.Fatal("choosing a candidate sent no SetEvent")
	}
	if v := evs[0].(SetEvent).Value; v != "/p/project/vendor/bin/phpunit" && v != "/p/tools/bin/phpunit" {
		t.Fatalf("value %v", v)
	}

	// Lists convert to []string; "Custom…" switches to free input.
	if got := candidateValue(KindList, []any{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("list %v", got)
	}
	if candidateValue(KindPath, "x") != "x" {
		t.Fatal("scalar")
	}
	st.custom = true
	h.Frame(w)
	if !st.free(p.Pages[1].Fields[0]) {
		t.Fatal("custom mode")
	}
	// A loading field without candidates takes free input.
	if cs := p.fields["phpunit.configuration"]; cs == nil || !cs.free(p.Pages[1].Fields[2]) {
		t.Fatal("loading field")
	}

	// Sections, overview, project page and discovered list render.
	for _, id := range []string{"core:Project", "plugins"} {
		p.Selected = id
		uitest.Smoke(t, id, image.Pt(1000, 700), w)
	}
	if got := p.sectionOrder(); !reflect.DeepEqual(got, []string{"Project", "Tests", "Plugins"}) {
		t.Fatalf("sections %v", got)
	}
	if SectionOf(Page{}) != "General" || SectionOf(Page{Module: &Module{}}) != "Plugins" {
		t.Fatal("default sections")
	}
	p.Selected = "core:Interpreter"
	p.Pages = append(p.Pages, Page{ID: "core:Interpreter", Title: "PHP Interpreter", Section: "Project", Extra: ExtraInterpreter})
	uitest.Smoke(t, "interpreter with discovered", image.Pt(1000, 700), w)
	// Search finds a page by title and section.
	if r := Results(p.Pages, "interpreter"); len(r) != 1 || r[0].ID != "core:Interpreter" {
		t.Fatalf("search %+v", r)
	}
	if r := Results(p.Pages, "tests phpunit"); len(r) != 1 {
		t.Fatalf("search section %+v", r)
	}
	p.NoProject = true
	p.Selected = "core:Project"
	uitest.Smoke(t, "no project", image.Pt(1000, 700), w)
}
