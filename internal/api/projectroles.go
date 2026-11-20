package api

import (
	"context"
	"errors"

	"github.com/ape-dev-de/tiegel/internal/core/projectroles"
)

// ProjectRoot is a folder with a role (see "project.roles").
type ProjectRoot = projectroles.Root

// ProjectRolesResult is the result of "project.roles": the source, test,
// resource and library roots of the project, sorted by path. Warning is
// set when composer.json is invalid (the roots then come from the folder
// names only).
type ProjectRolesResult struct {
	Roots   []ProjectRoot `json:"roots"`
	Warning string        `json:"warning,omitempty"`
}

func (a *App) registerProjectRoles() {
	mustHandle(a, "project.roles", "Folder roles (source/test/resource/library roots) from composer.json autoload, autoload-dev and vendor-dir, else from folder names", a.projectRoles)
}

func (a *App) projectRoles(_ context.Context, _ struct{}) (ProjectRolesResult, error) {
	pr, err := a.requireProject()
	if err != nil {
		return ProjectRolesResult{}, err
	}
	roots, err := projectroles.Detect(pr.root)
	res := ProjectRolesResult{Roots: roots}
	if res.Roots == nil {
		res.Roots = []ProjectRoot{}
	}
	if err != nil {
		if !errors.Is(err, projectroles.ErrComposer) {
			return ProjectRolesResult{}, Errorf(CodeFailed, "project roles: %w", err)
		}
		res.Warning = err.Error()
	}
	return res, nil
}
