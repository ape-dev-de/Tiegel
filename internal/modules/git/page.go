package git

// SettingsPage implements module.PageProvider (D525).
func (m *Module) SettingsPage() (section, title string) { return "Version Control", "Git" }
