package lspplugins

// SettingsPage implements module.PageProvider (D525).
func (p *Plugin) SettingsPage() (section, title string) { return "PHP", p.def.Name }
