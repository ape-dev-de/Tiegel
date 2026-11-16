package headless

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	for name, takes := range map[string]bool{"mask": true, "dir": true, "open": false, "preview": false, "max-size": true, "id": true} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "find", usage: "find PATTERN [--regex] [--case] [--word] [--mask GLOBS] [--dir DIR | --open] [--libraries] [--ignored] [--limit N] [--max-size BYTES]", summary: "find in files of the project (exit 1 without matches)", minArgs: 1, maxArgs: 1, cwdProject: true, run: runFind},
		&command{name: "replace", usage: "replace PATTERN REPLACEMENT [--preview] [find flags] [FILE...]", summary: "replace in files (open documents undoable, files with Local History)", minArgs: 2, maxArgs: -1, cwdProject: true, run: runReplace},
	)
}

func projectSearchParams(c *cli, pattern string) (api.ProjectSearchParams, error) {
	p := api.ProjectSearchParams{
		Pattern: pattern, Regex: c.p.has("regex"), CaseSensitive: c.p.has("case"), WholeWord: c.p.has("word"),
		Mask: c.p.value("mask"), Dir: c.p.value("dir"), Libraries: c.p.has("libraries"), Ignored: c.p.has("ignored"),
		ID: c.p.value("id"),
	}
	if c.p.has("open") {
		p.Scope = api.ScopeOpenFiles
	}
	var err error
	if p.Limit, err = c.p.intValue("limit"); err != nil {
		return p, err
	}
	if s := c.p.value("max-size"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return p, usagef("--max-size needs a positive number of bytes, got %q", s)
		}
		p.MaxFileSize = n
	}
	return p, nil
}

func runFind(c *cli) error {
	params, err := projectSearchParams(c, c.p.args[0])
	if err != nil {
		return err
	}
	var res api.ProjectSearchResult
	if err := c.call("search.inProject", params, &res); err != nil {
		return err
	}
	if err := c.emit(res, func(w io.Writer) {
		for _, f := range res.Files {
			for _, m := range f.Matches {
				fmt.Fprintf(w, "%s:%d:%d: %s\n", f.Path, m.Line, m.Col, m.LineText)
			}
		}
		if res.Limited {
			fmt.Fprintf(w, "(limited to %d matches)\n", res.Matches)
		}
	}); err != nil {
		return err
	}
	if res.Matches == 0 {
		return &quietError{code: ExitError}
	}
	return nil
}

func runReplace(c *cli) error {
	sp, err := projectSearchParams(c, c.p.args[0])
	if err != nil {
		return err
	}
	sp.Paths = c.p.args[2:]
	params := api.ProjectReplaceParams{ProjectSearchParams: sp, Replacement: c.p.args[1], Preview: c.p.has("preview")}
	var res api.ProjectReplaceResult
	if err := c.call("search.replaceInProject", params, &res); err != nil {
		return err
	}
	if err := c.emit(res, func(w io.Writer) {
		for _, f := range res.Files {
			if res.Preview {
				for _, m := range f.Matches {
					fmt.Fprintf(w, "%s:%d:%d: %s -> %s\n", f.Path, m.Line, m.Col, m.Text, m.Replacement)
				}
				continue
			}
			switch {
			case f.Error != "":
				fmt.Fprintf(w, "%s: error: %s\n", f.Path, f.Error)
			case f.Open:
				fmt.Fprintf(w, "%s: %d replaced (open document, not saved)\n", f.Path, f.Replaced)
			default:
				fmt.Fprintf(w, "%s: %d replaced\n", f.Path, f.Replaced)
			}
		}
		verb := "replaced"
		if res.Preview {
			verb = "would replace"
		}
		fmt.Fprintf(w, "%s %d in %d files\n", verb, res.Replaced, res.FilesChanged)
	}); err != nil {
		return err
	}
	if res.Failed > 0 {
		return fmt.Errorf("%d files could not be changed", res.Failed)
	}
	if res.Replaced == 0 {
		return &quietError{code: ExitError}
	}
	return nil
}
