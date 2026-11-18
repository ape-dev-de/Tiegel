package phpunit

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

func component(t *testing.T, kind, name, xml string) ideimport.Component {
	t.Helper()
	el, err := ideimport.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.FromSlash("/p/shop")
	return ideimport.Component{Kind: kind, Name: name, File: ".idea/x.xml", Scope: ideimport.ScopeProject, Element: el,
		Macros: ideimport.Macros{ProjectDir: root}}
}

func runConfig(t *testing.T, xml string) ideimport.Item {
	t.Helper()
	items, ok := importComponent(component(t, ideimport.KindRunConfiguration, refRunType, xml))
	if !ok || len(items) != 1 {
		t.Fatalf("not handled: %v %v", items, ok)
	}
	return items[0]
}

func TestImportRunConfigurations(t *testing.T) {
	for _, tc := range []struct {
		xml  string
		want map[string]any
	}{
		{`<configuration name="All"><TestRunner scope="XML" options="--testsuite Unit --stop-on-failure"/></configuration>`,
			map[string]any{fieldScope: scopeSuite, fieldSuite: "Unit", fieldArgs: "--stop-on-failure"}},
		{`<configuration name="Whole"><TestRunner scope="XML" configuration_file="$PROJECT_DIR$/phpunit.ci.xml" use_alternative_configuration_file="true"/></configuration>`,
			map[string]any{fieldScope: scopeAll, fieldArgs: "--configuration phpunit.ci.xml"}},
		{`<configuration name="Cls"><TestRunner scope="Class" class="A\BTest"/></configuration>`,
			map[string]any{fieldScope: scopeClass, fieldClass: `A\BTest`}},
		{`<configuration name="Grp"><TestRunner scope="Group" groups="slow"/></configuration>`,
			map[string]any{fieldScope: scopeAll, fieldArgs: "--group slow"}},
	} {
		it := runConfig(t, tc.xml)
		rc := it.Changes[0].RunConfig
		if rc.Type != ID || !reflect.DeepEqual(rc.Values, tc.want) || it.Area != ideimport.AreaRun {
			t.Errorf("%s: %+v", tc.xml, rc.Values)
		}
		if _, err := New(Options{}).configType().Check(*rc); err != nil {
			t.Errorf("%s does not validate: %v", rc.Name, err)
		}
	}
	if it := runConfig(t, `<configuration name="Dir"><TestRunner scope="Directory"/></configuration>`); it.Status != ideimport.StatusSkipped {
		t.Errorf("no directory = %+v", it)
	}
	if _, ok := importComponent(component(t, ideimport.KindRunConfiguration, "Other", `<configuration/>`)); ok {
		t.Error("other run type taken")
	}
}

func TestImportSettings(t *testing.T) {
	items, ok := importComponent(component(t, ideimport.KindComponent, refComponent,
		`<component name="PhpUnit"><phpunit_settings><PhpUnitSettings load_method="PHPUNIT_PHAR" phpunit_phar_path="$PROJECT_DIR$/tools/phpunit.phar" configuration_file_path="$PROJECT_DIR$/ci/phpunit.xml"/></phpunit_settings></component>`))
	if !ok {
		t.Fatal("not handled")
	}
	got := map[string]any{}
	for _, c := range items[0].Changes {
		got[c.Key] = c.Value
	}
	if !reflect.DeepEqual(got, map[string]any{SettingConfig: "ci/phpunit.xml", SettingExecutable: "tools/phpunit.phar"}) {
		t.Errorf("settings = %v", got)
	}
	items, _ = importComponent(component(t, ideimport.KindComponent, refComponent,
		`<component><phpunit_settings><PhpUnitSettings use_configuration_file="false" configuration_file_path="x.xml"/></phpunit_settings></component>`))
	if items[0].Status != ideimport.StatusSkipped {
		t.Errorf("unused configuration = %+v", items[0])
	}
	items, _ = importComponent(component(t, ideimport.KindComponent, refVersionCache, `<component/>`))
	if items[0].Status != ideimport.StatusSkipped {
		t.Errorf("cache = %+v", items[0])
	}
	if _, ok := importComponent(component(t, ideimport.KindComponent, "Other", `<component/>`)); ok {
		t.Error("other component taken")
	}
	if _, ok := importComponent(component(t, ideimport.KindExternalTool, refComponent, `<tool/>`)); ok {
		t.Error("external tool taken")
	}
}
