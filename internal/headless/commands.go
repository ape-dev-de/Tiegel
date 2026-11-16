package headless

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/history"
	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
	"github.com/ape-dev-de/tiegel/internal/modules/phpfeatures"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

func init() {
	register(
		&command{name: "info", usage: "info [--project DIR]", summary: "application and project information", maxArgs: 0, cwdProject: true, run: runInfo},
		&command{name: "methods", usage: "methods", summary: "list all API methods (for call/serve)", maxArgs: 0, run: runMethods},
		&command{name: "files", usage: "files [SUBDIR] [--tree] [--depth N] [--ignored] [--libraries]", summary: "list project files (.gitignore respected)", maxArgs: 1, cwdProject: true, run: runFiles},
		&command{name: "read", usage: "read FILE", summary: "print a file", minArgs: 1, maxArgs: 1, run: runRead},
		&command{name: "lang", usage: "lang FILE...", summary: "detect the language of files", minArgs: 1, maxArgs: -1, run: runLang},
		&command{name: "search", usage: "search FILE PATTERN [--regex] [--case] [--word] [--limit N]", summary: "find matches in a file", minArgs: 2, maxArgs: 2, run: runSearch},
		&command{name: "interpreters", usage: "interpreters [select ID | version [ID] [--modules] [--ini]]", summary: "list, select or query PHP interpreters", maxArgs: 2, cwdProject: true, run: runInterpreters},
		&command{name: "compose", usage: "compose [DIR] [--profile P]", summary: "detect PHP services in Docker Compose files", maxArgs: 1, cwdProject: true, run: runCompose},
		&command{name: "lsp-servers", usage: "lsp-servers [--all]", summary: "installed PHP language servers", maxArgs: 0, cwdProject: true, run: runLSPServers},
		&command{name: "modules", usage: "modules [enable ID | disable ID]", summary: "list, enable or disable modules", maxArgs: 2, run: runModules},
		&command{name: "history", usage: "history FILE [--show ID | --restore ID]", summary: "Local History of a file", minArgs: 1, maxArgs: 1, cwdProject: true, run: runHistory},
		&command{name: "serve", usage: "serve [--framing ndjson|lsp] [--concurrent] [--listen unix:PATH|tcp:HOST:PORT [--token-file F] [--token T] [--allow-remote]]", summary: "JSON-RPC 2.0 over stdin/stdout or a socket", maxArgs: 0, watch: true, run: runServe},
		&command{name: "call", usage: "call METHOD [JSON-PARAMS | -]", summary: "call any API method (\"-\" reads params from stdin)", minArgs: 1, maxArgs: 2, run: runCall},
		&command{name: "version", usage: "version", summary: "print the version", maxArgs: 0, run: runVersion},
		&command{name: "update-check", usage: "update-check [--prereleases]", summary: "check for a newer release (network access, only when run)", maxArgs: 0, run: runUpdateCheck},
		&command{name: "git-status", usage: "git-status", summary: "branch and changed files of the project", maxArgs: 0, cwdProject: true, run: runGitStatus},
		&command{name: "php-version", usage: "php-version [--start]", summary: "project PHP version and where it comes from (--start may start a stopped compose container)", maxArgs: 0, cwdProject: true, run: runPHPVersion},
		&command{name: "php-check", usage: "php-check FILE... [--php VERSION]", summary: "check PHP files against the project's PHP version (exit 1 on findings)", minArgs: 1, maxArgs: -1, cwdProject: true, run: runPHPCheck},
	)
}

func runVersion(c *cli) error {
	var vi api.VersionInfo
	if err := c.call("app.version", nil, &vi); err != nil {
		return err
	}
	return c.emit(vi, func(w io.Writer) { fmt.Fprintf(w, "%s (api %s)\n", vi.String, vi.APIVersion) })
}

