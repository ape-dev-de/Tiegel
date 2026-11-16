package gui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Remote git UI (D154): Fetch, Pull… and Update Project… (dialog with
// strategy merge/rebase/ff-only and Autostash) in the Git menu, the Git
// window header and the status bar; Stash Changes… and the stash list
// (third tab of the Git window) with Apply, Pop and Drop. Failures are
// shown with their reason (auth, network, diverged, local changes, …);
// conflicts open the merge window.

// Remote action IDs.
const (
	ActFetch         keymap.Action = "git.fetch"
	ActPull          keymap.Action = "git.pull"
	ActUpdateProject keymap.Action = "git.updateProject"
	ActStash         keymap.Action = "git.stash"
	ActShowStashes   keymap.Action = "git.stashes"
	statusRemoteID                 = "git.remote"
)

// gitTabStashes is the index of the stash tab of the Git window.
const gitTabStashes = 2

// remoteUI is the view state of the remote operations.
type remoteUI struct {
	problemSeen, conflictSeen, stashSeen uint64

	// Pull dialog.
	strategy  [3]atoms.Button
	autostash atoms.Checkbox
	prune     atoms.Checkbox
	choice    controller.PullOptions

	// Stash dialog.
	message   atoms.TextField
	untracked atoms.Checkbox
	keepIndex atoms.Checkbox

	// Stash list.
	rows     []molecules.ListRow
	list     molecules.ScrollList
	sel      int
	apply    atoms.Button
	pop      atoms.Button
	drop     atoms.Button
	stashNew atoms.Button

	// Git window header.
	fetch, pull, update atoms.Button
}

func (d *workDock) installRemote() {
	r := &d.remote
	r.apply = atoms.Button{Text: "Apply", Kind: atoms.Secondary}
	r.pop = atoms.Button{Text: "Pop", Kind: atoms.Secondary}
	r.drop = atoms.Button{Text: "Drop…", Kind: atoms.Ghost, Glyph: atoms.GlyphDelete}
	r.stashNew = atoms.Button{Text: "Stash Changes…", Kind: atoms.Primary}
	r.fetch = atoms.Button{Text: "Fetch", Kind: atoms.Ghost, Glyph: atoms.GlyphCloud}
	r.pull = atoms.Button{Text: "Pull…", Kind: atoms.Ghost, Glyph: atoms.GlyphArrowLeft}
	r.update = atoms.Button{Text: "Update", Kind: atoms.Ghost, Glyph: atoms.GlyphRefresh}
	r.message = atoms.TextField{Hint: "Message (optional)"}
	r.message.Editor.SingleLine = true
	r.untracked = atoms.Checkbox{Label: "Include untracked files"}
	r.keepIndex = atoms.Checkbox{Label: "Keep staged changes in the index"}
	r.autostash = atoms.Checkbox{Label: "Stash local changes before and restore after (autostash)"}
	r.prune = atoms.Checkbox{Label: "Prune deleted remote branches"}
	r.sel = -1
	reg := d.v.Registry
	reg.AddStatusProvider(d.remoteStatus)
	reg.AddStatusClickHandler(func(v *View, id string) bool {
		if id != statusRemoteID {
			return false
		}
		v.ShowMenu("Git", d.remoteMenu())
		return true
	})
}

func (d *workDock) remoteActions() []Action {
	w := d.work
	return []Action{
		{ID: ActFetch, Title: "Fetch", Menu: "Git", Section: "remote", Run: func(*View) { w().Fetch() }},
		{ID: ActPull, Title: "Pull…", Menu: "Git", Section: "remote", Run: func(*View) { d.openPullDialog(false) }},
		{ID: ActUpdateProject, Title: "Update Project…", Mac: "Cmd+T", PC: "Ctrl+T", Menu: "Git", Section: "remote", Run: func(*View) { d.openPullDialog(true) }},
		{ID: ActStash, Title: "Stash Changes…", Menu: "Git", Section: "stash", Run: func(*View) { d.openStashDialog() }},
		{ID: ActShowStashes, Title: "Show Stashes", Menu: "Git", Section: "stash", Run: func(*View) { d.showStashes() }},
	}
}

// remoteMenu is the status bar menu.
func (d *workDock) remoteMenu() []MenuItem {
	chord := func(id keymap.Action) string { return d.v.Registry.ChordText(id, d.v.goos) }
	return []MenuItem{
		{Title: "Update Project…", Action: ActUpdateProject, Shortcut: chord(ActUpdateProject)},
		{Title: "Pull…", Action: ActPull},
		{Title: "Fetch", Action: ActFetch},
		{Title: "Push", Action: ActPush, Shortcut: chord(ActPush)},
		{Separator: true},
		{Title: "Stash Changes…", Action: ActStash},
		{Title: "Show Stashes", Action: ActShowStashes},
	}
}

// remoteStatus is the status bar item (busy state or the update entry).
func (d *workDock) remoteStatus() []statusbar.Item {
	if !d.gitOn {
		return nil
	}
	r := d.work().Remote
	text, tone := "", statusbar.ToneMuted
	switch {
	case r.Busy != "":
		text = strings.ToUpper(r.Busy[:1]) + r.Busy[1:] + "…"
	case r.Problem != nil:
		text, tone = "Git: "+r.Problem.Kind, statusbar.ToneWarning
	}
	return []statusbar.Item{{ID: statusRemoteID, Text: text, Glyph: atoms.GlyphCloud, Tone: tone,
		Tooltip: "Fetch, Pull, Update Project, Push, Stash", Clickable: true}}
}

// remoteFrame shows new problems and conflicts and follows the stash
// list.
func (d *workDock) remoteFrame() {
	w, r, v := d.work(), &d.remote, d.v
	if r.problemSeen != w.Remote.ProblemVersion {
		r.problemSeen = w.Remote.ProblemVersion
		if p := w.Remote.Problem; p != nil && v.popup == nil {
			v.OpenPopup(&Popup{Title: p.Title, Detail: p.Detail, Width: 560, Centered: true,
				Buttons: []*PopupButton{{Text: "OK", Kind: atoms.Primary, OnClick: w.DismissRemoteProblem}},
				OnClose: w.DismissRemoteProblem})
		}
	}
	if r.conflictSeen != w.Remote.ConflictVersion {
		r.conflictSeen = w.Remote.ConflictVersion
		if cs := w.Remote.Conflicts; len(cs) > 0 {
			v.ShowTool(ToolGit)
			v.ctl.Vcs.OpenMerge(cs[0])
		}
	}
	if r.stashSeen != w.Remote.StashVersion {
		r.stashSeen = w.Remote.StashVersion
		r.rows = make([]molecules.ListRow, len(w.Remote.Stashes))
		if r.sel >= len(r.rows) {
			r.sel = len(r.rows) - 1
		}
		if r.sel < 0 && len(r.rows) > 0 {
			r.sel = 0
		}
	}
}

// ---- dialogs ----------------------------------------------------------------------

// openPullDialog asks for the strategy and autostash, then pulls (or
// updates the project).
func (d *workDock) openPullDialog(update bool) {
	w, r := d.work(), &d.remote
	r.choice = w.Remote.Pull
	if r.choice.Strategy == "" {
		r.choice.Strategy = "merge"
	}
	r.autostash.State = checkState(r.choice.Autostash)
	r.prune.State = checkState(r.choice.Prune)
	labels := map[string]string{"merge": "Merge", "rebase": "Rebase", "ff-only": "Fast-forward only"}
	for i, s := range controller.PullStrategies {
		r.strategy[i].Text = labels[s]
	}
	title, ok := "Pull", "Pull"
	info := d.v.ctl.Vcs.Info
	detail := "Fetch the upstream and integrate it into " + info.Branch + "."
	if info.Upstream != "" {
		detail = fmt.Sprintf("Fetch %s and integrate it into %s (%d behind, %d ahead).", info.Upstream, info.Branch, info.Behind, info.Ahead)
	}
	if update {
		title, ok = "Update Project", "Update"
		detail = "Save all documents, " + strings.ToLower(detail[:1]) + detail[1:] + " Open documents are reloaded."
	}
	body := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		for i, s := range controller.PullStrategies {
			if r.strategy[i].Clicked(gtx) {
				r.choice.Strategy = s
			}
		}
		btns := []layout.FlexChild{layout.Rigid(atoms.Small("Strategy").Fn(th))}
		for i, s := range controller.PullStrategies {
			b := &r.strategy[i]
			b.Kind = atoms.Ghost
			if r.choice.Strategy == s {
				b.Kind = atoms.Secondary
			}
			btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, btns...)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.autostash.Layout(gtx, th) })
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.prune.Layout(gtx, th) }),
		)
	}
	d.v.OpenPopup(&Popup{Title: title, Detail: detail, Body: body, Width: 520, Centered: true, Buttons: []*PopupButton{
		{Text: ok, Kind: atoms.Primary, OnClick: func() {
			o := r.choice
			o.Autostash, o.Prune = r.autostash.Checked(), r.prune.Checked()
			if update {
				w.UpdateProject(o)
			} else {
				w.Pull(o)
			}
		}},
		{Text: "Cancel", Kind: atoms.Secondary},
	}})
}

