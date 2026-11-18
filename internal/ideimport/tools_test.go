package ideimport

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/run"
)

func toolComponent(t *testing.T, xml string) Component {
	t.Helper()
	el, err := Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	return Component{Kind: KindExternalTool, Name: el.Attr("name"), File: "tools/External Tools.xml", Scope: ScopeGlobal,
		Element: el, Macros: Macros{UserHome: "/home/u"}}
}

// External tools map to run configurations of the type external-tool
// (review wave 8, finding 8).
func TestExternalToolItem(t *testing.T) {
	c := toolComponent(t, `<tool name="Lint file" description="Lints" useConsole="false" synchronizeAfterRun="false">
  <exec>
    <option name="COMMAND" value="$USER_HOME$/bin/lint" />
    <option name="PARAMETERS" value="--line $LineNumber$ $FilePath$ $Prompt$ $ModuleName$ $Prompt$" />
    <option name="WORKING_DIRECTORY" value="$FileDir$" />
  </exec>
</tool>`)
	it := ExternalToolItem(c)
	if it.Status != StatusReady || it.Area != AreaTools || it.Name != "External tool Lint file" || len(it.Changes) != 1 {
		t.Fatalf("item = %+v", it)
	}
	if !strings.Contains(it.Detail, "macros not supported (kept as text): $Prompt$, $ModuleName$") {
		t.Errorf("detail = %q", it.Detail)
	}
	cfg := it.Changes[0].RunConfig
	if cfg == nil || cfg.Type != run.TypeExternalTool || cfg.Name != "Lint file" {
		t.Fatalf("config = %+v", cfg)
	}
	want := map[string]any{
		run.ToolFieldProgram: "/home/u/bin/lint", run.ToolFieldArgs: "--line $LineNumber$ $FilePath$ $Prompt$ $ModuleName$ $Prompt$",
		run.ToolFieldDir: "$FileDir$", run.ToolFieldDescription: "Lints", run.ToolFieldConsole: false, run.ToolFieldSync: false,
	}
	for k, v := range want {
		if cfg.Values[k] != v {
			t.Errorf("%s = %v, want %v", k, cfg.Values[k], v)
		}
	}
	// Older files keep the flags as options.
	et := ParseExternalTool(toolComponent(t, `<tool name="x"><option name="SHOW_CONSOLE" value="false"/><option name="SYNC_AFTER" value="true"/><exec><option name="COMMAND" value="x"/></exec></tool>`).Element)
	if et.Console || !et.Sync {
		t.Errorf("option flags = %+v", et)
	}
	for xml, reason := range map[string]string{
		`<tool name="x" disabled="true"><exec><option name="COMMAND" value="x"/></exec></tool>`: "disabled",
		`<tool name="x"><exec/></tool>`:                                "no program",
		`<tool><exec><option name="COMMAND" value="x"/></exec></tool>`: "without a name",
	} {
		if it := ExternalToolItem(toolComponent(t, xml)); it.Status != StatusSkipped || !strings.Contains(it.Detail, reason) {
			t.Errorf("%s = %+v", xml, it)
		}
	}
	if got := UnknownToolMacros("$FilePath$ $SelectedText$ $FileClass$"); len(got) != 1 || got[0] != "$FileClass$" {
		t.Errorf("unknown = %v", got)
	}
}
