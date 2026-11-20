package headless

import (
	"fmt"
	"io"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	register(&command{name: "project-roles", usage: "project-roles", summary: "source/test/resource/library folders (composer.json autoload, else folder names)",
		maxArgs: 0, cwdProject: true, run: runProjectRoles})
}

func runProjectRoles(c *cli) error {
	var res api.ProjectRolesResult
	if err := c.call("project.roles", nil, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, r := range res.Roots {
			fmt.Fprintf(w, "%-8s %s (%s)\n", r.Role, r.Path, r.Origin)
		}
		if res.Warning != "" {
			fmt.Fprintf(c.env.Stderr, "warning: %s\n", res.Warning)
		}
	})
}
