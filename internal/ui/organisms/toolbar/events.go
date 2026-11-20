package toolbar

// Event is an intent reported by Bar.Update. The concrete types below are
// the complete set; switch on them. The toolbar holds no project, git or
// run logic – the host turns events into API calls.
type Event interface{ toolbarEvent() }

// Popup names one of the toolbar's inline popups.
type Popup uint8

// Popups.
const (
	PopupNone Popup = iota
	PopupProject
	PopupBranch
	PopupRun
)

// String returns the popup name.
func (p Popup) String() string {
	return [...]string{"none", "project", "branch", "run"}[p]
}

// PopupOpened: a popup was opened. Hosts refresh its data lazily on it,
// e.g. the branch list when the branch popup opens.
type PopupOpened struct{ Popup Popup }

// SwitchProject: an open project was chosen in the project popup.
type SwitchProject struct{ ID string }

// OpenProject: "Open…" in the project popup (the host shows a folder
// chooser).
type OpenProject struct{}

// OpenRecentProject: a recent project was chosen.
type OpenRecentProject struct{ Path string }

// HibernateProject: "Hibernate <project>" was chosen.
type HibernateProject struct{ ID string }

// ResumeProject: "Resume <project>" was chosen for a hibernated project.
type ResumeProject struct{ ID string }

// CheckoutBranch: "Checkout" in a branch's submenu. Remote branches are
// checked out as a new tracking branch by the host.
type CheckoutBranch struct {
	Name   string
	Remote bool
}

// NewBranch: "New Branch…" (From empty: from HEAD) or "New Branch from
// '<branch>'…". The host asks for the name.
type NewBranch struct{ From string }

// CheckoutRevision: "Checkout Tag or Revision…". The host asks for the
// tag or revision.
type CheckoutRevision struct{}

// MergeBranch: merge the branch into the current one.
type MergeBranch struct {
	Name   string
	Remote bool
}

// RebaseOnto: rebase the current branch onto the branch.
type RebaseOnto struct {
	Name   string
	Remote bool
}

// DeleteBranch: delete the branch (the host confirms).
type DeleteBranch struct {
	Name   string
	Remote bool
}

// SelectRunConfig: a run configuration was chosen in the run popup.
type SelectRunConfig struct{ ID string }

// EditRunConfigs: "Edit Configurations…" in the run popup.
type EditRunConfigs struct{}

// Run: the Run button (Config is the selected configuration ID).
type Run struct{ Config string }

// Debug: the Debug button.
type Debug struct{ Config string }

// Stop: the Stop button (only shown while something runs).
type Stop struct{}

// ToggleListen: the Xdebug listen toggle; On is the requested state.
type ToggleListen struct{ On bool }

// QuickActionClicked: a plugin quick-action button was clicked (ID of the
// QuickAction, usually an action ID).
type QuickActionClicked struct{ ID string }

// SearchEverywhere: the search button.
type SearchEverywhere struct{}

// OpenSettings: the settings button.
type OpenSettings struct{}

func (PopupOpened) toolbarEvent()        {}
func (SwitchProject) toolbarEvent()      {}
func (OpenProject) toolbarEvent()        {}
func (OpenRecentProject) toolbarEvent()  {}
func (HibernateProject) toolbarEvent()   {}
func (ResumeProject) toolbarEvent()      {}
func (CheckoutBranch) toolbarEvent()     {}
func (NewBranch) toolbarEvent()          {}
func (CheckoutRevision) toolbarEvent()   {}
func (MergeBranch) toolbarEvent()        {}
func (RebaseOnto) toolbarEvent()         {}
func (DeleteBranch) toolbarEvent()       {}
func (SelectRunConfig) toolbarEvent()    {}
func (EditRunConfigs) toolbarEvent()     {}
func (Run) toolbarEvent()                {}
func (Debug) toolbarEvent()              {}
func (Stop) toolbarEvent()               {}
func (ToggleListen) toolbarEvent()       {}
func (QuickActionClicked) toolbarEvent() {}
func (SearchEverywhere) toolbarEvent()   {}
func (OpenSettings) toolbarEvent()       {}
