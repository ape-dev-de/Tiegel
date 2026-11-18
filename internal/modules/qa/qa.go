// Package qa holds the quality tool plugins (AP17.1), one module each:
//
//	qa-pint          Laravel Pint (formatter)
//	qa-php-cs-fixer  PHP-CS-Fixer (formatter)
//	qa-phpcs         PHP_CodeSniffer (phpcs analyzer, phpcbf formatter)
//	qa-phpstan       PHPStan (analyzer)
//	qa-psalm         Psalm (analyzer)
//	qa-phpmd         PHPMD (analyzer)
//
// Every plugin is a qakit.Definition: program, Composer package (install
// recipe, D194), argument builders and the parser of the tool's report.
// Running, settings, format on save and inspections are qakit's; the
// kernel's qa service sees only plugin IDs.
package qa

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/modules/qa/qakit"
)

// Module IDs.
const (
	PintID         = "qa-pint"
	PHPCSFixerID   = "qa-php-cs-fixer"
	PHPCSID        = "qa-phpcs"
	PHPStanID      = "qa-phpstan"
	PsalmID        = "qa-psalm"
	PHPMDID        = "qa-phpmd"
	phpmdRulesets  = "cleancode,codesize,controversial,design,naming,unusedcode"
	defaultSrcPath = "src"
)

// All returns new instances of all quality tool plugins.
func All() []*qakit.Plugin {
	return []*qakit.Plugin{
		qakit.New(Pint()), qakit.New(PHPCSFixer()), qakit.New(PHPCS()),
		qakit.New(PHPStan()), qakit.New(Psalm()), qakit.New(PHPMD()),
	}
}

// configArg returns "--<name>=<config>" or nothing.
func configArg(name, config string) []string {
	if config == "" {
		return nil
	}
	return []string{"--" + name + "=" + config}
}

// Pint is Laravel Pint: formats a temporary copy in place.
func Pint() qakit.Definition {
	return qakit.Definition{
		ID: PintID, Name: "Laravel Pint", Order: 10, Program: "pint", Package: "laravel/pint",
		ConfigHelp: "pint.json to use; empty: Pint's own lookup (pint.json in the project root).",
		Format: &qakit.FormatSpec{Args: func(c qakit.Call) []string {
			return append(append([]string{"--no-interaction"}, configArg("config", c.Config)...), c.File)
		}},
	}
}

// PHPCSFixer is PHP-CS-Fixer: fixes a temporary copy in place (the
// explicit path overrides the configured finder; no cache).
func PHPCSFixer() qakit.Definition {
	return qakit.Definition{
		ID: PHPCSFixerID, Name: "PHP-CS-Fixer", Order: 20, Program: "php-cs-fixer", Package: "friendsofphp/php-cs-fixer",
		ConfigHelp: ".php-cs-fixer.php to use; empty: the tool's own lookup (.php-cs-fixer.php, .php-cs-fixer.dist.php).",
		Format: &qakit.FormatSpec{Args: func(c qakit.Call) []string {
			a := []string{"fix", "--no-interaction", "--using-cache=no", "--show-progress=none", "--quiet"}
			return append(append(a, configArg("config", c.Config)...), c.File)
		}},
	}
}

// phpcsConfigs are the configuration files PHP_CodeSniffer finds itself.
var phpcsConfigs = []string{".phpcs.xml", "phpcs.xml", ".phpcs.xml.dist", "phpcs.xml.dist"}

// PHPCS is PHP_CodeSniffer: phpcs analyzes (JSON report), phpcbf fixes
// through stdin.
func PHPCS() qakit.Definition {
	return qakit.Definition{
		ID: PHPCSID, Name: "PHP_CodeSniffer", Order: 30, Program: "phpcs", FormatProgram: "phpcbf", Package: "squizlabs/php_codesniffer",
		ConfigHelp: "Ruleset (phpcs.xml) or standard name (PSR12); empty: phpcs.xml in the project or the default standard.",
		Format: &qakit.FormatSpec{
			Stdin: true,
			Args: func(c qakit.Call) []string {
				a := append([]string{"-q", "--no-colors"}, configArg("standard", c.Config)...)
				return append(a, "--stdin-path="+c.File, "-")
			},
			// 0: nothing to fix, 1: fixed, 2: some errors left unfixed.
			OK: func(code int) bool { return code >= 0 && code <= 2 },
			Unchanged: func(code int, out []byte) bool {
				t := strings.TrimSpace(string(out))
				return code == 0 && (t == "" || strings.HasPrefix(t, "No fixable errors"))
			},
		},
		Analyze: &qakit.AnalyzeSpec{
			Args: func(c qakit.Call) []string {
				a := append([]string{"--report=json", "-q", "--no-colors"}, configArg("standard", c.Config)...)
				return append(a, c.Paths...)
			},
			// 0: clean, 1–3: issues found (fixable or not).
			OK:           func(code int) bool { return code >= 0 && code <= 3 },
			Parse:        ParsePHPCS,
			DefaultPaths: defaultPaths(phpcsConfigs...),
		},
	}
}

