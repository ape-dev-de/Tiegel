package actions

import (
	"testing"
)

func TestToolbar(t *testing.T) {
	r := NewRegistry()
	running := true
	err := r.Add(
		Action{ID: "claude.open", Label: "Claude Code", Icon: "run", Shortcut: Shortcut{Mac: "Cmd+Alt+K", PC: "Ctrl+Alt+K"},
			Check: CheckBox, Checked: func(Context) bool { return running },
			Badge: func(c Context) string { return c.Value("claude") }},
		Action{ID: "opencode.open", Label: "opencode", Enabled: func(Context) bool { return false }},
		Action{ID: "hidden", Label: "Hidden", Visible: func(Context) bool { return false }},
		Action{ID: "x.listen", Label: "Listen"},
	)
	if err != nil {
		t.Fatal(err)
	}
	r.AddToolbar("claude.open", "opencode.open", "hidden", "missing")
	r.AddToolbar("x.listen")
	r.AppendMenu(CtxToolbar, Sub("ignored", Item("x.listen")))
	v := r.Version()
	got := r.Toolbar(Context{Values: map[string]string{"claude": "2"}}, nil, "darwin")
	if len(got) != 4 {
		t.Fatalf("buttons %+v", got)
	}
	c := got[0]
	if c.ID != "claude.open" || !c.Active || c.Badge != "2" || c.Tooltip != "Claude Code (Cmd+Alt+K)" || c.Icon != "run" {
		t.Fatalf("claude %+v", c)
	}
	if !got[1].Disabled || got[1].Tooltip != "opencode" || !got[2].Separator || got[3].ID != "x.listen" {
		t.Fatalf("buttons %+v", got)
	}
	r.RemoveToolbar("claude.open", "opencode.open", "hidden", "missing")
	if r.Version() == v {
		t.Fatal("version unchanged")
	}
	got = r.Toolbar(Context{}, nil, "linux")
	if len(got) != 1 || got[0].ID != "x.listen" {
		t.Fatalf("after remove %+v", got)
	}
	r.RemoveToolbar("x.listen")
	if got := r.Menu(CtxToolbar); len(got) != 1 || len(got[0].Children) == 0 {
		t.Fatalf("menu %+v", got)
	}
	r.RemoveToolbar()
	running = false
}
