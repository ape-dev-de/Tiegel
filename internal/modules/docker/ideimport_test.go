package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

func TestImportDockerComponents(t *testing.T) {
	comp := func(name, xml string) ideimport.Component {
		el, err := ideimport.Parse(strings.NewReader(xml))
		if err != nil {
			t.Fatal(err)
		}
		return ideimport.Component{Kind: ideimport.KindComponent, Name: name, Scope: ideimport.ScopeGlobal, Element: el,
			Macros: ideimport.Macros{UserHome: "/home/u"}}
	}
	for xml, want := range map[string]string{
		`<component><S uri="unix://$USER_HOME$/.orbstack/run/docker.sock"/></component>`:                 "orbstack",
		`<component><option name="uri" value="unix:///home/u/.colima/default/docker.sock"/></component>`: "colima",
	} {
		items, ok := importComponent(comp(refDockerComponent, xml))
		if !ok || len(items[0].Changes) != 1 || items[0].Changes[0].Value != want || items[0].Changes[0].Key != SettingContext {
			t.Errorf("%s = %+v", xml, items)
		}
	}
	items, _ := importComponent(comp(refDockerComponent, `<component><S uri="unix:///var/run/docker.sock"/></component>`))
	if items[0].Status != ideimport.StatusSkipped {
		t.Errorf("default socket = %+v", items)
	}
	items, _ = importComponent(comp(refDockerComponent, `<component><S uri="tcp://10.0.0.1:2375"/></component>`))
	if items[0].Status != ideimport.StatusUnsupported {
		t.Errorf("tcp = %+v", items)
	}
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	items, _ = importComponent(comp(refDockerTools, `<component><option name="DOCKER_PATH" value="`+filepath.ToSlash(bin)+`"/></component>`))
	if len(items[0].Changes) != 1 || items[0].Changes[0].Key != SettingPath {
		t.Errorf("binary = %+v", items)
	}
	items, _ = importComponent(comp(refDockerTools, `<component><option name="dockerPath" value="/nonexistent/docker"/></component>`))
	if items[0].Status != ideimport.StatusSkipped {
		t.Errorf("missing binary = %+v", items)
	}
	if _, ok := importComponent(comp("Other", `<component/>`)); ok {
		t.Error("other component taken")
	}
	c := comp(refDockerComponent, `<tool/>`)
	c.Kind = ideimport.KindExternalTool
	if _, ok := importComponent(c); ok {
		t.Error("external tool taken")
	}
}
