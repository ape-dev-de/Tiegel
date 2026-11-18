package qakit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// JSONPayload returns out from the first '{' or '[' that starts a line:
// tools sometimes print warnings (deprecations, PHP notices) before
// their JSON report on stdout.
func JSONPayload(out []byte) []byte {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return trimmed
	}
	for i := 0; i < len(out); i++ {
		if (out[i] == '{' || out[i] == '[') && (i == 0 || out[i-1] == '\n') {
			return bytes.TrimSpace(out[i:])
		}
	}
	return trimmed
}

// checkstyle is the checkstyle XML report (written by many analyzers).
type checkstyle struct {
	XMLName xml.Name `xml:"checkstyle"`
	Files   []struct {
		Name   string `xml:"name,attr"`
		Errors []struct {
			Line     int    `xml:"line,attr"`
			Column   int    `xml:"column,attr"`
			Severity string `xml:"severity,attr"`
			Message  string `xml:"message,attr"`
			Source   string `xml:"source,attr"`
		} `xml:"error"`
	} `xml:"file"`
}

// IsCheckstyle reports whether out looks like a checkstyle report.
func IsCheckstyle(out []byte) bool {
	t := bytes.TrimSpace(out)
	return bytes.HasPrefix(t, []byte("<?xml")) || bytes.HasPrefix(t, []byte("<checkstyle"))
}

// ParseCheckstyle reads a checkstyle XML report.
func ParseCheckstyle(out []byte) ([]Finding, error) {
	t := bytes.TrimSpace(out)
	if i := bytes.Index(t, []byte("<")); i > 0 {
		t = t[i:]
	}
	if len(t) == 0 {
		return nil, errors.New("empty report")
	}
	var c checkstyle
	if err := xml.Unmarshal(t, &c); err != nil {
		return nil, fmt.Errorf("checkstyle report: %w", err)
	}
	var out2 []Finding
	for _, f := range c.Files {
		for _, e := range f.Errors {
			out2 = append(out2, Finding{File: f.Name, Line: e.Line, Col: e.Column, Severity: Severity(e.Severity), Message: e.Message, Code: e.Source})
		}
	}
	return out2, nil
}

// Severity normalizes a tool's severity name.
func Severity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error", "fatal", "critical":
		return SevError
	case "warning", "warn":
		return SevWarning
	case "info", "information", "notice", "suggestion":
		return SevInfo
	case "hint", "ignore":
		return SevHint
	}
	return SevWarning
}
