package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func init() {
	register(&command{name: "php-root", usage: "php-root | php-root set DIR | php-root reset",
		summary: "PHP project root (folder with composer.json that composer, vendor/, phpunit.xml and QA tools use): show with candidates, set or reset to detection",
		maxArgs: 2, cwdProject: true, run: runPHPRoot})
}

func runPHPRoot(c *cli) error {
	usage := usagef("usage: tiegel --headless %s", commands["php-root"].usage)
	args := c.p.args
	switch {
	case len(args) == 0:
	case args[0] == "set" && len(args) == 2:
		var pi api.ProjectInfo
		if err := c.call("project.info", nil, &pi); err != nil {
			return err
		}
		dir := args[1]
		if filepath.IsAbs(dir) {
			// The project root has its symlinks resolved (macOS /var).
			if r, err := filepath.EvalSymlinks(dir); err == nil {
				dir = r
			}
			if r, err := filepath.Rel(pi.Root, dir); err == nil {
				dir = r
			}
		}
		dir = filepath.ToSlash(filepath.Clean(filepath.FromSlash(dir)))
		raw, _ := json.Marshal(dir)
		var v api.SettingValue
		if err := c.call("settings.set", api.SettingsSetParams{Key: settings.KeyPHPRoot, Scope: string(settings.ScopeProject), Value: raw}, &v); err != nil {
			return err
		}
		var info api.PHPRootInfo
		if err := c.call("project.phpRoot", nil, &info); err != nil {
			return err
		}
		if info.Auto {
			// Not a folder inside the project: keep the detection.
			_ = c.call("settings.set", api.SettingsSetParams{Key: settings.KeyPHPRoot, Scope: string(settings.ScopeProject)}, &v)
			return api.Errorf(api.CodeInvalidParams, "%s is no folder inside the project", args[1])
		}
	case args[0] == "reset" && len(args) == 1:
		var v api.SettingValue
		if err := c.call("settings.set", api.SettingsSetParams{Key: settings.KeyPHPRoot, Scope: string(settings.ScopeProject)}, &v); err != nil {
			return err
		}
	default:
		return usage
	}
	var info api.PHPRootInfo
	if err := c.call("project.phpRoot", nil, &info); err != nil {
		return err
	}
	return c.emit(info, func(w io.Writer) {
		how := "setting " + settings.KeyPHPRoot
		if info.Auto {
			how = "detected"
		}
		fmt.Fprintf(w, "PHP root: %s (%s)\n", info.Path, how)
		for _, cd := range info.Candidates {
			mark := " "
			if cd.Recommended {
				mark = "*"
			}
			fmt.Fprintf(w, "  %s %-20s %s\n", mark, cd.Path, strings.Join(cd.Reasons, ", "))
		}
	})
}
