package autosave

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// externalDocs refuses to save paths changed on disk unless forced.
type externalDocs struct {
	*fakeDocs
	external map[string]bool
	forced   []string
}

func (e *externalDocs) Save(ctx context.Context, d Doc) error {
	if e.external[d.Path] && !d.Force {
		return fmt.Errorf("%s: %w", d.Path, ErrExternalChange)
	}
	if d.Force {
		e.forced = append(e.forced, d.Path)
	}
	return e.fakeDocs.Save(ctx, d)
}

// AP10.7: background autosaves skip externally changed files silently;
// all other triggers report them; Force is passed through.
func TestExternalChangeIsNeverOverwritten(t *testing.T) {
	docs := &externalDocs{fakeDocs: newDocs(), external: map[string]bool{"/ext": true}}
	m, _ := New(Config{Docs: docs})
	ctx := context.Background()
	for _, tr := range []Trigger{TriggerIdle, TriggerFocusLost} {
		docs.edit("/ext", "mine")
		docs.edit("/ok", "fine "+string(tr))
		if err := m.SaveAll(ctx, tr); err != nil {
			t.Fatalf("%s: %v", tr, err)
		}
		if docs.onDisk("/ext") != "" || docs.onDisk("/ok") != "fine "+string(tr) {
			t.Fatalf("%s: disk %q %q", tr, docs.onDisk("/ext"), docs.onDisk("/ok"))
		}
	}
	for _, tr := range []Trigger{TriggerBeforeCommit, TriggerManual, TriggerClose} {
		if err := m.SaveAll(ctx, tr); !errors.Is(err, ErrExternalChange) {
			t.Fatalf("%s: err = %v", tr, err)
		}
	}
	if _, err := m.SaveWith(ctx, TriggerManual, func() (Doc, error) {
		return Doc{Path: "/ext", Content: []byte("forced"), Version: 99, Force: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if docs.onDisk("/ext") != "forced" || len(docs.forced) != 1 {
		t.Fatalf("force: %q %v", docs.onDisk("/ext"), docs.forced)
	}
}
