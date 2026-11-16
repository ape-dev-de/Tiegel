package phpver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source tells where a detected version came from.
type Source int

// Version sources, in the order Detect consults them (Override first).
const (
	SourceUnknown Source = iota
	SourceOverride
	SourceInterpreter
	SourcePlatform
	SourceRequire
)

// String returns a short English label for the status bar.
func (s Source) String() string {
	switch s {
	case SourceOverride:
		return "override"
	case SourceInterpreter:
		return "interpreter"
	case SourcePlatform:
		return "composer config.platform.php"
	case SourceRequire:
		return "composer require.php"
	}
	return "unknown"
}

// Result is a detected PHP version and its source.
type Result struct {
	Version Version
	Source  Source
	// Detail is human-readable context, e.g. the constraint "^8.1" or the
	// raw interpreter output.
	Detail string
}

// Runner executes the PHP interpreter with the given arguments and returns
// its standard output. Implementations decide where PHP runs (locally, in a
// Docker Compose service, in WSL).
type Runner interface {
	RunPHP(ctx context.Context, args ...string) ([]byte, error)
}

// LocalRunner runs a PHP binary on the host.
type LocalRunner struct {
	// Binary is the path or name of the PHP executable; empty means "php"
	// from PATH.
	Binary string
}

// RunPHP implements Runner.
func (r LocalRunner) RunPHP(ctx context.Context, args ...string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "php"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("phpver: run %s: %w: %s", bin, err, msg)
		}
		return nil, fmt.Errorf("phpver: run %s: %w", bin, err)
	}
	return out, nil
}

// InterpreterVersion asks the interpreter behind r for PHP_VERSION.
func InterpreterVersion(ctx context.Context, r Runner) (Version, string, error) {
	out, err := r.RunPHP(ctx, "-r", "echo PHP_VERSION;")
	if err != nil {
		return Version{}, "", err
	}
	raw := strings.TrimSpace(string(out))
	v, err := ParseVersion(raw)
	if err != nil {
		return Version{}, raw, fmt.Errorf("phpver: interpreter output: %w", err)
	}
	return v, raw, nil
}

type composerFile struct {
	Require map[string]string `json:"require"`
	Config  struct {
		Platform map[string]any `json:"platform"`
	} `json:"config"`
}

// PlatformVersion returns config.platform.php from composer.json content.
// ok is false if the key is absent or set to false (Composer's way to
// disable a platform override).
func PlatformVersion(composerJSON []byte) (v Version, raw string, ok bool, err error) {
	var cf composerFile
	if err := json.Unmarshal(composerJSON, &cf); err != nil {
		return Version{}, "", false, fmt.Errorf("phpver: parse composer.json: %w", err)
	}
	s, isString := cf.Config.Platform["php"].(string)
	if !isString || strings.TrimSpace(s) == "" {
		return Version{}, "", false, nil
	}
	v, err = ParseVersion(s)
	if err != nil {
		return Version{}, s, false, err
	}
	return v, s, true, nil
}

// RequireVersion returns the lower bound of require.php from composer.json
// content. ok is false if there is no php requirement.
func RequireVersion(composerJSON []byte) (v Version, constraint string, ok bool, err error) {
	var cf composerFile
	if err := json.Unmarshal(composerJSON, &cf); err != nil {
		return Version{}, "", false, fmt.Errorf("phpver: parse composer.json: %w", err)
	}
	c, has := cf.Require["php"]
	if !has || strings.TrimSpace(c) == "" {
		return Version{}, "", false, nil
	}
	v, err = MinVersion(c)
	if err != nil {
		return Version{}, c, false, err
	}
	return v, c, true, nil
}

// Options configures Detect.
type Options struct {
	// Override, if non-nil, wins over everything else (user choice in the
	// status bar).
	Override *Version
	// Runner runs the project's interpreter; nil skips this step.
	Runner Runner
	// ProjectDir is searched for composer.json; empty skips the Composer
	// steps unless ComposerJSON is set.
	ProjectDir string
	// ComposerJSON is used instead of reading ProjectDir/composer.json.
	ComposerJSON []byte
}

// ErrUnknown is returned by Detect if no source yields a version.
var ErrUnknown = errors.New("phpver: PHP version could not be determined")

// Detect determines the project's PHP version in this order: Override,
// interpreter (Runner), composer.json config.platform.php, composer.json
// require.php lower bound. Failing sources are skipped; if all fail, the
// returned error wraps ErrUnknown and the individual causes.
func Detect(ctx context.Context, opts Options) (Result, error) {
	if opts.Override != nil {
		return Result{Version: *opts.Override, Source: SourceOverride}, nil
	}
	var errs []error
	if opts.Runner != nil {
		v, raw, err := InterpreterVersion(ctx, opts.Runner)
		if err == nil {
			return Result{Version: v, Source: SourceInterpreter, Detail: raw}, nil
		}
		errs = append(errs, err)
	}
	data := opts.ComposerJSON
	if data == nil && opts.ProjectDir != "" {
		b, err := os.ReadFile(filepath.Join(opts.ProjectDir, "composer.json"))
		switch {
		case err == nil:
			data = b
		case !errors.Is(err, os.ErrNotExist):
			errs = append(errs, fmt.Errorf("phpver: read composer.json: %w", err))
		}
	}
	if data != nil && !json.Valid(data) {
		errs = append(errs, errors.New("phpver: composer.json is not valid JSON"))
		data = nil
	}
	if data != nil {
		if v, raw, ok, err := PlatformVersion(data); ok {
			return Result{Version: v, Source: SourcePlatform, Detail: raw}, nil
		} else if err != nil {
			errs = append(errs, err)
		}
		if v, c, ok, err := RequireVersion(data); ok {
			return Result{Version: v, Source: SourceRequire, Detail: c}, nil
		} else if err != nil {
			errs = append(errs, err)
		}
	}
	return Result{}, errors.Join(append([]error{ErrUnknown}, errs...)...)
}
