package qakit

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
)

// Setting candidates of the quality tool plugins (D521): the tool's
// scripts in the Composer bin dirs (vendor/bin/phpstan, a custom
// vendor-dir, nested Composer projects) and its configuration files,
// resolved against the PHP root.

// provideCandidates registers the candidates of the plugin's path and
// configuration settings; Start calls it.
func (p *Plugin) provideCandidates(ctx module.Context) error {
	d := p.def
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{p.key(KeyPath)}, Source: d.Name + " scripts in the Composer bin folders", Setup: true,
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return api.ToolCandidates(q, d.Name, d.Program), nil
		}}); err != nil {
		return err
	}
	if d.FormatProgram != "" {
		if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{p.key(KeyFormatPath)}, Source: d.FormatProgram + " scripts in the Composer bin folders", Setup: true,
			Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
				return api.ToolCandidates(q, d.FormatProgram, d.FormatProgram), nil
			}}); err != nil {
			return err
		}
	}
	names := d.ConfigFiles
	if len(names) == 0 {
		return nil
	}
	return api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{p.key(KeyConfig)}, Source: d.Name + " configuration files in the project", Setup: true,
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return api.ConfigCandidates(q, d.Name+" configuration", names...), nil
		}})
}

// SettingsPage implements module.PageProvider (D525).
func (p *Plugin) SettingsPage() (section, title string) { return "Quality Tools", p.def.Name }