func runUpdateCheck(c *cli) error {
	var res api.UpdateCheckResult
	err := c.call("update.check", api.UpdateCheckParams{Prereleases: c.p.has("prereleases")}, &res)
	if err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if res.Available {
			fmt.Fprintf(w, "update available: %s (running %s)\n%s\n", res.Latest, res.Current, res.URL)
			return
		}
		fmt.Fprintf(w, "up to date (running %s, latest %s)\n", res.Current, res.Latest)
	})
}

func runGitStatus(c *cli) error {
	var st gitmod.GitStatus
	if err := c.call("git.status", nil, &st); err != nil {
		return err
	}
	return c.emit(st, func(w io.Writer) {
		branch := st.Branch
		if st.Detached {
			branch = "(detached " + st.Head + ")"
		}
		fmt.Fprintf(w, "## %s", branch)
		if st.Upstream != "" {
			fmt.Fprintf(w, "...%s [ahead %d, behind %d]", st.Upstream, st.Ahead, st.Behind)
		}
		fmt.Fprintln(w)
		for _, f := range st.Files {
			fmt.Fprintf(w, "%s%s %s\n", f.Index, f.Worktree, f.Path)
		}
	})
}

func runPHPVersion(c *cli) error {
	var pv phpruntime.PHPVersionResult
	if err := c.call("php.version", phpruntime.PHPVersionParams{Start: c.p.has("start")}, &pv); err != nil {
		return err
	}
	return c.emit(pv, func(w io.Writer) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", pv.Version, pv.Source, pv.Detail)
		if pv.Hint != nil {
			fmt.Fprintf(w, "hint: %s (php-version --start may start the container)\n", pv.Hint.Message)
		}
	})
}

func runPHPCheck(c *cli) error {
	failed := false
	var all []phpfeatures.CheckResult
	for _, f := range c.p.args {
		var res phpfeatures.CheckResult
		if err := c.call("php.check", phpfeatures.CheckParams{Path: f, Version: c.p.value("php")}, &res); err != nil {
			return err
		}
		failed = failed || len(res.Diagnostics) > 0
		all = append(all, res)
	}
	if err := c.emit(all, func(w io.Writer) {
		for i, res := range all {
			for _, d := range res.Diagnostics {
				fmt.Fprintf(w, "%s:%d:%d: %s: %s [%s] (PHP %s)\n", c.p.args[i], d.Line, d.Col, d.Severity, d.Message, d.Feature, res.Version)
			}
		}
	}); err != nil {
		return err
	}
	if failed {
		return &quietError{code: ExitError}
	}
	return nil
}

