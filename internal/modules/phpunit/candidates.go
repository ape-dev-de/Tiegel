package phpunit

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
)

// Setting candidates of the phpunit plugin (D521): PHPUnit scripts in the
// Composer bin dirs (also nested projects like project/vendor/bin/phpunit
// or a custom vendor-dir), phpunit.xml files in the PHP root and the
// project, and the interpreters. All of them are resolved against the PHP
// root (api.App.ToolRoot).

// candidateConfigNames are the configuration files PHPUnit finds itself,
// in order.
var candidateConfigNames = []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"}

// provideCandidates registers the candidate providers; Start calls it.
func provideCandidates(ctx module.Context, app *api.App) error {
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingExecutable}, Source: "PHPUnit scripts in the Composer bin folders", Setup: true,
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return api.ToolCandidates(q, "PHPUnit script", "phpunit"), nil
		}}); err != nil {
		return err
	}
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingConfig}, Source: "phpunit.xml files in the project", Setup: true,
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return api.ConfigCandidates(q, "PHPUnit configuration", candidateConfigNames...), nil
		}}); err != nil {
		return err
	}
	return api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingInterp}, Source: "project interpreter, host or an interpreter",
		Find: func(c context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return app.InterpreterRefCandidates(c, q), nil
		}})
}

// SettingsPage implements module.PageProvider (D525).
func (m *Module) SettingsPage() (section, title string) { return "Tests", "PHPUnit" }
