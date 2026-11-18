package lsp

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

func TestInlayHints(t *testing.T) {
	c, srv := connectFake(t, fakeserver.Options{}, Config{})
	ctx := timeout(t, 5*time.Second)
	if err := c.DidOpen(ctx, uri, "php", 1, "<?php\ncall($a);\n$typed = 1;\n"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "document synced", func() bool { return len(srv.Snapshot().Docs) == 1 })
	if !c.CanInlayHints() {
		t.Fatal("capability")
	}
	hs, err := c.InlayHints(ctx, uri, protocol.Range{End: protocol.Position{Line: 3}})
	if err != nil || len(hs) != 2 {
		t.Fatalf("%+v %v", hs, err)
	}
	if hs[0].LabelText() != "arg:" || hs[0].Kind != protocol.InlayHintParameter || hs[0].Position != (protocol.Position{Line: 1, Character: 5}) || !hs[0].PaddingRight {
		t.Fatalf("parameter %+v", hs[0])
	}
	if hs[1].LabelText() != ": int" || hs[1].Kind != protocol.InlayHintType || hs[1].Position != (protocol.Position{Line: 2, Character: 6}) {
		t.Fatalf("type %+v", hs[1])
	}
	if (protocol.InlayHint{Label: json.RawMessage(`3`)}).LabelText() != "" {
		t.Fatal("bad label")
	}
}

func TestInlayHintsNotSupported(t *testing.T) {
	c := &Client{}
	if _, err := c.InlayHints(timeout(t, time.Second), uri, protocol.Range{}); !errors.Is(err, ErrNotSupported) {
		t.Fatal(err)
	}
}
