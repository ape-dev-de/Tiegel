package gui

import (
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/interp"
	rtui "github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
)

// setProbePanel fills the interpreter info panel from the controller's
// probe state (in place: the panel keeps its button state).
func setProbePanel(p *rtui.InterpreterInfoPanel, st controller.ProbeState) {
	p.ID, p.Error, p.Busy, p.Info = st.ID, st.Err, st.Busy, nil
	if r := st.Result; r != nil {
		i := r.Info
		p.Info = rtui.FromProbe(interp.Info{Version: i.Version, SAPI: i.SAPI, OS: i.OS, Xdebug: i.Xdebug, IniFile: i.IniFile,
			IniScanned: i.IniScanned, Extensions: i.Extensions, ZendExtensions: i.ZendExtensions}, r.ProbedAt, r.Cached)
	}
}
