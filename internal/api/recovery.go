package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RecoveryPoint is a Local History revision holding unsaved changes that
// could not be saved (e.g. the save on close failed, or the App/project was
// closed with modified documents). "history.recovery" lists them, so a
// client can point the user to them at the next start.
type RecoveryPoint struct {
	Path     string    `json:"path"` // project relative (display path)
	File     string    `json:"file"` // absolute
	Revision uint64    `json:"revision"`
	Time     time.Time `json:"time"`
	Reason   string    `json:"reason"`
	Error    string    `json:"error,omitempty"`
}

// RecoveryDismissParams are the params of "history.recoveryDismiss".
type RecoveryDismissParams struct {
	// Paths to dismiss; empty dismisses all.
	Paths []string `json:"paths,omitempty"`
}

const recoveryFile = "recovery.json"

// recoveryPath returns the recovery index of the open project.
func (a *App) recoveryPath() (string, error) {
	pr, err := a.requireProject()
	if err != nil {
		return "", err
	}
	return filepath.Join(pr.settings.Dir(), recoveryFile), nil
}

func readRecovery(file string) ([]RecoveryPoint, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return []RecoveryPoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	var pts []RecoveryPoint
	if err := json.Unmarshal(data, &pts); err != nil {
		return nil, fmt.Errorf("recovery index %s: %w", file, err)
	}
	if pts == nil {
		pts = []RecoveryPoint{}
	}
	return pts, nil
}

func writeRecovery(file string, pts []RecoveryPoint) error {
	if len(pts) == 0 {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(pts, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	_, err = writeFileAtomic(file, data)
	return err
}

// preserveUnsaved records the content of every modified document as a
// Local History revision labelled "recovery: <reason>" and adds it to the
// project's recovery index. It logs what it did; cause is the error that
// prevented a normal save (may be nil when documents are being discarded).
// Without a project nothing can be recorded, which is logged as an error.
func (a *App) preserveUnsaved(reason string, cause error) []RecoveryPoint {
	var dirty []DocSnapshot
	for _, d := range a.Documents() {
		if d.Modified {
			dirty = append(dirty, d)
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	errText := ""
	if cause != nil {
		errText = cause.Error()
	}
	hist, err := a.projectHistory()
	if err != nil {
		for _, d := range dirty {
			a.log.Error("unsaved changes lost: no Local History", "file", d.File, "reason", reason, "cause", errText, "err", err)
		}
		return nil
	}
	file, err := a.recoveryPath()
	if err != nil {
		return nil
	}
	pts, err := readRecovery(file)
	if err != nil {
		a.log.Error("recovery index unreadable, starting a new one", "err", err)
		pts = []RecoveryPoint{}
	}
	var added []RecoveryPoint
	for _, d := range dirty {
		rev, _, err := hist.Save(d.File, d.Text, "recovery: "+reason)
		if err != nil {
			a.log.Error("unsaved changes could not be preserved", "file", d.File, "reason", reason, "cause", errText, "err", err)
			continue
		}
		pt := RecoveryPoint{Path: d.Path, File: d.File, Revision: rev.ID, Time: rev.Time, Reason: reason, Error: errText}
		kept := pts[:0]
		for _, p := range pts {
			if p.File == pt.File && p.Revision == pt.Revision {
				pt = p // same content recorded before: keep the original reason
			} else if p.File != pt.File {
				kept = append(kept, p)
			}
		}
		pts = append(kept, pt)
		added = append(added, pt)
		a.log.Error("unsaved changes preserved in Local History", "file", d.File, "revision", rev.ID, "reason", reason, "cause", errText)
	}
	if err := writeRecovery(file, pts); err != nil {
		a.log.Error("recovery index not written", "err", err)
	}
	return added
}

func (a *App) historyRecovery(_ context.Context, _ struct{}) ([]RecoveryPoint, error) {
	file, err := a.recoveryPath()
	if err != nil {
		return nil, err
	}
	pts, err := readRecovery(file)
	if err != nil {
		return nil, Errorf(CodeFailed, "recovery: %w", err)
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].Time.After(pts[j].Time) })
	return pts, nil
}

func (a *App) historyRecoveryDismiss(_ context.Context, p RecoveryDismissParams) ([]RecoveryPoint, error) {
	file, err := a.recoveryPath()
	if err != nil {
		return nil, err
	}
	pts, err := readRecovery(file)
	if err != nil {
		return nil, Errorf(CodeFailed, "recovery: %w", err)
	}
	if len(p.Paths) == 0 {
		pts = []RecoveryPoint{}
	} else {
		drop := map[string]bool{}
		for _, path := range p.Paths {
			abs, err := a.resolvePath(path)
			if err != nil {
				return nil, err
			}
			drop[abs] = true
		}
		kept := []RecoveryPoint{}
		for _, pt := range pts {
			if !drop[pt.File] {
				kept = append(kept, pt)
			}
		}
		pts = kept
	}
	if err := writeRecovery(file, pts); err != nil {
		return nil, Errorf(CodeFailed, "recovery: %w", err)
	}
	return pts, nil
}
