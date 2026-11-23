package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/toolfind"
)

// Setting candidates (D521): instead of typing a path, a service name or an
// interpreter ID, the user picks from values discovered on disk. Modules
// provide candidates for their keys through the settings.candidates filter
// (ProvideCandidates); "settings.candidates" returns them, the dialog shows
// them as a choice list with free input kept ("Custom…"), and the project
// setup (project.setupProposal, D526) proposes the recommended ones.
// Discovery reads local files only.

// SettingCandidate is a value proposed for a setting.
type SettingCandidate struct {
	// Value is the value to store (string for most kinds, []string for
	// lists, bool, number).
	Value any `json:"value"`
	// Label is the short text of the choice; Detail explains it (where it
	// was found, why it is recommended).
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	// Recommended marks the best choice (at most one per provider).
	Recommended bool `json:"recommended,omitempty"`
	// Current marks the candidate that already applies (equal to the
	// effective value, possibly written differently).
	Current bool `json:"current,omitempty"`
}

// SettingCandidatesQuery is the value of the settings.candidates filter.
// A provider appends to Candidates when Key is one of its keys.
type SettingCandidatesQuery struct {
	Key string
	// Root is the project root ("" without project); PHPRoot the PHP
	// root (see App.ToolRoot), equal to Root by default.
	Root    string
	PHPRoot string
	// Current is the effective value of Key.
	Current    json.RawMessage
	Candidates []SettingCandidate
}

// HookSettingCandidates collects setting candidates (filter, D521).
var HookSettingCandidates = hooks.NewFilter[SettingCandidatesQuery]("settings.candidates")

// CandidateProvider declares the keys a provider answers.
type CandidateProvider struct {
	// Keys are the setting keys.
	Keys []string
	// Source describes where the values come from (Spec.Candidates).
	Source string
	// Setup lists the keys in the project setup proposal (D526).
	Setup bool
	// Find returns the candidates of key.
	Find func(ctx context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error)
}

type candidateDecl struct {
	owner  string
	source string
	setup  bool
}

// candidateRegistry records which keys have providers (for the schema
// and the setup proposal).
type candidateRegistry struct {
	mu    sync.Mutex
	byKey map[string][]candidateDecl
}

func (r *candidateRegistry) add(owner string, p CandidateProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string][]candidateDecl{}
	}
	for _, k := range p.Keys {
		r.byKey[k] = append(r.byKey[k], candidateDecl{owner: owner, source: p.Source, setup: p.Setup})
	}
}

func (r *candidateRegistry) remove(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, ds := range r.byKey {
		var keep []candidateDecl
		for _, d := range ds {
			if d.owner != owner {
				keep = append(keep, d)
			}
		}
		if len(keep) == 0 {
			delete(r.byKey, k)
		} else {
			r.byKey[k] = keep
		}
	}
}

// source returns the provider description of key ("" without provider).
func (r *candidateRegistry) source(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ds := r.byKey[key]; len(ds) > 0 {
		return ds[0].source
	}
	return ""
}

