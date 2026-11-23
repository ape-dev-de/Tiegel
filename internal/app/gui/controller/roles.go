package controller

import (
	"context"
	"path/filepath"
)

// FolderRole is a folder with a role ("source", "test", "resource",
// "library") from project.roles: composer.json autoload/autoload-dev and
// vendor-dir, else conventional folder names.
type FolderRole struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

// RoleOf returns the role of the project-relative folder rel ("" if it
// has none).
func (c *Controller) RoleOf(rel string) string {
	return c.roles[rel]
}

// LoadRoles reloads the folder roles (project.roles); the project tree
// icons follow on the next frame (RolesVersion).
func (c *Controller) LoadRoles() {
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Roots []FolderRole `json:"roots"`
		}
		err := c.call(ctx, "project.roles", nil, &res)
		return func() {
			m := map[string]string{}
			if err == nil {
				for _, r := range res.Roots {
					m[r.Path] = r.Role
				}
			}
			c.roles = m
			c.RolesVersion++
		}
	})
}

// rolesFileChanged reloads the roles when composer.json of the PHP
// project root was saved.
func (c *Controller) rolesFileChanged(file string) {
	if c.Project.Open && file == filepath.Join(c.PHPRoot(), "composer.json") {
		c.LoadRoles()
	}
}
