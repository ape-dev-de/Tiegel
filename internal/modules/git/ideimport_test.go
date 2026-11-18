package git

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

func TestImportGitComponents(t *testing.T) {
	comp := func(name, xml string) ideimport.Component {
		el, err := ideimport.Parse(strings.NewReader(xml))
		if err != nil {
			t.Fatal(err)
		}
		return ideimport.Component{Kind: ideimport.KindComponent, Name: name, Scope: ideimport.ScopeProject, Element: el,
			Macros: ideimport.Macros{ProjectDir: "/p"}}
	}
	items, ok := importComponent(comp(refGitSettings, `<component><option name="UPDATE_TYPE" value="REBASE"/></component>`))
	if !ok || items[0].Changes[0].Key != SettingUpdateMethod || items[0].Changes[0].Value != "rebase" {
		t.Errorf("rebase = %+v", items)
	}
	items, _ = importComponent(comp(refGitSettings, `<component><option name="UPDATE_TYPE" value="BRANCH_DEFAULT"/></component>`))
	if items[0].Status != ideimport.StatusUnsupported {
		t.Errorf("branch default = %+v", items)
	}
	if items, ok := importComponent(comp(refGitSettings, `<component/>`)); !ok || len(items) != 0 {
		t.Errorf("no update type = %+v %v", items, ok)
	}
	items, _ = importComponent(comp(refVcsMappings, `<component><mapping directory="$PROJECT_DIR$" vcs="Git"/><mapping directory="" vcs="Git"/></component>`))
	if items[0].Status != ideimport.StatusSkipped || !strings.Contains(items[0].Detail, ".") {
		t.Errorf("git roots = %+v", items)
	}
	items, _ = importComponent(comp(refVcsMappings, `<component><mapping directory="$PROJECT_DIR$/svn" vcs="svn"/></component>`))
	if items[0].Status != ideimport.StatusUnsupported {
		t.Errorf("svn = %+v", items)
	}
	if _, ok := importComponent(comp("Other", `<component/>`)); ok {
		t.Error("other component taken")
	}
	c := comp(refGitSettings, `<x/>`)
	c.Kind = ideimport.KindRunConfiguration
	if _, ok := importComponent(c); ok {
		t.Error("run configuration taken")
	}
}