func (r *candidateRegistry) setupKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k, ds := range r.byKey {
		for _, d := range ds {
			if d.setup {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

func (a *App) provideCandidates(owner string, p CandidateProvider, opts ...hooks.HookOption) func() {
	keys := map[string]bool{}
	for _, k := range p.Keys {
		keys[k] = true
	}
	a.cands.add(owner, p)
	remove := HookSettingCandidates.On(a.hooks, 0, func(ctx context.Context, q SettingCandidatesQuery) (SettingCandidatesQuery, error) {
		if !keys[q.Key] {
			return q, nil
		}
		found, err := p.Find(ctx, q)
		if err != nil {
			return q, err
		}
		q.Candidates = append(append([]SettingCandidate(nil), q.Candidates...), found...)
		return q, nil
	}, append([]hooks.HookOption{hooks.WithOwner(owner)}, opts...)...)
	return func() {
		remove()
		a.cands.remove(owner)
	}
}

// ProvideCandidates registers a candidate provider of a module; it is
// removed when the module stops.
func ProvideCandidates(ctx module.Context, p CandidateProvider) error {
	a, ok := AppFrom(ctx)
	if !ok {
		return fmt.Errorf("api: candidates of %s: needs an api.App context", ctx.ModuleID())
	}
	ctx.OnStop(a.provideCandidates(ctx.ModuleID(), p))
	return nil
}

// CandidatesParams are the params of "settings.candidates".
type CandidatesParams struct {
	Key string `json:"key"`
}

// CandidatesResult is the result of "settings.candidates".
type CandidatesResult struct {
	Key  string `json:"key"`
	Kind string `json:"kind,omitempty"`
	// Source describes the provider ("" when none is registered).
	Source string `json:"source,omitempty"`
	// Current is the effective value.
	Current json.RawMessage `json:"current,omitempty"`
	// Candidates are ordered: recommended first, then in provider order;
	// enum options and booleans are listed too.
	Candidates []SettingCandidate `json:"candidates"`
	// Custom is true when values outside the candidates are accepted.
	Custom bool `json:"custom"`
}

func (a *App) registerCandidates() {
	mustHandle(a, "settings.candidates", "Discovered values for a setting (files, executables, services, interpreters): recommended first; local files only", a.settingsCandidates)
	mustHandle(a, "project.setupProposal", "Proposed project setup: recommended values for unconfigured settings (PHP root, interpreter, PHPUnit, QA tools) with reasons", a.setupProposal)
	mustHandle(a, "project.setupApply", "Apply chosen setup values (only what is passed) or dismiss the setup proposal for this project", a.setupApply)
	a.provideCandidates("core", CandidateProvider{Keys: []string{"terminal.shell"}, Source: "login shells (/etc/shells)", Find: func(_ context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
		var out []SettingCandidate
		for _, sh := range toolfind.Shells("") {
			out = append(out, SettingCandidate{Value: sh, Label: filepath.Base(sh), Detail: sh, Recommended: sh == os.Getenv("SHELL")})
		}
		return out, nil
	}})
	a.provideCandidates("core", CandidateProvider{Keys: []string{keyPHPRoot}, Source: "folders with composer.json", Setup: true, Find: a.phpRootCandidates})
}

// effective returns the effective value of key.
func (a *App) effective(key string) json.RawMessage {
	spec, _, _ := a.lookupSpec(key)
	raw, _ := settings.Effective(spec, key, a.global, a.ProjectSettings())
	return raw
}

func (a *App) settingsCandidates(ctx context.Context, p CandidatesParams) (CandidatesResult, error) {
	if p.Key == "" {
		return CandidatesResult{}, Errorf(CodeInvalidParams, "key is required")
	}
	return a.candidates(ctx, p.Key)
}

func (a *App) candidates(ctx context.Context, key string) (CandidatesResult, error) {
	spec, _, declared := a.lookupSpec(key)
	res := CandidatesResult{Key: key, Kind: string(spec.Kind), Source: a.cands.source(key), Candidates: []SettingCandidate{}, Custom: true}
	if res.Source == "" {
		res.Source = spec.Candidates
	}
	res.Current = a.effective(key)
	root := a.ProjectRoot()
	q := SettingCandidatesQuery{Key: key, Root: root, Current: res.Current}
	if root != "" {
		q.PHPRoot = a.ToolRoot(ctx)
	}
	q, err := HookSettingCandidates.Apply(ctx, a.hooks, q)
	if err != nil && ctx.Err() != nil {
		return res, Errorf(CodeFailed, "candidates: %w", err)
	}
	if err != nil {
		a.log.Warn("setting candidates", "key", key, "err", err)
	}
	list := q.Candidates
	if declared {
		switch spec.Kind {
		case settings.KindEnum:
			res.Custom = false
			for _, o := range spec.Options {
				list = append(list, SettingCandidate{Value: o, Label: o})
			}
		case settings.KindBool:
			res.Custom = false
			list = append(list, SettingCandidate{Value: true, Label: "on"}, SettingCandidate{Value: false, Label: "off"})
		}
	}
	var cur any
	_ = json.Unmarshal(res.Current, &cur)
	seen := map[string]bool{}
	for _, c := range list {
		k := fmt.Sprint(c.Value)
		if seen[k] {
			continue
		}
		seen[k] = true
		if !c.Current && sameValue(root, q.PHPRoot, c.Value, cur) {
			c.Current = true
		}
		res.Candidates = append(res.Candidates, c)
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].Recommended && !res.Candidates[j].Recommended
	})
	return res, nil
}

// sameValue compares a candidate with the effective value; strings that
// name the same file (relative to the project or PHP root, or absolute)
// are equal.
func sameValue(root, phpRoot string, a, b any) bool {
	if reflect.DeepEqual(normalize(a), normalize(b)) {
		return true
	}
	as, ok1 := a.(string)
	bs, ok2 := b.(string)
	if !ok1 || !ok2 || as == "" || bs == "" || root == "" {
		return false
	}
	abs := func(s, base string) string {
		if filepath.IsAbs(s) {
			return filepath.Clean(s)
		}
		return filepath.Join(base, filepath.FromSlash(s))
	}
	for _, base := range []string{root, phpRoot} {
		if base != "" && abs(as, base) == abs(bs, base) {
			if _, err := os.Stat(abs(as, base)); err == nil {
				return true
			}
		}
	}
	return false
}

// normalize converts []string to []any so JSON-decoded values compare.
func normalize(v any) any {
	if l, ok := v.([]string); ok {
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out
	}
	return v
}

// FileCandidates converts found files: the value is the absolute path
// (it works whatever directory the tool runs in), the label the path
// relative to the project. The first file of the PHP root is recommended.
func FileCandidates(found []toolfind.Found, what string) []SettingCandidate {
	out := make([]SettingCandidate, 0, len(found))
	rec := false
	for _, f := range found {
		c := SettingCandidate{Value: f.Path, Label: f.Rel, Detail: what}
		if f.InPHPRoot && !rec {
			c.Recommended, rec = true, true
			c.Detail = what + " in the PHP root"
		}
		out = append(out, c)
	}
	return out
}

// --- PHP root ---------------------------------------------------------

// keyPHPRoot is the PHP root setting (project.phpRoot: relative folder of
// composer.json, "" = auto). Its detection lives behind the
// "project.phpRoot" method; ToolRoot only asks it.
const keyPHPRoot = "project.phpRoot"

type phpRootAnswer struct {
	Path       string `json:"path"`
	Auto       bool   `json:"auto"`
	Candidates []struct {
		Path        string   `json:"path"`
		Reasons     []string `json:"reasons"`
		Recommended bool     `json:"recommended"`
	} `json:"candidates"`
}

func (a *App) absProjectPath(p string) string {
	root := a.ProjectRoot()
	if p == "" || p == "." {
		return root
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// ToolRoot returns the absolute PHP root of the open project: the answer
// of "project.phpRoot" when that method exists, else the stored
// project.phpRoot setting, else the first Composer project (the root
// itself or a subfolder up to depth 2), else the project root. Tool
// candidates (PHPUnit, QA tools, Composer) are resolved against it.
func (a *App) ToolRoot(ctx context.Context) string {
	root := a.ProjectRoot()
	if root == "" {
		return ""
	}
	var ans phpRootAnswer
	if err := a.CallLocal(ctx, "project.phpRoot", nil, &ans); err == nil && ans.Path != "" {
		return a.absProjectPath(ans.Path)
	}
	if v := Setting(a, keyPHPRoot, ""); v != "" {
		if st, err := os.Stat(a.absProjectPath(v)); err == nil && st.IsDir() {
			return a.absProjectPath(v)
		}
	}
	if ps := toolfind.ComposerProjects(root, "", 0); len(ps) > 0 {
		for _, p := range ps {
			if p.Dir == root {
				return root
			}
		}
		return ps[0].Dir
	}
	return root
}

func (a *App) phpRootCandidates(ctx context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
	if q.Root == "" {
		return nil, nil
	}
	rel := func(p string) string {
		r, err := filepath.Rel(q.Root, a.absProjectPath(p))
		if err != nil {
			return p
		}
		return filepath.ToSlash(r)
	}
	var out []SettingCandidate
	var ans phpRootAnswer
	if err := a.CallLocal(ctx, "project.phpRoot", nil, &ans); err == nil {
		for _, c := range ans.Candidates {
			v := rel(c.Path)
			if v == "." {
				v = ""
			}
			out = append(out, SettingCandidate{Value: v, Label: orDot(v), Detail: strings.Join(c.Reasons, "; "), Recommended: c.Recommended,
				Current: a.absProjectPath(c.Path) == q.PHPRoot && !ans.Auto})
		}
	} else {
		for i, p := range toolfind.ComposerProjects(q.Root, q.PHPRoot, 0) {
			v := rel(p.Dir)
			if v == "." {
				v = ""
			}
			out = append(out, SettingCandidate{Value: v, Label: orDot(v), Detail: "composer.json", Recommended: i == 0})
		}
	}
	return out, nil
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// --- project setup (D526) ---------------------------------------------

// keySetupDismissed records per project that the setup sheet was
// dismissed: it is not shown automatically again.
const keySetupDismissed = "project.setupDismissed"

var setupSpecsErr = settings.RegisterCore(settings.Spec{Key: keySetupDismissed, Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject,
	Group: settings.PageProjects, Label: "Project setup dismissed",
	Help: "The project setup (PHP root, interpreter, PHPUnit, quality tools) is not proposed on open any more; Project Setup… shows it anyway."})

// SetupItem is a proposed setting value.
type SetupItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Owner is the module ID ("" for core settings).
	Owner string `json:"owner,omitempty"`
	// Current is the effective value, Proposed the recommended candidate.
	Current  json.RawMessage  `json:"current,omitempty"`
	Proposed SettingCandidate `json:"proposed"`
	// Choices are all candidates (for a dropdown).
	Choices []SettingCandidate `json:"choices"`
}

// SetupProposal is the result of "project.setupProposal".
type SetupProposal struct {
	Project string `json:"project"`
	// Dismissed is true when the user dismissed the proposal for this
	// project (the GUI then does not show it on open).
	Dismissed bool        `json:"dismissed"`
	Items     []SetupItem `json:"items"`
	// Notable is true when the proposal contains something the user
	// would hardly find alone: a PHP root below the project root or an
	// interpreter in a container. The GUI offers the sheet on open only
	// then (Project Setup… always shows it).
	Notable bool `json:"notable"`
}

// SetupApplyParams are the params of "project.setupApply".
type SetupApplyParams struct {
	// Items are the values to write (key and value); nothing else is
	// changed.
	Items []SetupValue `json:"items,omitempty"`
	// Dismiss stores that the proposal was dismissed for the project.
	Dismiss bool `json:"dismiss,omitempty"`
}

// SetupValue is a value to apply.
type SetupValue struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// SetupApplyResult is the result of "project.setupApply".
type SetupApplyResult struct {
	Applied []string `json:"applied"`
	Errors  []string `json:"errors,omitempty"`
}

// setupOrder sorts the setup keys: PHP root, interpreter, tests, tools.
func setupOrder(k string) int {
	switch {
	case k == keyPHPRoot:
		return 0
	case k == SettingInterpreter:
		return 1
	case strings.HasPrefix(k, "phpunit."):
		return 2
	case strings.HasPrefix(k, "composer."):
		return 3
	}
	return 4
}

func (a *App) setupProposal(ctx context.Context, _ struct{}) (SetupProposal, error) {
	ps := a.ProjectSettings()
	if ps == nil {
		return SetupProposal{}, Errorf(CodeNoProject, "no project open")
	}
	res := SetupProposal{Project: a.ProjectRoot(), Dismissed: settings.Get(ps, keySetupDismissed, false), Items: []SetupItem{}}
	keys := a.cands.setupKeys()
	sort.Slice(keys, func(i, j int) bool {
		if oi, oj := setupOrder(keys[i]), setupOrder(keys[j]); oi != oj {
			return oi < oj
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		spec, owner, _ := a.lookupSpec(k)
		if _, stored := ps.Raw(k); stored {
			continue
		}
		if _, stored := a.global.Raw(k); stored && k != SettingInterpreter {
			continue
		}
		cr, err := a.candidates(ctx, k)
		if err != nil {
			return res, err
		}
		if len(cr.Candidates) == 0 || !cr.Candidates[0].Recommended || cr.Candidates[0].Current {
			continue
		}
		label := spec.Label
		if label == "" {
			label = k
		}
		res.Items = append(res.Items, SetupItem{Key: k, Label: label, Owner: owner, Current: cr.Current, Proposed: cr.Candidates[0], Choices: cr.Candidates})
		if id, ok := cr.Candidates[0].Value.(string); k == keyPHPRoot || (k == SettingInterpreter && ok && !strings.HasPrefix(id, "local:")) {
			res.Notable = true
		}
	}
	return res, nil
}

func (a *App) setupApply(ctx context.Context, p SetupApplyParams) (SetupApplyResult, error) {
	if a.ProjectSettings() == nil {
		return SetupApplyResult{}, Errorf(CodeNoProject, "no project open")
	}
	res := SetupApplyResult{Applied: []string{}}
	for _, it := range p.Items {
		var err error
		if it.Key == SettingInterpreter {
			var id string
			if err = json.Unmarshal(it.Value, &id); err == nil {
				err = a.CallLocal(ctx, "interp.select", map[string]string{"id": id}, nil)
			}
		} else {
			_, err = a.settingsSet(ctx, SettingsSetParams{Key: it.Key, Scope: "project", Value: it.Value})
		}
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", it.Key, err))
			continue
		}
		res.Applied = append(res.Applied, it.Key)
	}
	if p.Dismiss {
		if err := settings.Set(a.ProjectSettings(), keySetupDismissed, true); err != nil {
			return res, Errorf(CodeFailed, "setup: %w", err)
		}
	}
	return res, nil
}

// InterpreterRefCandidates returns the choices of an interpreter reference
// setting (phpunit.interpreter, composer.interpreter, …): "project" (the
// project's interpreter, recommended), "host" and every interpreter
// candidate of php-runtime.interpreter (through the same filter).
func (a *App) InterpreterRefCandidates(ctx context.Context, q SettingCandidatesQuery) []SettingCandidate {
	out := []SettingCandidate{
		{Value: "project", Label: "Project interpreter", Detail: "the interpreter selected for the project (e.g. a Compose service)", Recommended: true},
		{Value: "host", Label: "Host", Detail: "php on this computer"},
	}
	sub := SettingCandidatesQuery{Key: SettingInterpreter, Root: q.Root, PHPRoot: q.PHPRoot}
	sub, _ = HookSettingCandidates.Apply(ctx, a.hooks, sub)
	for _, c := range sub.Candidates {
		c.Recommended, c.Current = false, false
		out = append(out, c)
	}
	return out
}

// ToolCandidates finds the executables called names (Composer bin dirs,
// PHARs) for a tool path setting, resolved against the PHP root.
func ToolCandidates(q SettingCandidatesQuery, what string, names ...string) []SettingCandidate {
	if q.Root == "" {
		return nil
	}
	return FileCandidates(toolfind.Executables(q.Root, q.PHPRoot, names...), what)
}

// ConfigCandidates finds configuration files called names (in order of
// preference) in the PHP root, the project root and its subfolders.
func ConfigCandidates(q SettingCandidatesQuery, what string, names ...string) []SettingCandidate {
	if q.Root == "" {
		return nil
	}
	return FileCandidates(toolfind.Files(q.Root, q.PHPRoot, names...), what)
}
