package gui

import (
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	rtui "github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
)

func TestSetProbePanel(t *testing.T) {
	var p rtui.InterpreterInfoPanel
	setProbePanel(&p, controller.ProbeState{ID: "x", Busy: true, Err: "e"})
	if p.ID != "x" || !p.Busy || p.Error != "e" || p.Info != nil {
		t.Fatalf("%+v", p)
	}
	setProbePanel(&p, controller.ProbeState{ID: "x", Result: &controller.ProbeResult{Info: controller.ProbeInfo{Version: "8.3.1", Xdebug: "3.3"}, ProbedAt: time.Now(), Cached: true}})
	if p.Info == nil || p.Info.Version != "8.3.1" || !p.Info.Cached || p.Info.ProbedAt == "" || p.Busy {
		t.Fatalf("%+v", p.Info)
	}
}
