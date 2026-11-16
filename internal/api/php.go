package api

import (
	"context"
	"errors"
	"os"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/phpver"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// PHPVersionParams are the params of "php.version".
type PHPVersionParams struct {
	// Override wins over every other source (e.g. "8.2").
	Override string `json:"override,omitempty"`
	// Interpreter is the interpreter ID to ask; empty means the selected
	// one. NoInterpreter skips asking an interpreter at all.
	Interpreter   string `json:"interpreter,omitempty"`
	NoInterpreter bool   `json:"noInterpreter,omitempty"`
}

// PHPVersionResult is the result of "php.version".
type PHPVersionResult struct {
	Version string `json:"version"`
	Short   string `json:"short"`
	// Source is where the version came from: override, interpreter,
	// platform (composer config.platform.php) or require (require.php).
	Source string `json:"source"`
	Detail string `json:"detail,omitempty"`
}

// PHPCheckParams are the params of "php.check". The open document is checked
// if there is one, otherwise the file on disk.
type PHPCheckParams struct {
	Path string `json:"path"`
	// Version to check against; empty means "php.version" detection.
	Version string `json:"version,omitempty"`
}

// PHPDiagnostic is a feature or deprecation finding. Line and Col are
// 1-based (Col in characters).
type PHPDiagnostic struct {
	Feature   string `json:"feature"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	Highlight string `json:"text"`
}

// PHPCheckResult is the result of "php.check".
type PHPCheckResult struct {
	Path        string          `json:"path"`
	Version     string          `json:"version"`
	Source      string          `json:"source"`
	Diagnostics []PHPDiagnostic `json:"diagnostics"`
}

func (a *App) registerPHP() {
	mustHandle(a, "php.version", "Project PHP version with its source (override, interpreter, composer)", a.phpVersion)
	mustHandle(a, "php.check", "Check a PHP file against the project's PHP version (features, deprecations)", a.phpCheck)
}

// interpRunner adapts an interp.Interpreter to phpver.Runner.
type interpRunner struct{ i interp.Interpreter }

func (r interpRunner) RunPHP(ctx context.Context, args ...string) ([]byte, error) {
	return interp.Run(ctx, r.i, args...)
}

func (a *App) phpVersion(ctx context.Context, p PHPVersionParams) (PHPVersionResult, error) {
	opts := phpver.Options{ProjectDir: a.ProjectRoot()}
	if p.Override != "" {
		v, err := phpver.ParseVersion(p.Override)
		if err != nil {
			return PHPVersionResult{}, Errorf(CodeInvalidParams, "override: %w", err)
		}
		opts.Override = &v
	}
	if !p.NoInterpreter {
		id := p.Interpreter
		if id == "" {
			id = settings.Get(a.settingsStore(), settingInterpreter, "")
		}
		if id != "" {
			i, err := a.interpreter(ctx, id)
			if err != nil {
				return PHPVersionResult{}, err
			}
			opts.Runner = interpRunner{i}
		}
	}
	res, err := phpver.Detect(ctx, opts)
	if err != nil {
		if errors.Is(err, phpver.ErrUnknown) {
			return PHPVersionResult{}, Errorf(CodeNotFound, "%w", err)
		}
		return PHPVersionResult{}, Errorf(CodeFailed, "php version: %w", err)
	}
	return PHPVersionResult{Version: res.Version.String(), Short: res.Version.Short(), Source: res.Source.String(), Detail: res.Detail}, nil
}

func (a *App) phpCheck(ctx context.Context, p PHPCheckParams) (PHPCheckResult, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return PHPCheckResult{}, err
	}
	var src []byte
	if d, derr := a.doc(abs); derr == nil {
		d.mu.Lock()
		src = d.buf.Bytes()
		d.mu.Unlock()
	} else if src, err = os.ReadFile(abs); err != nil {
		return PHPCheckResult{}, notFoundOr(err, "php.check")
	}
	res := PHPCheckResult{Path: a.displayPath(abs), Diagnostics: []PHPDiagnostic{}}
	var v phpver.Version
	if p.Version != "" {
		if v, err = phpver.ParseVersion(p.Version); err != nil {
			return res, Errorf(CodeInvalidParams, "version: %w", err)
		}
		res.Source = "param"
	} else {
		pv, err := a.phpVersion(ctx, PHPVersionParams{})
		if err != nil {
			return res, err
		}
		v, _ = phpver.ParseVersion(pv.Version)
		res.Source = pv.Source
	}
	res.Version = v.String()
	doc, err := syntax.NewDocument(ctx, syntax.LangPHP, src)
	if err != nil {
		return res, Errorf(CodeFailed, "parse: %w", err)
	}
	defer doc.Close()
	diags, err := phpver.Check(doc, v)
	if err != nil {
		return res, Errorf(CodeFailed, "check: %w", err)
	}
	buf := buffer.New(src)
	for _, d := range diags {
		r := d.Range
		res.Diagnostics = append(res.Diagnostics, PHPDiagnostic{
			Feature: d.FeatureID, Severity: d.Severity.String(), Message: d.Message,
			Start: r.StartByte, End: r.EndByte,
			Line: r.Start.Line + 1, Col: buffer.RuneCol(buf.Line(r.Start.Line), r.Start.Column) + 1,
			EndLine: r.End.Line + 1, EndCol: buffer.RuneCol(buf.Line(r.End.Line), r.End.Column) + 1,
			Highlight: string(src[r.StartByte:r.EndByte]),
		})
	}
	return res, nil
}
