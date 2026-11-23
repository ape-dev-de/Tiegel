package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
)

// Setting candidates of the docker module (D521, D523): compose file sets
// found around the project, docker binaries in PATH and the usual install
// folders, and the Docker contexts of the local CLI configuration. None of
// them contacts the engine.

func (m *Module) provideCandidates(ctx module.Context) error {
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{api.SettingComposeFiles}, Source: "compose files in the project, its subfolders and parent folders",
		Find: m.composeFileCandidates}); err != nil {
		return err
	}
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingPath}, Source: "docker in PATH and the usual install folders",
		Find: m.binaryCandidates}); err != nil {
		return err
	}
	return api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingContext}, Source: "Docker contexts (local CLI configuration)",
		Find: m.contextCandidates})
}

func (m *Module) composeFileCandidates(ctx context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
	if q.Root == "" {
		return nil, nil
	}
	d, err := compose.Discover(ctx, q.Root, compose.DiscoverOptions{PHPRoot: q.PHPRoot, Env: m.app.Options().ComposeEnv})
	if err != nil {
		return nil, err
	}
	best := -1
	for i, s := range d.Sets {
		if _, ok := s.Best(); ok {
			best = i
			break
		}
	}
	var out []api.SettingCandidate
	rel := func(p string) string {
		if r, err := filepath.Rel(q.Root, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	for i, s := range d.Sets {
		if s.Error != "" {
			continue
		}
		files := make([]string, len(s.Files))
		names := make([]string, len(s.Files))
		for j, f := range s.Files {
			files[j], names[j] = rel(f), filepath.Base(f)
		}
		detail := fmt.Sprintf("%s (%s", rel(s.Dir), s.Relation)
		if s.Levels > 0 {
			detail += fmt.Sprintf(", %d level(s)", s.Levels)
		}
		detail += ")"
		if c, ok := s.Best(); ok {
			detail += fmt.Sprintf("; PHP service %q", c.Service)
			if c.Mapping != nil {
				detail += fmt.Sprintf(" maps %s → %s", rel(c.Mapping.Host), c.Mapping.Remote)
			}
		}
		out = append(out, api.SettingCandidate{Value: files, Label: strings.Join(names, " + ") + " in " + rel(s.Dir), Detail: detail, Recommended: i == best && s.Relation != compose.RelationRoot})
		// Each optional overlay as a further choice on top of the set.
		for _, ov := range s.Overlays {
			out = append(out, api.SettingCandidate{Value: append(append([]string(nil), files...), rel(ov)),
				Label: strings.Join(names, " + ") + " + " + filepath.Base(ov), Detail: "with the optional overlay " + rel(ov)})
		}
	}
	return out, nil
}

func (m *Module) binaryCandidates(ctx context.Context, _ api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
	home, _ := os.UserHomeDir()
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if m.opts.CommonDirs != nil {
		dirs = append(dirs, m.opts.CommonDirs...)
	} else {
		dirs = append(dirs, dk.CommonDirs(runtime.GOOS, home)...)
	}
	found, _ := m.binary(ctx)
	seen := map[string]bool{}
	var out []api.SettingCandidate
	for _, d := range dirs {
		p, err := shellenv.LookPath("docker", d)
		if err != nil || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, api.SettingCandidate{Value: p, Label: p, Detail: "found in " + d, Recommended: p == found.Path})
	}
	return out, nil
}

func (m *Module) contextCandidates(ctx context.Context, _ api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
	c, _, err := m.client(ctx, "")
	if err != nil {
		return nil, nil
	}
	list, err := c.Contexts(ctx)
	if err != nil {
		return nil, nil
	}
	out := []api.SettingCandidate{{Value: "", Label: "docker's current context", Detail: "follows `docker context use`"}}
	for _, ci := range list {
		detail := ci.Endpoint
		if ci.Remote {
			detail += " (remote)"
		}
		out = append(out, api.SettingCandidate{Value: ci.Name, Label: ci.Name, Detail: detail, Recommended: ci.Current})
	}
	return out, nil
}

// SettingsPage implements module.PageProvider (D525).
func (m *Module) SettingsPage() (section, title string) {
	return "Docker & Compose", "Docker & Compose"
}
