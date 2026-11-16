package headless

import (
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/phpfeatures"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

func init() {
	flagSpec["exec-mode"] = true
	register(
		&command{name: "runtime-diagnose", usage: "runtime-diagnose [INTERPRETER-ID]", summary: "Xdebug setup of the interpreter: ini values, mappings, hints (exit 1 on warnings)", maxArgs: 1, cwdProject: true, run: runDiagnose},
		&command{name: "mappings", usage: "mappings [set HOST=REMOTE... | reset] [--exec-mode auto|exec|run]", summary: "show or correct host/container path mappings", maxArgs: -1, cwdProject: true, run: runMappings},
		&command{name: "php-override", usage: "php-override [VERSION | clear]", summary: "show, set or clear the persistent PHP version override", maxArgs: 1, cwdProject: true, run: runOverride},
	)
}

func runDiagnose(c *cli) error {
	var p phpruntime.DiagnoseParams
	if len(c.p.args) > 0 {
		p.Interpreter = c.p.args[0]
	}
	var d phpruntime.Diagnosis
	if err := c.call("runtime.diagnose", p, &d); err != nil {
		return err
	}
	warn := false
	for _, h := range d.Hints {
		warn = warn || h.Severity == "warning"
	}
	if err := c.emit(d, func(w io.Writer) {
		if d.Interpreter != nil {
			fmt.Fprintf(w, "interpreter %s (%s) PHP %s\n", d.Interpreter.ID, d.Interpreter.Kind, d.Version)
		}
		for _, k := range sortedKeys(d.Ini) {
			fmt.Fprintf(w, "ini %s=%s\n", k, d.Ini[k])
		}
		fmt.Fprintf(w, "exec-mode %s\n", d.ExecMode)
		for _, m := range d.Mappings.Mappings {
			fmt.Fprintf(w, "map %s -> %s (%s)\n", m.Host, m.Remote, d.Mappings.Source)
		}
		for _, h := range d.Hints {
			fmt.Fprintf(w, "%s [%s] %s\n", h.Severity, h.Code, h.Message)
		}
	}); err != nil {
		return err
	}
	if warn {
		return &quietError{code: ExitError}
	}
	return nil
}

func runMappings(c *cli) error {
	if mode := c.p.value("exec-mode"); mode != "" {
		if err := c.call("runtime.execMode", phpruntime.ExecModeParams{Mode: mode}, nil); err != nil {
			return err
		}
	}
	var res phpruntime.MappingsResult
	switch {
	case len(c.p.args) == 0:
		if err := c.call("runtime.mappings.get", nil, &res); err != nil {
			return err
		}
	case c.p.args[0] == "reset" && len(c.p.args) == 1:
		if err := c.call("runtime.mappings.set", phpruntime.MappingsSetParams{Reset: true}, &res); err != nil {
			return err
		}
	case c.p.args[0] == "set" && len(c.p.args) > 1:
		var ms []api.MappingInfo
		for _, a := range c.p.args[1:] {
			host, remote, ok := strings.Cut(a, "=")
			if !ok || host == "" || remote == "" {
				return usagef("mapping needs HOST=REMOTE, got %q", a)
			}
			ms = append(ms, api.MappingInfo{Host: host, Remote: remote})
		}
		if err := c.call("runtime.mappings.set", phpruntime.MappingsSetParams{Mappings: ms}, &res); err != nil {
			return err
		}
	default:
		return usagef("usage: tiegel --headless %s", commands["mappings"].usage)
	}
	return c.emit(res, func(w io.Writer) {
		if len(res.Mappings) == 0 {
			fmt.Fprintln(w, "no path mappings")
		}
		for _, m := range res.Mappings {
			fmt.Fprintf(w, "%s\t%s\t%s\n", m.Host, m.Remote, res.Source)
		}
	})
}

func runOverride(c *cli) error {
	if len(c.p.args) == 1 {
		v := c.p.args[0]
		if v == "clear" {
			v = ""
		}
		if err := c.call("php.setVersionOverride", phpfeatures.OverrideParams{Version: v}, nil); err != nil {
			return err
		}
	}
	var pv phpruntime.PHPVersionResult
	if err := c.call("php.version", phpruntime.PHPVersionParams{}, &pv); err != nil {
		return err
	}
	return c.emit(pv, func(w io.Writer) { fmt.Fprintf(w, "%s\t%s\n", pv.Version, pv.Source) })
}
