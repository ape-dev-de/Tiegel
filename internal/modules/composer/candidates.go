package composer

import (
	"context"
	"os/exec"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
)

// Setting candidates of the composer plugin (D521): composer.phar files
// and Composer bin scripts around the PHP root, composer in PATH, and the
// interpreters for composer.interpreter.
func provideCandidates(ctx module.Context, app *api.App) error {
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingPath}, Source: "composer.phar in the project and composer in PATH",
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			out := api.ToolCandidates(q, "Composer", "composer")
			if p, err := exec.LookPath("composer"); err == nil {
				out = append(out, api.SettingCandidate{Value: "composer", Label: "composer (PATH)", Detail: p, Recommended: len(out) == 0})
			}
			return out, nil
		}}); err != nil {
		return err
	}
	return api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingInterp}, Source: "project interpreter, host or an interpreter",
		Find: func(c context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return app.InterpreterRefCandidates(c, q), nil
		}})
}

// SettingsPage implements module.PageProvider (D525).
func (m *Module) SettingsPage() (section, title string) { return "PHP", "Composer" }
