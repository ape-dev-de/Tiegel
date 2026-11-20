package controller

import "github.com/ape-dev-de/tiegel/internal/api"

// NewLocalInProcess returns a Backend bound to exactly a, also when a
// hosts the window's workspace (api.App.EnableWorkspace): calls are not
// forwarded to the active project and only a's own events arrive. With
// several projects per window (AP18.5) the GUI keeps one controller per
// project App (api.Workspace.App) built on this backend.
func NewLocalInProcess(a *api.App) *InProcess {
	return &InProcess{InProcess: api.NewLocalInProcess(a)}
}
