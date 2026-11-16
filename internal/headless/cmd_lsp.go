package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

func init() {
	flagSpec["server"] = true
	flagSpec["command"] = true
	flagSpec["timeout"] = true
	register(&command{
		name:       "lsp-check",
		usage:      "lsp-check FILE... [--server ID] [--command PATH] [--timeout DUR]",
		summary:    "language server diagnostics of files (exit 1 on errors/warnings)",
		minArgs:    1,
		maxArgs:    -1,
		cwdProject: true,
		run:        runLSPCheck,
	})
}

// lspCheckResult is one file of the lsp-check output.
type lspCheckResult struct {
	Path        string               `json:"path"`
	Version     uint64               `json:"version"`
	Diagnostics []langphp.Diagnostic `json:"diagnostics"`
}

func runLSPCheck(c *cli) error {
	timeout := 10 * time.Second
	if v := c.p.value("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return usagef("--timeout needs a duration like 10s, got %q", v)
		}
		timeout = d
	}
	events := make(chan langphp.Diagnostics, 64)
	sess := c.app.NewSession(func(ev api.Event) {
		var d langphp.Diagnostics
		if json.Unmarshal(ev.Payload, &d) == nil {
			select {
			case events <- d:
			default: // nobody waits any more
			}
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicLSPDiagnostics)
	callSess := func(method string, params, out any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		res, err := sess.Call(c.ctx, method, raw)
		if err != nil || out == nil {
			return err
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if err := callSess("lsp.start", langphp.StartParams{Server: c.p.value("server"), Command: c.p.value("command")}, nil); err != nil {
		return err
	}
	defer func() { _ = callSess("lsp.stop", nil, nil) }()

	want := map[string]uint64{} // display path → version
	order := make([]string, 0, len(c.p.args))
	for _, f := range c.p.args {
		var st api.DocOpenResult
		if err := callSess("doc.open", api.DocOpenParams{Path: f}, &st); err != nil {
			return err
		}
		if st.Language != "php" {
			return fmt.Errorf("%s is not a PHP file (language %s)", f, st.Language)
		}
		want[st.Path] = st.Version
		order = append(order, st.Path)
	}
	got := map[string]langphp.Diagnostics{}
	deadline := time.After(timeout)
	for len(got) < len(want) {
		select {
		case d := <-events:
			if v, ok := want[d.Path]; ok && d.Version >= v {
				got[d.Path] = d
			}
		case <-deadline:
			var missing []string
			for _, p := range order {
				if _, ok := got[p]; !ok {
					missing = append(missing, p)
				}
			}
			return fmt.Errorf("timeout after %s waiting for diagnostics of %v", timeout, missing)
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
	failed := false
	out := make([]lspCheckResult, 0, len(order))
	for _, p := range order {
		d := got[p]
		sort.SliceStable(d.Diagnostics, func(a, b int) bool { return d.Diagnostics[a].Start < d.Diagnostics[b].Start })
		for _, dd := range d.Diagnostics {
			failed = failed || dd.Severity == "error" || dd.Severity == "warning"
		}
		out = append(out, lspCheckResult{Path: p, Version: d.Version, Diagnostics: d.Diagnostics})
	}
	if err := c.emit(out, func(w io.Writer) {
		for i, r := range out {
			for _, d := range r.Diagnostics {
				fmt.Fprintf(w, "%s:%d:%d: %s: %s", c.p.args[i], d.Line, d.Col, d.Severity, d.Message)
				switch {
				case d.Source != "" && d.Code != "":
					fmt.Fprintf(w, " [%s %s]", d.Source, d.Code)
				case d.Source != "" || d.Code != "":
					fmt.Fprintf(w, " [%s%s]", d.Source, d.Code)
				}
				fmt.Fprintln(w)
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
