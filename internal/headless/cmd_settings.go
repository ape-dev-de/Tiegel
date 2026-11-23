package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["schema"] = false
	flagSpec["module"] = true
	flagSpec["query"] = true
	flagSpec["scope"] = true
	flagSpec["effective"] = false
	register(
		&command{name: "settings", usage: "settings [--schema] [--module ID|core] [--query TEXT] | settings get KEY [--scope global|project] [--effective] | settings set KEY VALUE [--scope S] | settings reset KEY [--scope S] | settings candidates KEY",
			summary: "declared settings of the core and the plugins (schema with values), read, write (validated) or reset one", maxArgs: 3, cwdProject: true, run: runSettings},
		&command{name: "editorconfig", usage: "editorconfig FILE", summary: "effective .editorconfig properties of a file", minArgs: 1, maxArgs: 1, cwdProject: true, run: runEditorConfig},
	)
}

func runSettings(c *cli) error {
	if len(c.p.args) == 0 {
		return runSettingsSchema(c)
	}
	usage := usagef("usage: tiegel --headless %s", commands["settings"].usage)
	verb, args := c.p.args[0], c.p.args[1:]
	scope := c.p.value("scope")
	switch {
	case verb == "get" && len(args) == 1:
		var v api.SettingValue
		if err := c.call("settings.get", api.SettingsParams{Key: args[0], Scope: scope, Effective: c.p.has("effective")}, &v); err != nil {
			return err
		}
		return c.emit(v, func(w io.Writer) {
			if !v.Found {
				fmt.Fprintf(w, "%s: not set (%s)\n", v.Key, v.Scope)
				return
			}
			fmt.Fprintf(w, "%s = %s (%s)\n", v.Key, v.Value, v.Scope)
		})
	case verb == "set" && len(args) == 2:
		raw := json.RawMessage(args[1])
		if !json.Valid(raw) {
			// Plain words are taken as strings: settings set ui.theme light
			raw, _ = json.Marshal(args[1])
		}
		var v api.SettingValue
		if err := c.call("settings.set", api.SettingsSetParams{Key: args[0], Scope: scope, Value: raw}, &v); err != nil {
			return err
		}
		return c.emit(v, func(w io.Writer) { fmt.Fprintf(w, "%s = %s (%s)\n", v.Key, v.Value, v.Scope) })
	case verb == "candidates" && len(args) == 1:
		return runSettingsCandidates(c, args[0])
	case verb == "reset" && len(args) == 1:
		var v api.SettingValue
		if err := c.call("settings.set", api.SettingsSetParams{Key: args[0], Scope: scope}, &v); err != nil {
			return err
		}
		return c.emit(v, func(w io.Writer) { fmt.Fprintf(w, "%s reset (%s)\n", v.Key, v.Scope) })
	}
	return usage
}

func runSettingsSchema(c *cli) error {
	var s api.SettingsSchema
	if err := c.call("settings.schema", api.SchemaParams{Module: c.p.value("module"), Query: c.p.value("query")}, &s); err != nil {
		return err
	}
	return c.emit(s, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		row := func(si api.SettingInfo) {
			val := string(si.Value)
			if len(val) > 40 {
				val = val[:37] + "..."
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", si.Key, si.Kind, val, si.Source, si.Label)
		}
		byPage := map[string][]api.SettingInfo{}
		for _, si := range s.Core {
			byPage[si.Group] = append(byPage[si.Group], si)
		}
		for _, pg := range append(s.Pages, "") {
			if len(byPage[pg]) == 0 {
				continue
			}
			fmt.Fprintf(tw, "Core › %s\n", orDash(pg))
			for _, si := range byPage[pg] {
				row(si)
			}
		}
		for _, m := range s.Modules {
			state := "off"
			if m.Enabled {
				state = "on"
			}
			fmt.Fprintf(tw, "Plugins › %s [%s]\n", m.ID, state)
			for _, si := range m.Settings {
				row(si)
			}
		}
		_ = tw.Flush()
		if len(s.Problems) > 0 {
			fmt.Fprintf(w, "problems:\n  %s\n", strings.Join(s.Problems, "\n  "))
		}
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runEditorConfig(c *cli) error {
	var res api.EditorConfigResult
	if err := c.call("files.editorconfig", api.PathParams{Path: c.p.args[0]}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if !res.Enabled {
			fmt.Fprintf(w, "%s: .editorconfig support is off (files.editorconfig)\n", res.Path)
			return
		}
		if res.Empty() {
			fmt.Fprintf(w, "%s: no .editorconfig properties\n", res.Path)
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		put := func(k string, v any) { fmt.Fprintf(tw, "%s\t%v\n", k, v) }
		if res.IndentStyle != "" {
			put("indent_style", res.IndentStyle)
		}
		if res.IndentSize > 0 {
			put("indent_size", res.IndentSize)
		}
		if res.TabWidth > 0 {
			put("tab_width", res.TabWidth)
		}
		if res.EndOfLine != "" {
			put("end_of_line", res.EndOfLine)
		}
		if res.Charset != "" {
			put("charset", res.Charset)
		}
		if res.TrimTrailingWhitespace != nil {
			put("trim_trailing_whitespace", *res.TrimTrailingWhitespace)
		}
		if res.InsertFinalNewline != nil {
			put("insert_final_newline", *res.InsertFinalNewline)
		}
		_ = tw.Flush()
		for _, f := range res.Files {
			fmt.Fprintf(w, "from %s\n", f)
		}
		if res.CharsetWarning != "" {
			fmt.Fprintf(w, "warning: %s\n", res.CharsetWarning)
		}
	})
}