func runInfo(c *cli) error {
	var out struct {
		App     api.AppInfo     `json:"app"`
		Project api.ProjectInfo `json:"project"`
	}
	if err := c.call("app.info", nil, &out.App); err != nil {
		return err
	}
	if err := c.call("project.info", nil, &out.Project); err != nil {
		return err
	}
	return c.emit(out, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		v := out.App.Version
		if v == "" {
			v = "dev"
		}
		fmt.Fprintf(tw, "Version:\t%s (api %s, %s/%s)\n", v, out.App.APIVersion, out.App.GOOS, out.App.GOARCH)
		fmt.Fprintf(tw, "Config:\t%s\n", out.App.ConfigDir)
		if !out.Project.Open {
			fmt.Fprintf(tw, "Project:\t(none)\n")
		} else {
			p := out.Project
			fmt.Fprintf(tw, "Project:\t%s (%s)\n", p.Name, p.Root)
			fmt.Fprintf(tw, "Git:\t%s\n", yesNo(p.Git))
			fmt.Fprintf(tw, "Composer:\t%s\n", yesNo(p.Composer))
			if len(p.ComposeFiles) > 0 {
				fmt.Fprintf(tw, "Compose:\t%s\n", strings.Join(p.ComposeFiles, ", "))
			}
			if p.Interpreter != "" {
				fmt.Fprintf(tw, "Interpreter:\t%s\n", p.Interpreter)
			}
		}
		_ = tw.Flush()
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func runMethods(c *cli) error {
	var ms []api.MethodInfo
	if err := c.call("app.methods", nil, &ms); err != nil {
		return err
	}
	return c.emit(ms, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, m := range ms {
			fmt.Fprintf(tw, "%s\t%s\n", m.Name, m.Summary)
		}
		_ = tw.Flush()
	})
}

func runFiles(c *cli) error {
	params := api.FilesListParams{Ignored: c.p.has("ignored"), Libraries: c.p.has("libraries")}
	if len(c.p.args) > 0 {
		params.Path = c.p.args[0]
	}
	var err error
	if params.Depth, err = c.p.intValue("depth"); err != nil {
		return err
	}
	var res api.FilesListResult
	if err := c.call("files.list", params, &res); err != nil {
		return err
	}
	tree := c.p.has("tree")
	return c.emit(res, func(w io.Writer) {
		var walk func(n *api.FileNode, depth int)
		walk = func(n *api.FileNode, depth int) {
			for _, ch := range n.Children {
				name := ch.Path
				if tree {
					name = strings.Repeat("  ", depth) + ch.Name
				}
				if ch.Dir {
					name += "/"
				}
				var marks []string
				if ch.Library && !n.Library {
					marks = append(marks, "library")
				}
				if ch.Ignored {
					marks = append(marks, "ignored")
				}
				if len(marks) > 0 && (tree || ch.Dir) {
					name += "  [" + strings.Join(marks, ", ") + "]"
				}
				fmt.Fprintln(w, name)
				walk(ch, depth+1)
			}
		}
		walk(res.Tree, 0)
		if res.Truncated {
			fmt.Fprintln(c.env.Stderr, "tiegel: listing truncated")
		}
	})
}

func runRead(c *cli) error {
	var res api.FileContent
	if err := c.call("files.read", api.FilesReadParams{Path: c.p.args[0]}, &res); err != nil {
		return err
	}
	if c.json {
		return writeJSON(c.env.Stdout, res)
	}
	data := []byte(res.Content)
	if res.Encoding == "base64" {
		var err error
		if data, err = base64.StdEncoding.DecodeString(res.Content); err != nil {
			return err
		}
	}
	_, err := c.env.Stdout.Write(data)
	return err
}

func runLang(c *cli) error {
	out := make([]api.LanguageResult, 0, len(c.p.args))
	for _, f := range c.p.args {
		var r api.LanguageResult
		if err := c.call("files.detectLanguage", api.DetectLanguageParams{Path: f}, &r); err != nil {
			return err
		}
		r.Path = f
		out = append(out, r)
	}
	return c.emit(out, func(w io.Writer) {
		for _, r := range out {
			if len(out) == 1 {
				fmt.Fprintln(w, r.Language)
			} else {
				fmt.Fprintf(w, "%s\t%s\n", r.Path, r.Language)
			}
		}
	})
}

func runSearch(c *cli) error {
	params := api.SearchParams{
		Path: c.p.args[0], Pattern: c.p.args[1],
		Regex: c.p.has("regex"), CaseSensitive: c.p.has("case"), WholeWord: c.p.has("word"),
	}
	var err error
	if params.Limit, err = c.p.intValue("limit"); err != nil {
		return err
	}
	var res api.SearchResult
	if err := c.call("search.inDoc", params, &res); err != nil {
		return err
	}
	if err := c.emit(res, func(w io.Writer) {
		for _, m := range res.Matches {
			fmt.Fprintf(w, "%s:%d:%d: %s\n", c.p.args[0], m.Line, m.Col, m.LineText)
		}
	}); err != nil {
		return err
	}
	if len(res.Matches) == 0 {
		return &quietError{code: ExitError}
	}
	return nil
}

// quietError ends a command with an exit code but no message (like grep
// without matches).
type quietError struct{ code int }

func (e *quietError) Error() string { return "" }

func runInterpreters(c *cli) error {
	if len(c.p.args) > 0 {
		switch c.p.args[0] {
		case "select":
			if len(c.p.args) != 2 {
				return usagef("usage: tiegel --headless interpreters select ID")
			}
			var info phpruntime.InterpreterInfo
			if err := c.call("interp.select", phpruntime.InterpSelectParams{ID: c.p.args[1]}, &info); err != nil {
				return err
			}
			return c.emit(info, func(w io.Writer) { fmt.Fprintf(w, "selected %s\n", info.ID) })
		case "version":
			params := phpruntime.InterpVersionParams{Modules: c.p.has("modules"), Ini: c.p.has("ini")}
			if len(c.p.args) == 2 {
				params.ID = c.p.args[1]
			}
			var res phpruntime.InterpVersionResult
			if err := c.call("interp.version", params, &res); err != nil {
				return err
			}
			return c.emit(res, func(w io.Writer) {
				fmt.Fprintln(w, res.Version)
				for _, m := range res.Modules {
					fmt.Fprintf(w, "module\t%s\n", m)
				}
				for _, k := range sortedKeys(res.Ini) {
					fmt.Fprintf(w, "ini\t%s=%s\n", k, res.Ini[k])
				}
			})
		default:
			return usagef("usage: tiegel --headless %s", commands["interpreters"].usage)
		}
	}
	var list []phpruntime.InterpreterInfo
	if err := c.call("interp.list", nil, &list); err != nil {
		return err
	}
	return c.emit(list, func(w io.Writer) {
		if len(list) == 0 {
			fmt.Fprintln(w, "no PHP interpreter found")
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, i := range list {
			mark := " "
			if i.Selected {
				mark = "*"
			}
			v := i.Version
			if v == "" {
				v = "?"
			}
			fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", mark, i.Kind, v, i.ID, i.Label)
		}
		_ = tw.Flush()
	})
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func runCompose(c *cli) error {
	params := phpruntime.ComposeParams{Profiles: c.p.flags["profile"]}
	if len(c.p.args) > 0 {
		params.Dir = c.p.args[0]
	}
	var res phpruntime.ComposeResult
	if err := c.call("compose.detect", params, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if !res.Found {
			fmt.Fprintf(w, "no compose file in %s\n", res.Dir)
			return
		}
		fmt.Fprintf(w, "Project %s: %s\n", res.Name, strings.Join(res.Files, ", "))
		fmt.Fprintf(w, "Services: %s\n", strings.Join(res.Services, ", "))
		if len(res.Candidates) == 0 {
			fmt.Fprintln(w, "no PHP service detected")
		}
		for _, cand := range res.Candidates {
			fmt.Fprintf(w, "\n%s (score %d)", cand.Service, cand.Score)
			if cand.Image != "" {
				fmt.Fprintf(w, " image %s", cand.Image)
			}
			fmt.Fprintln(w)
			for _, r := range cand.Reasons {
				fmt.Fprintf(w, "  + %s\n", r)
			}
			for _, m := range cand.Mappings {
				fmt.Fprintf(w, "  map %s -> %s\n", m.Host, m.Remote)
			}
			for _, h := range cand.XdebugHints {
				fmt.Fprintf(w, "  %s [%s] %s\n", h.Severity, h.Code, h.Message)
			}
		}
	})
}

func runLSPServers(c *cli) error {
	var list []langphp.LSPServerInfo
	if err := c.call("lsp.servers", langphp.LSPServersParams{All: c.p.has("all")}, &list); err != nil {
		return err
	}
	return c.emit(list, func(w io.Writer) {
		if len(list) == 0 {
			fmt.Fprintln(w, "no PHP language server found (phpactor, phpantom; intelephense with --all)")
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, s := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, s.Path, s.License)
		}
		_ = tw.Flush()
	})
}

func runModules(c *cli) error {
	if len(c.p.args) > 0 {
		if len(c.p.args) != 2 || (c.p.args[0] != "enable" && c.p.args[0] != "disable") {
			return usagef("usage: tiegel --headless %s", commands["modules"].usage)
		}
		var m api.ModuleInfo
		if err := c.call("modules."+c.p.args[0], api.ModuleParams{ID: c.p.args[1]}, &m); err != nil {
			return err
		}
		return c.emit(m, func(w io.Writer) { fmt.Fprintf(w, "%s enabled=%v running=%v\n", m.ID, m.Enabled, m.Running) })
	}
	var list []api.ModuleInfo
	if err := c.call("modules.list", nil, &list); err != nil {
		return err
	}
	return c.emit(list, func(w io.Writer) {
		if len(list) == 0 {
			fmt.Fprintln(w, "no modules registered")
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, m := range list {
			state := "off"
			if m.Enabled {
				state = "on"
			}
			if m.Running {
				state = "running"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, state, strings.Join(m.DependsOn, ","), m.Error)
		}
		_ = tw.Flush()
	})
}

func runHistory(c *cli) error {
	file := c.p.args[0]
	parseID := func(flag string) (uint64, error) {
		id, err := strconv.ParseUint(c.p.value(flag), 10, 64)
		if err != nil {
			return 0, usagef("--%s needs a revision number", flag)
		}
		return id, nil
	}
	switch {
	case c.p.has("show"):
		id, err := parseID("show")
		if err != nil {
			return err
		}
		var rc api.RevisionContent
		if err := c.call("history.content", api.RevisionParams{Path: file, ID: id}, &rc); err != nil {
			return err
		}
		if c.json {
			return writeJSON(c.env.Stdout, rc)
		}
		_, err = io.WriteString(c.env.Stdout, rc.Text)
		return err
	case c.p.has("restore"):
		id, err := parseID("restore")
		if err != nil {
			return err
		}
		var rev history.Revision
		if err := c.call("history.restore", api.RevisionParams{Path: file, ID: id}, &rev); err != nil {
			return err
		}
		return c.emit(rev, func(w io.Writer) { fmt.Fprintf(w, "restored %s to revision %d (now #%d)\n", file, id, rev.ID) })
	}
	var revs []history.Revision
	if err := c.call("history.list", api.PathParams{Path: file}, &revs); err != nil {
		return err
	}
	return c.emit(revs, func(w io.Writer) {
		if len(revs) == 0 {
			fmt.Fprintf(w, "no history for %s\n", file)
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, r := range revs {
			fmt.Fprintf(tw, "#%d\t%s\t%d bytes\t%s\n", r.ID, r.Time.Local().Format(time.DateTime), r.Size, r.Label)
		}
		_ = tw.Flush()
	})
}

// stdio joins stdin and stdout into the stream Serve needs.
type stdio struct {
	io.Reader
	io.Writer
}

func (stdio) Close() error { return nil }

func runServe(c *cli) error {
	opts := api.ServeOptions{Concurrent: c.p.has("concurrent")}
	switch c.p.value("framing") {
	case "", "ndjson", "line":
		opts.Framing = jsonrpc2.LineFraming
	case "lsp", "header":
		opts.Framing = jsonrpc2.HeaderFraming
	default:
		return usagef("--framing must be ndjson or lsp")
	}
	if c.p.has("listen") {
		return runListen(c, opts)
	}
	for _, f := range []string{"token", "token-file", "allow-remote"} {
		if c.p.has(f) {
			return usagef("--%s needs --listen", f)
		}
	}
	err := c.app.Serve(c.ctx, stdio{c.env.Stdin, c.env.Stdout}, opts)
	if err != nil && c.ctx.Err() != nil {
		return nil // interrupted
	}
	return err
}

func runCall(c *cli) error {
	method := c.p.args[0]
	var raw json.RawMessage
	if len(c.p.args) == 2 {
		src := c.p.args[1]
		if src == "-" {
			b, err := io.ReadAll(c.env.Stdin)
			if err != nil {
				return err
			}
			src = string(b)
		}
		raw = json.RawMessage(strings.TrimSpace(src))
		if len(raw) > 0 && !json.Valid(raw) {
			return usagef("params are not valid JSON: %s", src)
		}
	}
	res, err := c.sess.Call(c.ctx, method, raw)
	if err != nil {
		return err
	}
	return writeJSON(c.env.Stdout, res)
}
