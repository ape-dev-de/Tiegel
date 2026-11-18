package controller

import (
	"sort"
	"strings"
)

// Severity of a diagnostic, most severe first (same order as
// editorview.Severity).
type Severity uint8

// Severities.
const (
	SevError Severity = iota
	SevWarning
	SevInfo
	SevHint
)

func parseSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "error":
		return SevError
	case "warning":
		return SevWarning
	case "information", "info":
		return SevInfo
	}
	return SevHint
}

// String returns the severity name.
func (s Severity) String() string {
	return [...]string{"error", "warning", "info", "hint"}[s]
}

// Diagnostic is a problem in a file from the language server or the PHP
// feature check.
type Diagnostic struct {
	File       string
	Path       string
	Start, End int // byte offsets
	Line, Col  int // 1-based
	Severity   Severity
	Message    string
	Source     string
}

type diagSource uint8

const (
	sourceLSP diagSource = iota
	sourcePHP
)

type fileDiags struct {
	lsp, php []Diagnostic
	// qa are the findings of the quality tool plugins by plugin ID.
	qa map[string][]Diagnostic
}

func (c *Controller) setDiagnostics(ev diagEvent, src diagSource) {
	file := ev.File
	if file == "" {
		file = c.Abs(ev.Path)
	}
	fd := c.diags[file]
	if fd == nil {
		fd = &fileDiags{}
		c.diags[file] = fd
	}
	out := make([]Diagnostic, 0, len(ev.Diagnostics))
	for _, d := range ev.Diagnostics {
		source := d.Source
		if src == sourcePHP {
			source = "php " + ev.PHPVersion
			if d.Feature != "" {
				source += " · " + d.Feature
			}
		}
		out = append(out, Diagnostic{
			File: file, Path: c.Rel(file), Start: d.Start, End: d.End, Line: d.Line, Col: d.Col,
			Severity: parseSeverity(d.Severity), Message: d.Message, Source: source,
		})
	}
	if src == sourceLSP {
		fd.lsp = out
	} else {
		fd.php = out
	}
	c.DiagVersion++
}

// Diagnostics returns the diagnostics of file (language server, PHP
// feature check and quality tool plugins), ordered by position.
func (c *Controller) Diagnostics(file string) []Diagnostic {
	fd := c.diags[file]
	if fd == nil {
		return nil
	}
	out := append(append(append([]Diagnostic(nil), fd.lsp...), fd.php...), fd.qaAll()...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// Problems returns all diagnostics, grouped by file (sorted by path) and
// ordered by severity, then position.
func (c *Controller) Problems() []Diagnostic {
	files := make([]string, 0, len(c.diags))
	for f := range c.diags {
		files = append(files, f)
	}
	sort.Strings(files)
	var out []Diagnostic
	for _, f := range files {
		ds := c.Diagnostics(f)
		sort.SliceStable(ds, func(i, j int) bool {
			if ds[i].Severity != ds[j].Severity {
				return ds[i].Severity < ds[j].Severity
			}
			return ds[i].Start < ds[j].Start
		})
		out = append(out, ds...)
	}
	return out
}

// ProblemCounts returns the number of errors and warnings.
func (c *Controller) ProblemCounts() (errs, warns int) {
	for _, fd := range c.diags {
		for _, l := range [][]Diagnostic{fd.lsp, fd.php, fd.qaAll()} {
			for _, d := range l {
				switch d.Severity {
				case SevError:
					errs++
				case SevWarning:
					warns++
				}
			}
		}
	}
	return errs, warns
}