func checkState(on bool) atoms.CheckState {
	if on {
		return atoms.Checked
	}
	return atoms.Unchecked
}

// openStashDialog asks for the message and options of a new stash entry.
func (d *workDock) openStashDialog() {
	w, r := d.work(), &d.remote
	r.message.SetText("")
	body := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.message.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.untracked.Layout(gtx, th) })
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.keepIndex.Layout(gtx, th) }),
		)
	}
	d.v.OpenPopup(&Popup{Title: "Stash Changes", Detail: "Documents are saved first; the work tree returns to HEAD.", Body: body, Width: 480, Centered: true,
		Buttons: []*PopupButton{
			{Text: "Stash", Kind: atoms.Primary, OnClick: func() {
				w.Stash(controller.StashOptions{Message: r.message.Text(), IncludeUntracked: r.untracked.Checked(), KeepIndex: r.keepIndex.Checked()})
			}},
			{Text: "Cancel", Kind: atoms.Secondary},
		}})
}

// showStashes opens the stash tab of the Git window.
func (d *workDock) showStashes() {
	v := d.v
	if v.gitd != nil {
		v.gitd.tab = gitTabStashes
	}
	v.ShowTool(ToolGit)
	d.work().LoadStashes()
}

// ---- Git window -----------------------------------------------------------------------

