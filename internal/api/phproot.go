package api

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/phproot"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// The PHP project root (D515): the folder with composer.json that
// composer, vendor/, phpunit.xml, PSR-4 and the quality tools resolve
// from. It is the project root unless the PHP project lives in a
// subfolder; the setting project.phpRoot overrides the detection.

// TopicPHPRootChanged is published (PHPRootInfo) when the effective PHP
// root changes: the setting changed or a composer.json appeared or
// disappeared.
const TopicPHPRootChanged = "project.phpRootChanged"

// PHPRootCandidate is a folder that may be the PHP root.
type PHPRootCandidate = phproot.Candidate

// PHPRootInfo is the result of "project.phpRoot".
type PHPRootInfo struct {
	// Path is the effective PHP root, project relative and slash
	// separated ("." = the project root).
	Path string `json:"path"`
	// Abs is Path as absolute directory.
	Abs string `json:"abs"`
	// Auto is true when the detection decided (setting empty or
	// invalid).
	Auto bool `json:"auto"`
	// Setting is the value of project.phpRoot ("" = automatic).
	Setting string `json:"setting,omitempty"`
	// Candidates are the folders with composer.json, best first.
	Candidates []PHPRootCandidate `json:"candidates"`
}

// phpRootCache caches the effective PHP root of the open project; it is
// dropped when the setting or a composer.json changes.
type phpRootCache struct {
	mu sync.Mutex
	st phpRootState
	// announced is the PHP root clients know (of project announcedFor):
	// project.phpRootChanged is published when the effective one
	// differs.
	announced, announcedFor string
}

type phpRootState struct {
	root    string // project root it belongs to
	setting string
	abs     string
	auto    bool
	valid   bool
}

func (a *App) registerPHPRoot() {
	mustHandle(a, "project.phpRoot", "The PHP project root (composer.json, vendor/, phpunit.xml, PSR-4, QA tools resolve from it): effective path, whether it was detected, and the candidates", a.phpRootInfo)
	settings.Changed.Subscribe(a.bus, func(c settings.Change) {
		if c.Key == settings.KeyPHPRoot {
			a.refreshPHPRoot(true)
		}
	})
	a.bus.Subscribe(TopicFilesChanged, func(e bus.Event) {
		ev, ok := e.Payload.(FilesChangedEvent)
		if !ok {
			return
		}
		for _, c := range ev.Changes {
			removedDir := c.Dir && c.Kind != FileCreated && c.Kind != FileModified
			if phpRootRelevant(c.Path) || phpRootRelevant(c.OldPath) || removedDir {
				a.refreshPHPRoot(true)
				return
			}
		}
	})
	a.bus.Subscribe(TopicProjectClosed, func(bus.Event) {
		a.phpRoot.mu.Lock()
		a.phpRoot.st = phpRootState{}
		a.phpRoot.mu.Unlock()
	})
	// project.info reports the PHP root of a freshly opened project.
	a.bus.Subscribe(TopicProjectOpened, func(bus.Event) { a.refreshPHPRoot(false) })
}

// phpRootRelevant reports whether a change of the project relative path
// p may change the detection (composer.json, composer.lock, phpunit.xml
// or vendor/ up to the search depth).
func phpRootRelevant(p string) bool {
	if p == "" {
		return false
	}
	p = filepath.ToSlash(p)
	if strings.Count(strings.Trim(p, "/"), "/") > phproot.MaxDepth {
		return false
	}
	switch path.Base(p) {
	case "composer.json", "composer.lock", "vendor", "phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml":
		return true
	}
	return false
}

// PHPRoot returns the effective PHP project root (absolute): the setting
// project.phpRoot, else the detected folder with composer.json, else the
// project root; "" without project. Modules resolve composer.json,
// vendor/bin, phpunit.xml and PSR-4 from it.
func (a *App) PHPRoot() string {
	abs, _ := a.resolvePHPRoot()
	return abs
}

func (a *App) resolvePHPRoot() (string, bool) {
	root := a.ProjectRoot()
	if root == "" {
		return "", true
	}
	setting := Setting(a, settings.KeyPHPRoot, "")
	c := &a.phpRoot
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.st; st.valid && st.root == root && st.setting == setting {
		return st.abs, st.auto
	}
	abs, auto := phproot.Resolve(root, setting)
	c.st = phpRootState{root: root, setting: setting, abs: abs, auto: auto, valid: true}
	return abs, auto
}

// refreshPHPRoot recomputes the PHP root and publishes
// project.phpRootChanged (when publish is set) if it differs from the one
// clients know.
func (a *App) refreshPHPRoot(publish bool) {
	c := &a.phpRoot
	root := a.ProjectRoot()
	c.mu.Lock()
	c.st.valid = false
	c.mu.Unlock()
	abs, _ := a.resolvePHPRoot()
	if abs == "" {
		return
	}
	c.mu.Lock()
	changed := c.announcedFor == root && c.announced != abs
	c.announced, c.announcedFor = abs, root
	c.mu.Unlock()
	if !changed || !publish {
		return
	}
	info, err := a.phpRootInfo(context.Background(), struct{}{})
	if err == nil {
		a.bus.Publish(TopicPHPRootChanged, info)
	}
}

func (a *App) phpRootInfo(_ context.Context, _ struct{}) (PHPRootInfo, error) {
	pr, err := a.requireProject()
	if err != nil {
		return PHPRootInfo{}, err
	}
	abs, auto := a.resolvePHPRoot()
	info := PHPRootInfo{
		Path: phproot.Rel(pr.root, abs), Abs: abs, Auto: auto,
		Setting:    Setting(a, settings.KeyPHPRoot, ""),
		Candidates: phproot.Detect(pr.root),
	}
	if info.Candidates == nil {
		info.Candidates = []PHPRootCandidate{}
	}
	return info, nil
}
