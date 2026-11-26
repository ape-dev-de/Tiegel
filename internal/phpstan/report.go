// Package phpstan reads PHPStan results without running PHPStan (D556):
// report files of the error formats json, checkstyle and junit, the
// result cache of the last command line run (both cache formats, parsed
// statically – PHP is never executed), the NEON configuration (tmpDir,
// resultCachePath, includes, ignoreErrors incl. baselines) and applies
// ignoreErrors to cached errors. It has no UI and no knowledge of the
// project or containers; callers map paths (see Resolver).
package phpstan

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Raw is one error as written by PHPStan, before paths are resolved.
type Raw struct {
	// Path is the file the error is shown in (the trait file for errors
	// in traits); FilePath the analysed file and TraitPath the trait, as
	// written (absolute, relative to the report's working directory or
	// to PHPStan's %rootDir% in a 2.2+ cache).
	Path      string
	FilePath  string
	TraitPath string
	// Line is 1-based; 0 = the whole file.
	Line       int
	Message    string
	Identifier string
	Tip        string
	// Ignorable mirrors canBeIgnored (cache only; reports are final).
	Ignorable bool
}

// Report formats.
const (
	FormatJSON       = "json"
	FormatCheckstyle = "checkstyle"
	FormatJUnit      = "junit"
	FormatCache      = "resultCache"
)

// Report is a parsed report file.
type Report struct {
	Format string
	Errors []Raw
	// Messages are errors not tied to a file (configuration problems).
	Messages []string
}

// DetectFormat guesses the format of a report from its content ("" if
// unknown): JSON object with "files", <checkstyle>, <testsuite(s)>, or a
// result cache.
func DetectFormat(data []byte) string {
	t := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	switch {
	case bytes.HasPrefix(t, []byte(serializedPrefix)):
		return FormatCache
	case bytes.HasPrefix(t, []byte("<?php")):
		if bytes.Contains(t[:min(len(t), 4096)], []byte("'lastFullAnalysisTime'")) {
			return FormatCache
		}
		return ""
	case bytes.HasPrefix(t, []byte("{")):
		return FormatJSON
	case bytes.HasPrefix(t, []byte("<")):
		head := t[:min(len(t), 2048)]
		switch {
		case bytes.Contains(head, []byte("<checkstyle")):
			return FormatCheckstyle
		case bytes.Contains(head, []byte("<testsuite")):
			return FormatJUnit
		}
	}
	// Tools print text before the payload (e.g. a deprecation notice).
	if i := bytes.Index(t, []byte(`{"totals"`)); i >= 0 {
		return FormatJSON
	}
	return ""
}

// ParseReport parses a report in one of the error formats json,
// checkstyle or junit (detected from the content).
func ParseReport(data []byte) (Report, error) {
	switch f := DetectFormat(data); f {
	case FormatJSON:
		return parseJSON(data)
	case FormatCheckstyle:
		return parseCheckstyle(data)
	case FormatJUnit:
		return parseJUnit(data)
	case FormatCache:
		return Report{}, errors.New("this is a result cache, not a report (use ReadResultCache)")
	}
	return Report{}, errors.New("unknown report format (expected PHPStan --error-format=json, checkstyle or junit)")
}

// reContext matches the suffix PHPStan adds to files of trait errors:
// "Trait.php (in context of class Foo)".
var reContext = regexp.MustCompile(` \(in context of [^)]*\)$`)

// StripContext removes the " (in context of class X)" suffix.
func StripContext(file string) string { return reContext.ReplaceAllString(file, "") }

func parseJSON(data []byte) (Report, error) {
	t := bytes.TrimSpace(data)
	if i := bytes.Index(t, []byte(`{"totals"`)); i > 0 {
		t = t[i:]
	}
	var r struct {
		Files map[string]struct {
			Messages []struct {
				Message    string `json:"message"`
				Line       *int   `json:"line"`
				Identifier string `json:"identifier"`
				Tip        string `json:"tip"`
				Ignorable  *bool  `json:"ignorable"`
			} `json:"messages"`
		} `json:"files"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(t, &r); err != nil {
		return Report{}, fmt.Errorf("phpstan json report: %w", err)
	}
	rep := Report{Format: FormatJSON, Messages: r.Errors}
	for file, f := range r.Files {
		p := StripContext(file)
		for _, m := range f.Messages {
			e := Raw{Path: p, FilePath: p, Message: m.Message, Identifier: m.Identifier, Tip: m.Tip, Ignorable: true}
			if m.Line != nil {
				e.Line = *m.Line
			}
			rep.Errors = append(rep.Errors, e)
		}
	}
	sortRaw(rep.Errors)
	return rep, nil
}

func parseCheckstyle(data []byte) (Report, error) {
	var r struct {
		Files []struct {
			Name   string `xml:"name,attr"`
			Errors []struct {
				Line     string `xml:"line,attr"`
				Message  string `xml:"message,attr"`
				Source   string `xml:"source,attr"`
				Severity string `xml:"severity,attr"`
			} `xml:"error"`
		} `xml:"file"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("phpstan checkstyle report: %w", err)
	}
	rep := Report{Format: FormatCheckstyle}
	for _, f := range r.Files {
		p := StripContext(f.Name)
		for _, e := range f.Errors {
			line, _ := strconv.Atoi(strings.TrimSpace(e.Line))
			rep.Errors = append(rep.Errors, Raw{Path: p, FilePath: p, Line: line, Message: e.Message, Identifier: e.Source, Ignorable: true})
		}
	}
	sortRaw(rep.Errors)
	return rep, nil
}

// reJUnitName splits "path:line" (PHPStan) of a JUnit test case.
var reJUnitName = regexp.MustCompile(`^(.*?)(?::(\d+))?$`)

func parseJUnit(data []byte) (Report, error) {
	type failure struct {
		Type    string `xml:"type,attr"`
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	}
	type testcase struct {
		Name     string    `xml:"name,attr"`
		File     string    `xml:"file,attr"`
		Failures []failure `xml:"failure"`
		Errors   []failure `xml:"error"`
	}
	type suite struct {
		Cases  []testcase `xml:"testcase"`
		Suites []suite    `xml:"testsuite"`
	}
	var root suite
	if err := xml.Unmarshal(data, &root); err != nil {
		return Report{}, fmt.Errorf("phpstan junit report: %w", err)
	}
	rep := Report{Format: FormatJUnit}
	var walk func(s suite)
	walk = func(s suite) {
		for _, c := range s.Cases {
			m := reJUnitName.FindStringSubmatch(c.Name)
			name, line := m[1], 0
			if m[2] != "" {
				line, _ = strconv.Atoi(m[2])
			}
			if c.File != "" {
				name = c.File
			}
			p := StripContext(name)
			for _, f := range append(c.Failures, c.Errors...) {
				msg := f.Message
				if msg == "" {
					msg = strings.TrimSpace(f.Text)
				}
				if p == "" || strings.EqualFold(p, "General error") {
					rep.Messages = append(rep.Messages, msg)
					continue
				}
				rep.Errors = append(rep.Errors, Raw{Path: p, FilePath: p, Line: line, Message: msg, Ignorable: true})
			}
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(root)
	sortRaw(rep.Errors)
	return rep, nil
}

func sortRaw(rs []Raw) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Path != rs[j].Path {
			return rs[i].Path < rs[j].Path
		}
		return rs[i].Line < rs[j].Line
	})
}
