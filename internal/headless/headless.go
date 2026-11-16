// Package headless implements `tiegel --headless`: single commands for
// scripts and pipelines (human readable or --json) and `serve`, JSON-RPC 2.0
// over stdio. Every command is a thin wrapper around methods of internal/api,
// so the CLI, the JSON-RPC server and the GUI share one implementation.
package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Env is the process environment of a CLI run. Tests replace the streams and
// the base API options.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Getenv reads environment variables; nil means os.Getenv.
	Getenv func(string) string
	// Options are the base options for the App; ConfigDir is overridden by
	// --config-dir or $TIEGEL_CONFIG_DIR.
	Options api.Options
	// Modules configures the compiled-in modules (all are installed).
	Modules modules.Options
}

// usageError marks wrong command-line usage (exit code 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

// flagSpec lists the known flags; true means the flag takes a value.
// Command files add their own flags in init.
var flagSpec = map[string]bool{
	// global
	"json": false, "version": false, "project": true, "config-dir": true, "help": false, "h": false,
	// serve
	"framing": true, "concurrent": false,
	// files
	"tree": false, "depth": true, "ignored": false, "libraries": false,
	// search
	"regex": false, "case": false, "word": false, "limit": true,
	// lsp-servers
	"all": false,
	// compose
	"profile": true,
	// debug-listen
	"addr": true, "map": true,
	// history
	"show": true, "restore": true,
	// interpreters version
	"modules": false, "ini": false,
	// update-check
	"prereleases": false,
	// php-check
	"php": true,
}

// parsed is the parsed command line.
type parsed struct {
	cmd   string
	args  []string
	flags map[string][]string
}

func (p *parsed) has(name string) bool { _, ok := p.flags[name]; return ok }

func (p *parsed) value(name string) string {
	v := p.flags[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func (p *parsed) intValue(name string) (int, error) {
	s := p.value(name)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, usagef("--%s needs a number, got %q", name, s)
	}
	return n, nil
}

// parseArgs separates flags (anywhere on the line, "--name value" or
// "--name=value") from the command and its arguments. "--" ends flag parsing.
func parseArgs(args []string) (*parsed, error) {
	p := &parsed{flags: map[string][]string{}}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if a == "--headless" {
			continue
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		takes, known := flagSpec[name]
		if !known {
			return nil, usagef("unknown flag %s", a)
		}
		switch {
		case takes && !hasVal:
			if i+1 >= len(args) {
				return nil, usagef("flag --%s needs a value", name)
			}
			i++
			val = args[i]
		case !takes && hasVal:
			return nil, usagef("flag --%s takes no value", name)
		}
		p.flags[name] = append(p.flags[name], val)
	}
	if len(pos) > 0 {
		p.cmd, p.args = pos[0], pos[1:]
	}
	return p, nil
}

// Run executes one headless invocation and returns the exit code. args are
// the arguments after the program name; "--headless" may be included.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	p, err := parseArgs(args)
	if err != nil {
		return fail(env, err)
	}
	if p.cmd == "" && p.has("version") {
		p.cmd = "version"
	}
	if p.cmd == "" || p.cmd == "help" || p.has("help") || p.has("h") {
		topic := p.cmd
		if topic == "help" || topic == "" {
			topic = ""
			if len(p.args) > 0 {
				topic = p.args[0]
			}
		}
		if err := printHelp(env.Stdout, topic); err != nil {
			return fail(env, err)
		}
		return ExitOK
	}
	cmd, ok := commands[p.cmd]
	if !ok {
		return fail(env, usagef("unknown command %q (see tiegel --headless help)", p.cmd))
	}
	if len(p.args) < cmd.minArgs || (cmd.maxArgs >= 0 && len(p.args) > cmd.maxArgs) {
		return fail(env, usagef("usage: tiegel --headless %s", cmd.usage))
	}

	opts := env.Options
	opts.Extensions = append(append([]func(*api.App) error(nil), opts.Extensions...), modules.Installer(env.Modules))
	if dir := p.value("config-dir"); dir != "" {
		opts.ConfigDir = dir
	} else if dir := env.Getenv("TIEGEL_CONFIG_DIR"); dir != "" {
		opts.ConfigDir = dir
	}
	app, err := api.New(opts)
	if err != nil {
		return fail(env, err)
	}
	defer app.Close()

	c := &cli{ctx: ctx, app: app, env: env, p: p, json: p.has("json")}
	c.sess = app.NewSession(nil)
	defer c.sess.Close()

	projectDir := p.value("project")
	if projectDir == "" && cmd.cwdProject {
		projectDir = "."
	}
	if projectDir != "" {
		if _, err := app.OpenProject(ctx, projectDir); err != nil {
			return fail(env, err)
		}
	}
	if err := cmd.run(c); err != nil {
		return fail(env, err)
	}
	return ExitOK
}

func fail(env Env, err error) int {
	var qe *quietError
	if errors.As(err, &qe) {
		return qe.code
	}
	fmt.Fprintf(env.Stderr, "tiegel: %v\n", err)
	var ue *usageError
	if errors.As(err, &ue) || api.IsUsageError(err) {
		return ExitUsage
	}
	return ExitError
}

// cli is the state of one command execution.
type cli struct {
	ctx  context.Context
	app  *api.App
	sess *api.Session
	env  Env
	p    *parsed
	json bool
}

// call invokes an API method with params given as a Go value and decodes
// the result into out (if non-nil).
func (c *cli) call(method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := c.sess.Call(c.ctx, method, raw)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// emit writes v as indented JSON in --json mode, otherwise runs human.
func (c *cli) emit(v any, human func(w io.Writer)) error {
	if c.json {
		return writeJSON(c.env.Stdout, v)
	}
	human(c.env.Stdout)
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// command is one CLI command.
type command struct {
	name    string
	usage   string
	summary string
	minArgs int
	maxArgs int // -1: unlimited
	// cwdProject opens the working directory as project unless --project
	// is given.
	cwdProject bool
	run        func(c *cli) error
}

var commands = map[string]*command{}

func register(cmds ...*command) {
	for _, c := range cmds {
		commands[c.name] = c
	}
}

func printHelp(w io.Writer, topic string) error {
	if topic != "" {
		c, ok := commands[topic]
		if !ok {
			return usagef("unknown command %q", topic)
		}
		fmt.Fprintf(w, "tiegel --headless %s\n\n%s\n", c.usage, c.summary)
		return nil
	}
	fmt.Fprint(w, `Tiegel headless mode – the IDE core without a window.

Usage:
  tiegel --headless <command> [arguments] [flags]

Global flags:
  --json             machine-readable output (JSON)
  --project DIR      project directory (default: working directory for project commands)
  --config-dir DIR   settings directory (default: $TIEGEL_CONFIG_DIR or the OS config dir)

Commands:
`)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-14s %s\n", n, commands[n].summary)
	}
	fmt.Fprint(w, `  help [command] show help

Exit codes: 0 success, 1 error, 2 usage error.
See docs/headless.md for the JSON-RPC protocol and the method list.
`)
	return nil
}
