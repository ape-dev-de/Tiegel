package debugxdebug

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

func TestImportDebugPort(t *testing.T) {
	comp := func(xml string) ideimport.Component {
		el, err := ideimport.Parse(strings.NewReader(xml))
		if err != nil {
			t.Fatal(err)
		}
		return ideimport.Component{Kind: ideimport.KindComponent, Name: refDebugComponent, Scope: ideimport.ScopeGlobal, Element: el}
	}
	items, ok := importComponent(comp(`<component xdebug_debug_port="9005,9003" break_at_first_line="true"/>`))
	if !ok || items[0].Changes[0].Key != SettingPort || items[0].Changes[0].Value != 9005 ||
		!strings.Contains(items[0].Detail, "break at first line") || !strings.Contains(items[0].Detail, "first of") {
		t.Errorf("ports = %+v", items)
	}
	items, _ = importComponent(comp(`<component><option name="xdebug_debug_port" value="9000"/></component>`))
	if items[0].Changes[0].Value != 9000 {
		t.Errorf("option = %+v", items)
	}
	for _, xml := range []string{`<component/>`, `<component xdebug_debug_port="none"/>`, `<component xdebug_debug_port="70000"/>`} {
		items, _ = importComponent(comp(xml))
		if items[0].Status != ideimport.StatusSkipped {
			t.Errorf("%s = %+v", xml, items)
		}
	}
	c := comp(`<component/>`)
	c.Name = "Other"
	if _, ok := importComponent(c); ok {
		t.Error("other component taken")
	}
}
