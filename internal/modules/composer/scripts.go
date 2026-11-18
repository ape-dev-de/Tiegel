package composer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Script is one entry of the "scripts" section of composer.json.
type Script struct {
	Name string `json:"name"`
	// Commands are the script's commands (a string value is one command).
	Commands []string `json:"commands"`
	// Description comes from "scripts-descriptions".
	Description string `json:"description,omitempty"`
	// Line is the 1-based line of the script's key in composer.json (for
	// the gutter run arrow; 0 if unknown).
	Line int `json:"line,omitempty"`
}

// ParseScripts reads the scripts of a composer.json document, sorted by
// name. A document without scripts yields none; invalid JSON is an error.
func ParseScripts(data []byte) ([]Script, error) {
	var cj struct {
		Scripts      map[string]json.RawMessage `json:"scripts"`
		Descriptions map[string]string          `json:"scripts-descriptions"`
	}
	if err := json.Unmarshal(data, &cj); err != nil {
		return nil, fmt.Errorf("composer.json: %w", err)
	}
	lines := scriptLines(data)
	out := make([]Script, 0, len(cj.Scripts))
	for name, raw := range cj.Scripts {
		s := Script{Name: name, Description: cj.Descriptions[name], Line: lines[name]}
		var one string
		var many []string
		switch {
		case json.Unmarshal(raw, &one) == nil:
			s.Commands = []string{one}
		case json.Unmarshal(raw, &many) == nil:
			s.Commands = many
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// scriptLines returns the line of every key of the top-level "scripts"
// object (by walking the tokens; offsets give the lines).
func scriptLines(data []byte) map[string]int {
	out := map[string]int{}
	dec := json.NewDecoder(bytes.NewReader(data))
	lineAt := func(off int64) int { return bytes.Count(data[:min(int(off), len(data))], []byte("\n")) + 1 }
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return out
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return out
		}
		if k, _ := key.(string); k != "scripts" {
			if skipValue(dec) != nil {
				return out
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return out
		}
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				return out
			}
			// The offset is just after the key's closing quote: same line.
			if n, ok := name.(string); ok {
				out[n] = lineAt(dec.InputOffset())
			}
			if skipValue(dec) != nil {
				return out
			}
		}
		return out
	}
	return out
}

// skipValue consumes one JSON value.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}