// phpstanConfigs are the configuration files PHPStan finds itself.
var phpstanConfigs = []string{"phpstan.neon", "phpstan.neon.dist", "phpstan.dist.neon"}

// PHPStan analyzes with the JSON error format.
func PHPStan() qakit.Definition {
	return qakit.Definition{
		ID: PHPStanID, Name: "PHPStan", Order: 40, Program: "phpstan", Package: "phpstan/phpstan",
		ConfigHelp: "phpstan.neon to use; empty: phpstan.neon(.dist) in the project.",
		Analyze: &qakit.AnalyzeSpec{
			Args: func(c qakit.Call) []string {
				a := []string{"analyse", "--error-format=json", "--no-progress", "--no-interaction"}
				if c.Config != "" {
					a = append(a, "--configuration="+c.Config)
				}
				return append(a, c.Paths...)
			},
			Parse:        ParsePHPStan,
			DefaultPaths: defaultPaths(phpstanConfigs...),
		},
	}
}

// Psalm analyzes with the JSON output format.
func Psalm() qakit.Definition {
	return qakit.Definition{
		ID: PsalmID, Name: "Psalm", Order: 50, Program: "psalm", Package: "vimeo/psalm",
		ConfigHelp: "psalm.xml to use; empty: psalm.xml(.dist) in the project.",
		Analyze: &qakit.AnalyzeSpec{
			Args: func(c qakit.Call) []string {
				a := append([]string{"--output-format=json", "--no-progress", "--monochrome"}, configArg("config", c.Config)...)
				return append(a, c.Paths...)
			},
			// 0: clean, 2: issues found.
			OK:    func(code int) bool { return code == 0 || code == 2 },
			Parse: ParsePsalm,
		},
	}
}

// phpmdConfigs are ruleset files used when the setting is empty.
var phpmdConfigs = []string{"phpmd.xml", "phpmd.xml.dist", ".phpmd.xml", ".phpmd.xml.dist"}

// PHPMD analyzes with the JSON renderer: phpmd PATHS json RULESETS.
func PHPMD() qakit.Definition {
	d := qakit.Definition{
		ID: PHPMDID, Name: "PHPMD", Order: 60, Program: "phpmd", Package: "phpmd/phpmd",
		ConfigHelp: "Ruleset file or list (e.g. cleancode,codesize); empty: phpmd.xml in the project or the standard rulesets.",
	}
	d.Analyze = &qakit.AnalyzeSpec{
		Args: func(c qakit.Call) []string {
			paths := c.Paths
			if len(paths) == 0 {
				paths = []string{"."}
			}
			rules := c.Config
			if rules == "" {
				rules = phpmdRulesets
			}
			return []string{strings.Join(paths, ","), "json", rules, "--exclude", "vendor/*"}
		},
		// 0: clean, 2: violations, 3: some files could not be processed.
		OK:    func(code int) bool { return code == 0 || code == 2 || code == 3 },
		Parse: ParsePHPMD,
		DefaultPaths: func(root string) []string {
			if isDir(filepath.Join(root, defaultSrcPath)) {
				return []string{defaultSrcPath}
			}
			return []string{"."}
		},
	}
	return d
}

