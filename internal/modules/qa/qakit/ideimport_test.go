package qakit

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

func importPlugin() *Plugin {
	return New(Definition{ID: "qa-x", Name: "X", Program: "x", Format: &FormatSpec{},
		Import: &ImportSpec{Settings: "XTool", Options: "XOptions", ConfigOptions: []string{"config"}, StandardOption: "codingStandard", Formatter: "X_FMT"}})
}

func comp(t *testing.T, name, xml string) ideimport.Component {
	t.Helper()
	el, err := ideimport.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	return ideimport.Component{Kind: ideimport.KindComponent, Name: name, File: ".idea/php.xml", Scope: ideimport.ScopeProject, Element: el,
		Macros: ideimport.Macros{ProjectDir: filepath.FromSlash("/p")}}
}

func changes(it ideimport.Item) map[string]any {
	out := map[string]any{}
	for _, c := range it.Changes {
		out[c.Key] = c.Value
	}
	return out
}

func TestImportQAComponents(t *testing.T) {
	p := importPlugin()
	for _, tc := range []struct {
		name, xml string
		want      map[string]any
	}{
		{"XTool", `<component><s><x_by_interpreter timeout="1500"/></s></component>`,
			map[string]any{"qa-x.useInterpreter": true, "qa-x.timeout": 2}},
		{"XTool", `<component><s><XConfiguration tool_path="$PROJECT_DIR$/bin/x" timeout="99999999"/></s></component>`,
			map[string]any{"qa-x.path": "bin/x", "qa-x.useInterpreter": false, "qa-x.timeout": MaxTimeout}},
		{"XOptions", `<component><option name="config" value="$PROJECT_DIR$/x.xml"/></component>`,
			map[string]any{"qa-x.config": "x.xml"}},
		{"XOptions", `<component><option name="codingStandard" value="PSR12"/></component>`,
			map[string]any{"qa-x.config": "PSR12"}},
		{"PhpExternalFormatter", `<component><option name="externalFormatter" value="X_FMT"/></component>`,
			map[string]any{"qa-x.reformat": true}},
	} {
		items, ok := p.importComponent(comp(t, tc.name, tc.xml))
		if !ok || len(items) != 1 || !reflect.DeepEqual(changes(items[0]), tc.want) {
			t.Errorf("%s %s: %+v %v", tc.name, tc.xml, items, ok)
		}
	}
	for _, xml := range []string{`<component/>`} {
		items, _ := p.importComponent(comp(t, "XTool", xml))
		if items[0].Status != ideimport.StatusSkipped {
			t.Errorf("empty settings = %+v", items[0])
		}
		items, _ = p.importComponent(comp(t, "XOptions", `<component><option name="codingStandard" value="Custom"/></component>`))
		if items[0].Status != ideimport.StatusSkipped {
			t.Errorf("custom without file = %+v", items[0])
		}
	}
	if _, ok := p.importComponent(comp(t, "PhpExternalFormatter", `<component><option name="externalFormatter" value="OTHER"/></component>`)); ok {
		t.Error("other formatter taken")
	}
	if _, ok := p.importComponent(comp(t, "Unknown", `<component/>`)); ok {
		t.Error("unknown component taken")
	}
	if _, ok := New(Definition{ID: "qa-y"}).importComponent(comp(t, "XTool", `<component/>`)); ok {
		t.Error("plugin without import spec took a component")
	}
}
