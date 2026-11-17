package syntaxmod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestSettingsAndAssociations(t *testing.T) {
	a, _ := newApp(t)
	big := "<?php\n" + strings.Repeat("echo 1;\n", 3000) // ~24 KB
	modtest.Project(t, a, map[string]string{"big.php": big, "app.cfg": "A=1\n"})
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingMaxFileKB, Value: json.RawMessage(`16`)}, nil)
	var f FoldsResult
	modtest.Call(t, a, "syntax.folds", Source{Path: "big.php"}, &f)
	if f.Language != "" || len(f.Folds) != 0 {
		t.Fatalf("big file not plain: %+v", f.Info)
	}
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "big.php"}, nil)
	modtest.Call(t, a, "syntax.folds", Source{Path: "big.php"}, &f)
	if f.Language != "" || !f.Open {
		t.Fatalf("big doc not plain: %+v", f.Info)
	}

	// files.associations is honoured for files that are not open.
	modtest.Call(t, a, "syntax.folds", Source{Path: "app.cfg"}, &f)
	if f.Language != "" {
		t.Fatalf("before %+v", f.Info)
	}
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: "files.associations", Scope: "project", Value: json.RawMessage(`["*.cfg=dotenv"]`)}, nil)
	modtest.Call(t, a, "syntax.folds", Source{Path: "app.cfg"}, &f)
	if f.Language != "env" {
		t.Fatalf("after %+v", f.Info)
	}
}
