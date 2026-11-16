package controller

import (
	"context"
	"time"
)

// RecoveryPoint is an entry of history.recovery: unsaved changes of a file
// that could not be saved when the last session ended, kept as a Local
// History revision.
type RecoveryPoint struct {
	// Path is project relative, File absolute.
	Path     string    `json:"path"`
	File     string    `json:"file"`
	Revision uint64    `json:"revision"`
	Time     time.Time `json:"time"`
	Reason   string    `json:"reason"`
	Error    string    `json:"error"`
}

// RecoveryDiff is a recovery point loaded for review: the preserved text
// against the file's current content on disk.
type RecoveryDiff struct {
	Point RecoveryPoint
	// Current is the file content now ("" if the file is gone), Recovered
	// the preserved unsaved content.
	Current, Recovered string
}

// loadRecovery asks the backend for recovery points of the open project
// (history.recovery). Errors (older backend, no project) are ignored: the
// hint is optional.
func (c *Controller) loadRecovery() {
	c.Go(func(ctx context.Context) func() {
		var pts []RecoveryPoint
		err := c.call(ctx, "history.recovery", nil, &pts)
		return func() {
			if err != nil {
				pts = nil
			}
			c.setRecovery(pts)
		}
	})
}

func (c *Controller) setRecovery(pts []RecoveryPoint) {
	c.Recovery = pts
	c.RecoveryVersion++
}

// ShowRecovery opens the file of pt and loads the preserved content and
// the current content into RecoveryShown (the view shows them as a diff).
func (c *Controller) ShowRecovery(pt RecoveryPoint) {
	c.OpenFile(pt.File, nil)
	c.Go(func(ctx context.Context) func() {
		var rev struct {
			Text string `json:"text"`
		}
		err := c.call(ctx, "history.content", map[string]any{"path": pt.File, "id": pt.Revision}, &rev)
		var cur struct {
			Content string `json:"content"`
		}
		_ = c.call(ctx, "files.read", map[string]any{"path": pt.File, "maxBytes": 4 << 20}, &cur)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.RecoveryShown = &RecoveryDiff{Point: pt, Current: cur.Content, Recovered: rev.Text}
			c.RecoveryShownVersion++
		}
	})
}

// CloseRecoveryDiff forgets the shown recovery diff.
func (c *Controller) CloseRecoveryDiff() {
	if c.RecoveryShown != nil {
		c.RecoveryShown = nil
		c.RecoveryShownVersion++
	}
}

// RestoreRecovery writes the preserved content back to the file
// (history.restore; an open document is reloaded) and dismisses the point.
func (c *Controller) RestoreRecovery(pt RecoveryPoint) {
	c.CloseRecoveryDiff()
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "history.restore", map[string]any{"path": pt.File, "id": pt.Revision}, nil)
		var rest []RecoveryPoint
		var derr error
		if err == nil {
			derr = c.call(ctx, "history.recoveryDismiss", map[string]any{"paths": []string{pt.File}}, &rest)
		}
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Restored unsaved changes of " + pt.Path
			if derr == nil {
				c.setRecovery(rest)
			}
			c.OpenFile(pt.File, nil)
		}
	})
}

// DismissRecovery drops the recovery hints of files (none: all) through
// history.recoveryDismiss; the revisions stay in the Local History.
func (c *Controller) DismissRecovery(files ...string) {
	c.CloseRecoveryDiff()
	params := map[string]any{}
	if len(files) > 0 {
		params["paths"] = files
	}
	c.Go(func(ctx context.Context) func() {
		var rest []RecoveryPoint
		err := c.call(ctx, "history.recoveryDismiss", params, &rest)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.setRecovery(rest)
		}
	})
}