// layoutGitHeaderButtons are Fetch, Pull and Update in the Git window
// header.
func (d *workDock) layoutGitHeaderButtons(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r, w := &d.remote, d.work()
	if r.fetch.Clicked(gtx) {
		d.v.Post(w.Fetch)
	}
	if r.pull.Clicked(gtx) {
		d.v.Post(func() { d.openPullDialog(false) })
	}
	if r.update.Clicked(gtx) {
		d.v.Post(func() { d.openPullDialog(true) })
	}
	if w.Remote.Busy != "" {
		gtx = gtx.Disabled()
	}
	btn := func(b *atoms.Button) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
		})
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, btn(&r.fetch), btn(&r.pull), btn(&r.update))
}

// layoutStashes is the stash tab of the Git window.
func (d *workDock) layoutStashes(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r, w, v := &d.remote, d.work(), d.v
	list := w.Remote.Stashes
	for i := range r.rows {
		if cl, _ := r.rows[i].Update(gtx); cl {
			r.sel = i
		}
	}
	var cur *controller.StashEntry
	if r.sel >= 0 && r.sel < len(list) {
		e := list[r.sel]
		cur = &e
	}
	if r.stashNew.Clicked(gtx) {
		v.Post(d.openStashDialog)
	}
	if cur != nil {
		e := *cur
		if r.apply.Clicked(gtx) {
			v.Post(func() { w.StashApply(e, false) })
		}
		if r.pop.Clicked(gtx) {
			v.Post(func() {
				v.Confirm("Pop "+e.Ref+"?", "The changes are applied and the entry is dropped (kept if there are conflicts).", "Pop", func() { w.StashApply(e, true) })
			})
		}
		if r.drop.Clicked(gtx) {
			v.Post(func() {
				v.Confirm("Drop "+e.Ref+"?", e.Message+"\nThe stashed changes are lost.", "Drop", func() { w.StashDrop(e) })
			})
		}
	}
	toolbar := func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			off := cur == nil || w.Remote.Busy != ""
			gap := func(b *atoms.Button) layout.FlexChild {
				return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if off {
						gtx = gtx.Disabled()
					}
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
				})
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.stashNew.Layout(gtx, th) }),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
				}),
				gap(&r.apply), gap(&r.pop), gap(&r.drop),
			)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(toolbar),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(list) == 0 {
				return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted("No stashed changes").Fn(th))
			}
			return r.list.Layout(gtx, th, len(list), func(gtx layout.Context, i int) layout.Dimensions {
				e := list[i]
				row := &r.rows[i]
				row.Selected = i == r.sel
				return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(atoms.Label{Text: e.Ref, Role: atoms.RoleCode}.Fn(th)),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Label{Text: e.Message, MaxLines: 1}.Fn(th))
						}),
						layout.Rigid(atoms.Small(e.Date.Local().Format("2006-01-02 15:04")).Fn(th)),
					)
				})
			})
		}),
	)
}
