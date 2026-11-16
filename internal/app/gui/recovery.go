package gui

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	gitui "github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// recoveryBar is the unobtrusive banner above the editor that points to
// unsaved changes of the last session (history.recovery) and the popup
// that shows one of them as a diff against the file on disk.
type recoveryBar struct {
	show, dismiss atoms.Button
	diff          *gitui.DiffView
	seen          uint64
}

func newRecoveryBar() *recoveryBar {
	r := &recoveryBar{
		show:    atoms.Button{Kind: atoms.Secondary, Text: "Show"},
		dismiss: atoms.Button{Kind: atoms.Ghost, Text: "Dismiss"},
		diff:    gitui.NewDiffView(),
	}
	r.diff.HideToolbar = true
	return r
}

// recoveryTitle is the banner title.
const recoveryTitle = "Unsaved changes from the last session can be recovered"

// recoveryDetail lists the files of the recovery points (at most three).
func recoveryDetail(pts []controller.RecoveryPoint) string {
	var names []string
	for i, p := range pts {
		if i == 3 {
			names = append(names, fmt.Sprintf("and %d more", len(pts)-3))
			break
		}
		names = append(names, p.Path)
	}
	return strings.Join(names, ", ")
}

// handleRecovery processes the banner buttons and opens the diff popup
// when a recovery point was loaded.
func (v *View) handleRecovery(gtx layout.Context) {
	r, c := v.recovery, v.ctl
	if len(c.Recovery) > 0 {
		if r.show.Clicked(gtx) {
			c.ShowRecovery(c.Recovery[0])
		}
		if r.dismiss.Clicked(gtx) {
			c.DismissRecovery()
		}
	}
	if r.seen == c.RecoveryShownVersion {
		return
	}
	r.seen = c.RecoveryShownVersion
	rd := c.RecoveryShown
	if rd == nil {
		return
	}
	pt := rd.Point
	r.diff.SetDiff(gitui.WholeFileDiff(pt.Path, pt.Path, []byte(rd.Current), []byte(rd.Recovered)))
	detail := fmt.Sprintf("%s – kept %s (%s). Left: file on disk, right: unsaved changes.", pt.Path, pt.Time.Local().Format("2006-01-02 15:04"), pt.Reason)
	if rd.Current == rd.Recovered {
		detail = pt.Path + " – the file on disk already has this content."
	}
	v.OpenPopup(&Popup{
		Title: "Recover unsaved changes", Detail: detail, Width: 760, BodyHeight: 360,
		Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return r.diff.Layout(gtx, th) },
		Buttons: []*PopupButton{
			{Text: "Restore", Kind: atoms.Primary, OnClick: func() { c.RestoreRecovery(pt) }},
			{Text: "Discard", Kind: atoms.Secondary, OnClick: func() { c.DismissRecovery(pt.File) }},
			{Text: "Close", Kind: atoms.Ghost, OnClick: c.CloseRecoveryDiff},
		},
		OnClose: c.CloseRecoveryDiff,
	})
}

// layoutEditorArea is the editor slot: the recovery banner (if any) above
// the editor.
func (v *View) layoutEditorArea(gtx layout.Context) layout.Dimensions {
	pts := v.ctl.Recovery
	if len(pts) == 0 {
		return v.layoutGroups(gtx)
	}
	th, r := v.th, v.recovery
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, molecules.Banner{Kind: molecules.BannerInfo, Title: recoveryTitle, Detail: recoveryDetail(pts)}.Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.show.Layout(gtx, th) })
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.dismiss.Layout(gtx, th) })
					}),
				)
			})
		}),
		layout.Flexed(1, v.layoutGroups),
	)
}
