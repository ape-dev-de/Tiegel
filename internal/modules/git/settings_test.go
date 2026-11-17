package git

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// TestUpdateMethodSetting: without an explicit strategy, git.pull uses the
// declared setting git.updateMethod (rebase keeps the history linear).
func TestUpdateMethodSetting(t *testing.T) {
	a, dir := setup(t)
	other := remoteSetup(t, dir)

	var s api.SettingsSchema
	modtest.Call(t, a, "settings.schema", api.SchemaParams{Module: ID}, &s)
	if len(s.Modules) != 1 || len(s.Modules[0].Settings) != 1 || s.Modules[0].Settings[0].Key != SettingUpdateMethod {
		t.Fatalf("schema %+v", s)
	}
	modtest.WantCode(t, a, "settings.set", api.SettingsSetParams{Key: SettingUpdateMethod, Scope: "project", Value: json.RawMessage(`"octopus"`)}, api.CodeInvalidParams)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingUpdateMethod, Scope: "project", Value: json.RawMessage(`"rebase"`)}, nil)

	// Diverging, non-conflicting commits.
	write(t, other, "b.txt", "theirs\n")
	modtest.Git(t, other, "commit", "-q", "-am", "theirs")
	modtest.Git(t, other, "push", "-q", "origin", "main")
	write(t, dir, "c.txt", "mine\n")
	modtest.Git(t, dir, "add", "c.txt")
	modtest.Git(t, dir, "commit", "-q", "-m", "mine")

	var pr PullResult
	modtest.Call(t, a, "git.pull", PullParams{}, &pr)
	if len(pr.Conflicts) != 0 {
		t.Fatalf("pull %+v", pr)
	}
	if merges := strings.TrimSpace(modtest.Git(t, dir, "rev-list", "--merges", "HEAD")); merges != "" {
		t.Fatalf("rebase expected, found merge commits %s", merges)
	}
	if read(t, dir, "b.txt") != "theirs\n" {
		t.Fatal("remote change missing")
	}
}
