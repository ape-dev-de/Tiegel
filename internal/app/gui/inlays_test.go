package gui

import (
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// Inlay hints are requested after the debounce while a server runs and
// removed when the setting is switched off.
func TestInlayHintsRequestAndSetting(t *testing.T) {
	old := InlayDelay
	InlayDelay = 20 * time.Millisecond
	t.Cleanup(func() { InlayDelay = old })
	e := newEnvWith(t, true, map[string]string{"src/H.php": "<?php\nf($a);\n"}, nil)
	s := e.openEditor("src/H.php")
	e.ctl.Status.LSPRunning = true
	e.waitFor("request", func() bool { return s.inlay.on && s.inlay.sent && !s.inlay.busy })
	ver := s.inlay.version

	// Edits move existing hints and ask again after the delay.
	s.ed.SetInlayHints([]editorview.InlayHint{{Offset: 8, Label: "x:"}})
	s.doc.Lock()
	_, _ = s.doc.Buffer().Insert(0, []byte(" "))
	s.doc.Unlock()
	e.waitFor("new request", func() bool { return s.inlay.version != ver && s.inlay.sent && !s.inlay.busy })
	if h := s.ed.InlayHints(); len(h) != 1 || h[0].Offset != 9 {
		t.Fatalf("hints %+v", h)
	}

	e.ctl.SetSettingValue("editor.inlayHints", "global", false)
	e.waitFor("hints off", func() bool { return !e.ctl.Prefs.InlayHints && len(s.ed.InlayHints()) == 0 && !s.inlay.on })
}