// defaultPaths: without a configuration file of the tool in the project,
// analyze src (if present) or the project.
func defaultPaths(configs ...string) func(root string) []string {
	return func(root string) []string {
		for _, c := range configs {
			if _, err := os.Stat(filepath.Join(root, c)); err == nil {
				return nil
			}
		}
		if isDir(filepath.Join(root, defaultSrcPath)) {
			return []string{defaultSrcPath}
		}
		return []string{"."}
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ParsePHPStan reads PHPStan's JSON error format.
func ParsePHPStan(out []byte) ([]qakit.Finding, []string, error) {
	if qakit.IsCheckstyle(out) {
		f, err := qakit.ParseCheckstyle(out)
		return f, nil, err
	}
	var r struct {
		Files map[string]struct {
			Messages []struct {
				Message    string `json:"message"`
				Line       *int   `json:"line"`
				Identifier string `json:"identifier"`
				Tip        string `json:"tip"`
			} `json:"messages"`
		} `json:"files"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(qakit.JSONPayload(out), &r); err != nil {
		return nil, nil, fmt.Errorf("phpstan json: %w", err)
	}
	var fs []qakit.Finding
	for file, f := range r.Files {
		for _, m := range f.Messages {
			line := 1
			if m.Line != nil && *m.Line > 0 {
				line = *m.Line
			}
			msg := m.Message
			if m.Tip != "" {
				msg += " (" + m.Tip + ")"
			}
			fs = append(fs, qakit.Finding{File: file, Line: line, Severity: qakit.SevError, Message: msg, Code: m.Identifier})
		}
	}
	sortFindings(fs)
	return fs, r.Errors, nil
}

// ParsePsalm reads Psalm's JSON output format.
func ParsePsalm(out []byte) ([]qakit.Finding, []string, error) {
	if qakit.IsCheckstyle(out) {
		f, err := qakit.ParseCheckstyle(out)
		return f, nil, err
	}
	var issues []struct {
		Severity   string `json:"severity"`
		LineFrom   int    `json:"line_from"`
		LineTo     int    `json:"line_to"`
		ColumnFrom int    `json:"column_from"`
		ColumnTo   int    `json:"column_to"`
		Type       string `json:"type"`
		Message    string `json:"message"`
		FileName   string `json:"file_name"`
		FilePath   string `json:"file_path"`
	}
	payload := qakit.JSONPayload(out)
	if len(payload) == 0 {
		return nil, nil, nil
	}
	if err := json.Unmarshal(payload, &issues); err != nil {
		return nil, nil, fmt.Errorf("psalm json: %w", err)
	}
	fs := make([]qakit.Finding, 0, len(issues))
	for _, i := range issues {
		file := i.FilePath
		if file == "" {
			file = i.FileName
		}
		f := qakit.Finding{File: file, Line: i.LineFrom, Col: i.ColumnFrom, Severity: qakit.Severity(i.Severity), Message: i.Message, Code: i.Type}
		if i.LineTo >= i.LineFrom && i.ColumnTo > 0 {
			f.EndLine, f.EndCol = i.LineTo, i.ColumnTo
		}
		fs = append(fs, f)
	}
	return fs, nil, nil
}

// ParsePHPMD reads PHPMD's JSON renderer output.
func ParsePHPMD(out []byte) ([]qakit.Finding, []string, error) {
	if qakit.IsCheckstyle(out) {
		f, err := qakit.ParseCheckstyle(out)
		return f, nil, err
	}
	var r struct {
		Files []struct {
			File       string `json:"file"`
			Violations []struct {
				BeginLine   int    `json:"beginLine"`
				EndLine     int    `json:"endLine"`
				Description string `json:"description"`
				Rule        string `json:"rule"`
				RuleSet     string `json:"ruleSet"`
				Priority    int    `json:"priority"`
			} `json:"violations"`
		} `json:"files"`
		Errors []struct {
			FileName string `json:"fileName"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(qakit.JSONPayload(out), &r); err != nil {
		return nil, nil, fmt.Errorf("phpmd json: %w", err)
	}
	var fs []qakit.Finding
	for _, f := range r.Files {
		for _, v := range f.Violations {
			fs = append(fs, qakit.Finding{File: f.File, Line: v.BeginLine, Severity: phpmdSeverity(v.Priority),
				Message: strings.TrimSpace(v.Description), Code: v.Rule})
		}
	}
	var msgs []string
	for _, e := range r.Errors {
		msgs = append(msgs, strings.TrimSpace(e.FileName+": "+e.Message))
	}
	return fs, msgs, nil
}

// phpmdSeverity maps PHPMD priorities (1 highest … 5 lowest).
func phpmdSeverity(p int) string {
	switch {
	case p == 1:
		return qakit.SevError
	case p <= 3:
		return qakit.SevWarning
	}
	return qakit.SevInfo
}

// ParsePHPCS reads PHP_CodeSniffer's JSON report.
func ParsePHPCS(out []byte) ([]qakit.Finding, []string, error) {
	if qakit.IsCheckstyle(out) {
		f, err := qakit.ParseCheckstyle(out)
		return f, nil, err
	}
	var r struct {
		Files map[string]struct {
			Messages []struct {
				Message  string `json:"message"`
				Source   string `json:"source"`
				Type     string `json:"type"`
				Line     int    `json:"line"`
				Column   int    `json:"column"`
				Fixable  bool   `json:"fixable"`
				Severity int    `json:"severity"`
			} `json:"messages"`
		} `json:"files"`
	}
	if err := json.Unmarshal(qakit.JSONPayload(out), &r); err != nil {
		return nil, nil, fmt.Errorf("phpcs json: %w", err)
	}
	var fs []qakit.Finding
	for file, f := range r.Files {
		for _, m := range f.Messages {
			msg := m.Message
			if m.Fixable {
				msg += " (fixable)"
			}
			fs = append(fs, qakit.Finding{File: file, Line: m.Line, Col: m.Column, Severity: qakit.Severity(m.Type), Message: msg, Code: m.Source})
		}
	}
	sortFindings(fs)
	return fs, nil, nil
}

// sortFindings orders findings of map based reports (file, line, col).
func sortFindings(fs []qakit.Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}
