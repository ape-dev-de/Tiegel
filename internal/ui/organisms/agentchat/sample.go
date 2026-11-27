package agentchat

// Sample fills c with a representative session (catalog, tests).
func Sample(c *Chat) {
	c.Agent, c.State, c.Model = "OpenCode 2.2.0", "ready", "anthropic/sonnet"
	c.Models = []Choice{{Value: "anthropic/sonnet", Name: "Claude Sonnet"}, {Value: "openai/gpt", Name: "GPT"}}
	c.Policy = "Edits: allowed · Shell: ask · Commits: blocked · Push: blocked · Web: ask"
	c.Entries = []Entry{
		{Key: 0, Kind: User, Text: "Rename greet() to welcome() and run the tests.", Context: []ContextBlock{{Title: "Context from project knowledge", Tokens: 412, Hits: []ContextHit{
			{Title: "Shop greeting", Heading: "Data flows › Greeting", Sources: []string{"src/Shop.php:12", "src/Mailer.php:40"}, Generated: "2026-11-20", File: "/k/processes/shop.md", Anchor: "greeting"},
			{Title: "App\\Mailer", Heading: "Contract", Sources: []string{"src/Mailer.php:8"}, Generated: "2026-11-02", Outdated: true, File: "/k/components/mailer.md", Anchor: "contract"},
		}}}},
		{Key: 1, Kind: Thought, Text: "The function is used in two files.\nI should update both."},
		{Key: 2, Kind: Plan, Plan: []PlanItem{{Text: "Rename the function", Status: "completed"}, {Text: "Update callers", Status: "in_progress"}, {Text: "Run tests"}}},
		{Key: 3, Kind: Tool, Tool: &ToolCall{Title: "edit", Status: "completed", Paths: []string{"src/Shop.php"},
			Diffs: []DiffStat{{Path: "src/Shop.php", Added: 3, Removed: 2}}, Output: "Edited src/Shop.php"}},
		{Key: 4, Kind: Notice, Level: Blocked, Text: "Blocked: git commit (disabled in settings) – Run: git commit -am rename"},
		{Key: 5, Kind: Agent, Text: "I renamed `greet()` to **welcome()** in `src/Shop.php`.\n\n- updated 2 callers\n- tests are next"},
		{Key: 6, Kind: Tool, Tool: &ToolCall{Title: "phpunit", Status: "in_progress", Command: "vendor/bin/phpunit"}},
		{Key: 7, Kind: Permission, Perm: &Request{ID: "p1", Summary: "Run: vendor/bin/phpunit", Options: []Option{
			{ID: "once", Name: "Allow once", Kind: "allow_once"}, {ID: "always", Name: "Always", Kind: "allow_always"}, {ID: "reject", Name: "Reject", Kind: "reject_once"}}}},
		{Key: 8, Kind: Notice, Level: Error, Text: "The agent failed: rate limited"},
	}
	c.Changes = []Change{{Path: "src/Shop.php", Kind: "modified", Added: 3, Removed: 2}, {Path: "src/New.php", Kind: "created", Added: 12}, {Path: "old.txt", Kind: "deleted", Removed: 4, Conflict: true}}
}
