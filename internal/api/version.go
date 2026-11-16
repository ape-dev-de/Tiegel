package api

import (
	"context"
	"errors"

	"github.com/ape-dev-de/tiegel/internal/update"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// VersionInfo is the result of "app.version".
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Dev     bool   `json:"dev"`
	// String is the one-line description (version.String()).
	String     string `json:"string"`
	APIVersion string `json:"apiVersion"`
}

// UpdateCheckParams are the params of "update.check".
type UpdateCheckParams struct {
	// Source overrides the release feed (https, or http on loopback).
	Source string `json:"source,omitempty"`
	// Prereleases also offers pre-release versions.
	Prereleases bool `json:"prereleases,omitempty"`
}

// UpdateCheckResult is the result of "update.check".
type UpdateCheckResult struct {
	Current   string `json:"current"`
	Available bool   `json:"available"`
	Latest    string `json:"latest,omitempty"`
	URL       string `json:"url,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Source    string `json:"source"`
}

func (a *App) registerVersion() {
	mustHandle(a, "app.version", "Build version, commit and date", func(context.Context, struct{}) (VersionInfo, error) {
		return VersionInfo{
			Version: version.Version, Commit: version.Commit, Date: version.Date, Dev: version.IsDev(),
			String: version.String(), APIVersion: Version,
		}, nil
	})
	mustHandle(a, "update.check", "Check for a newer release (network access; only on explicit request)", a.updateCheck)
}

// updateCheck always runs the check (CheckNow): calling this method is the
// explicit user action the privacy rules require. Nothing is downloaded.
func (a *App) updateCheck(ctx context.Context, p UpdateCheckParams) (UpdateCheckResult, error) {
	src := p.Source
	if src == "" {
		src = a.opts.UpdateSource
	}
	c := update.New(update.Config{Source: src, IncludePrereleases: p.Prereleases}, a.opts.UpdateClient)
	res := UpdateCheckResult{Current: version.Version, Source: c.Source()}
	r, err := c.CheckNow(ctx, version.Version)
	if err != nil {
		if errors.Is(err, update.ErrNoRelease) {
			return res, Errorf(CodeNotFound, "update check: %w", err)
		}
		return res, Errorf(CodeUnavailable, "update check: %w", err)
	}
	res.Available, res.Latest, res.URL, res.Notes = r.Available, r.Version, r.URL, r.Notes
	return res, nil
}
