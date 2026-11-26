package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/mcp"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// Project knowledge (D560–D563): "knowledge" drives the knowledge module
// (knowledge.*); "mcp knowledge" serves the knowledge to MCP clients
// (coding agents) over stdio. "build" without --yes only shows the plan
// with the estimate: building runs the coding agent and costs tokens.

func init() {
	flagSpec["k"] = true
	flagSpec["model"] = true
	flagSpec["no-embed"] = false
	flagSpec["keyword"] = false
	register(&command{name: "knowledge",
		usage: "knowledge [status | plan [--scope DIR]... [--all] | build [UNIT]... [--scope DIR]... [--all] [--no-embed] [--yes] | cancel | list | show [UNIT] | read PATH | " +
			"search QUERY... [-k N] [--keyword] | embed [--model M] | pull [--yes] | mcp-config]",
		summary: "project knowledge base: agents describe data flows into Markdown outside the repo, hybrid search; build needs --yes (uses the agent's tokens)",
		maxArgs: -1, cwdProject: true, run: runKnowledge},
		&command{name: "mcp", usage: "mcp knowledge [--project DIR]",
			summary: "MCP server over stdio with the tools knowledge_search, knowledge_read, knowledge_list (for opencode, Claude and other MCP clients)",
			minArgs: 1, maxArgs: 1, cwdProject: true, run: runMCP})
}

func runKnowledge(c *cli) error {
	sub := "status"
	args := c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	scope := api.KnowledgeScopeParams{Scope: c.p.flags["scope"], All: c.p.has("all")}
	switch sub {
	case "status":
		var st api.KnowledgeStatus
		if err := c.call("knowledge.status", nil, &st); err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) { printKnowledgeStatus(w, st) })
	case "plan":
		var pl api.KnowledgePlan
		if err := c.call("knowledge.plan", scope, &pl); err != nil {
			return err
		}
		return c.emit(pl, func(w io.Writer) { printKnowledgePlan(w, pl) })
	case "build":
		return c.knowledgeBuild(scope, args)
	case "cancel":
		var r api.KnowledgeRun
		if err := c.call("knowledge.cancel", nil, &r); err != nil {
			return err
		}
		return c.emit(r, func(w io.Writer) { fmt.Fprintln(w, "no build is running in this process") })
	case "list":
		var us []api.KnowledgeUnit
		if err := c.call("knowledge.list", nil, &us); err != nil {
			return err
		}
		return c.emit(us, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "UNIT\tTITLE\tFILES\tCONFIDENCE\tSTATE")
			for _, u := range us {
				state := "current"
				if u.Stale != "" {
					state = u.Stale
				}
				conf := "-"
				if u.MD != "" {
					conf = fmt.Sprintf("%.2f", u.Confidence)
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", u.ID, u.Title, u.Files, conf, state)
			}
			_ = tw.Flush()
		})
	case "show", "read":
		var doc api.KnowledgeDoc
		var err error
		arg := strings.Join(args, " ")
		if sub == "show" {
			err = c.call("knowledge.show", api.KnowledgeUnitParams{Unit: arg}, &doc)
		} else {
			if arg == "" {
				return usagef("usage: tiegel --headless knowledge read PATH")
			}
			err = c.call("knowledge.read", api.KnowledgeReadParams{Path: arg}, &doc)
		}
		if err != nil {
			return err
		}
		return c.emit(doc, func(w io.Writer) { fmt.Fprint(w, doc.Content) })
	case "search":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless knowledge search QUERY... [-k N]")
		}
		k, err := c.p.intValue("k")
		if err != nil {
			return err
		}
		var res api.KnowledgeSearchResult
		if err := c.call("knowledge.search", api.KnowledgeSearchParams{Query: strings.Join(args, " "), K: k, KeywordOnly: c.p.has("keyword")}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) { printKnowledgeHits(w, res) })
	case "embed":
		var res api.KnowledgeEmbedResult
		if err := c.call("knowledge.embed", api.KnowledgeEmbedParams{Model: c.p.value("model")}, &res); err != nil {
			return err
		}
		if err := c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "%d sections, %d embedded now, %d removed, %d without vector (%s)\n", res.Chunks, res.Embedded, res.Deleted, res.Pending, res.Model)
			if res.Error != "" {
				fmt.Fprintln(w, "embedding failed: "+res.Error+" – keyword search still works")
			}
		}); err != nil {
			return err
		}
		if res.Error != "" {
			return &quietError{code: ExitError}
		}
		return nil
	case "pull":
		var st api.KnowledgeModelStatus
		if err := c.call("knowledge.modelStatus", nil, &st); err != nil {
			return err
		}
		if st.PullCommand == "" {
			return fmt.Errorf("the %s provider has no pull; the model %s is used as configured", st.Provider, st.Model)
		}
		if !c.p.has("yes") {
			return c.emit(st, func(w io.Writer) {
				state := "missing"
				switch {
				case !st.Reachable:
					state = "server not reachable"
				case st.Available:
					state = "available"
				}
				fmt.Fprintf(w, "model %s at %s: %s\n", st.Model, st.Endpoint, state)
				fmt.Fprintf(w, "pull downloads the model: %s\nrun with --yes to pull\n", st.PullCommand)
			})
		}
		fmt.Fprintf(c.env.Stderr, "running: %s (at %s)\n", st.PullCommand, st.Endpoint)
		if err := c.call("knowledge.pullModel", api.KnowledgePullParams{Wait: true}, &st); err != nil {
			return err
		}
		if st.Error != "" {
			return fmt.Errorf("%s", st.Error)
		}
		return c.emit(st, func(w io.Writer) { fmt.Fprintf(w, "model %s: %s\n", st.Model, st.PullStatus) })
	case "mcp-config":
		var mc api.KnowledgeMCPConfig
		if err := c.call("knowledge.mcpConfig", nil, &mc); err != nil {
			return err
		}
		return c.emit(mc, func(w io.Writer) {
			fmt.Fprintf(w, "opencode (opencode.json):\n%s\n\nClaude Code (.mcp.json) and other MCP clients:\n%s\n", mc.Opencode, mc.Claude)
		})
	}
	return usagef("unknown knowledge command %q (see tiegel --headless help knowledge)", sub)
}

func printKnowledgeStatus(w io.Writer, st api.KnowledgeStatus) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "Directory:\t%s\n", st.Dir)
	fmt.Fprintf(tw, "Units:\t%d built, %d to build, %d failed\n", st.Units, st.Stale, st.Failed)
	if !st.LastRun.IsZero() {
		fmt.Fprintf(tw, "Last run:\t%s\n", st.LastRun.Local().Format("2006-01-02 15:04"))
	}
	model := st.Model
	if model == "" {
		model = "agent default"
	}
	fmt.Fprintf(tw, "Agent:\t%s (%s)\n", st.Agent, model)
	e := st.Embedding
	where := "local"
	if !e.Local {
		where = "contents are sent to this server"
	}
	fmt.Fprintf(tw, "Embeddings:\t%s %s at %s (%s)\n", e.Provider, e.Model, e.Endpoint, where)
	fmt.Fprintf(tw, "Store:\t%d sections, %d embedded, %d KB\n", e.Chunks, e.Embedded, e.Bytes/1024)
	if e.Recovered != "" {
		fmt.Fprintf(tw, "\tthe store was broken and was reset: %s\n", e.Recovered)
	}
	fmt.Fprintf(tw, "Agent access:\t%s\n", map[bool]string{true: "on (MCP tools knowledge_search, knowledge_read, knowledge_list)", false: "off"}[st.GiveAgent])
	if st.Run.Running {
		fmt.Fprintf(tw, "Build:\trunning (%s)\n", st.Run.Phase)
	}
	_ = tw.Flush()
}

func printKnowledgePlan(w io.Writer, pl api.KnowledgePlan) {
	if len(pl.Units) == 0 {
		fmt.Fprintf(w, "nothing to build: all %d units are current (--all rebuilds them)\n", pl.Total)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "UNIT\tTITLE\tFILES\tWHY")
	for _, u := range pl.Units {
		why := u.Stale
		if why == "" {
			why = "rebuild"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", u.ID, u.Title, u.Files, why)
	}
	_ = tw.Flush()
	e := pl.Estimate
	fmt.Fprintf(w, "\n%d of %d units, %d files. %s\n", e.Units, pl.Total, e.Files, e.Note)
}

func printKnowledgeHits(w io.Writer, res api.KnowledgeSearchResult) {
	if res.Note != "" {
		fmt.Fprintln(w, "note: "+res.Note)
	}
	if len(res.Hits) == 0 {
		fmt.Fprintln(w, "no results")
	}
	for i, h := range res.Hits {
		fmt.Fprintf(w, "%d. %.3f  %s  (%s#%s)\n", i+1, h.Score, h.Heading, h.Path, h.Anchor)
		text := h.Snippet
		if r := []rune(text); len(r) > 240 {
			text = string(r[:239]) + "…"
		}
		fmt.Fprintf(w, "   %s\n", text)
	}
}

// knowledgeBuild prints the plan, and with --yes runs the build with
// progress lines.
func (c *cli) knowledgeBuild(scope api.KnowledgeScopeParams, units []string) error {
	var pl api.KnowledgePlan
	if err := c.call("knowledge.plan", scope, &pl); err != nil {
		return err
	}
	if len(units) > 0 {
		pl.Estimate.Units = len(units)
	}
	if !c.p.has("yes") {
		return c.emit(pl, func(w io.Writer) {
			printKnowledgePlan(w, pl)
			if len(pl.Units) > 0 {
				fmt.Fprintln(w, "\nnothing was run: add --yes to build (uses your agent's tokens)")
			}
		})
	}
	var mu sync.Mutex
	sess := c.app.NewSession(func(ev api.Event) {
		if c.json || ev.Topic != api.TopicKnowledgeProgress {
			return
		}
		var p api.KnowledgeProgress
		if json.Unmarshal(ev.Payload, &p) != nil || p.State == "queued" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		line := fmt.Sprintf("[%d/%d] %s: %s %s", p.Index+1, p.Total, p.Unit, p.Step, p.State)
		switch {
		case p.Attempt > 0 && p.State == "running":
			line += fmt.Sprintf(" (repair %d: %s)", p.Attempt, p.Error)
		case p.Error != "":
			line += ": " + p.Error
		case p.State == "done":
			line += fmt.Sprintf(" (confidence %.2f)", p.Confidence)
		}
		fmt.Fprintln(c.env.Stderr, line)
	})
	defer sess.Close()
	sess.Subscribe(api.TopicKnowledgeProgress)
	var run api.KnowledgeRun
	err := c.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: scope, Units: units, Wait: true, NoEmbed: c.p.has("no-embed")}, &run)
	sess.Flush(c.ctx)
	if err != nil {
		return err
	}
	if err := c.emit(run, func(w io.Writer) {
		fmt.Fprintf(w, "%d units built, %d failed", run.Done, run.Failed)
		if run.Cancelled {
			fmt.Fprint(w, ", cancelled")
		}
		fmt.Fprintln(w)
		for _, e := range run.Errors {
			fmt.Fprintln(w, "  "+e)
		}
		if run.EmbedError != "" {
			fmt.Fprintln(w, "embedding: "+run.EmbedError+" – keyword search works; run knowledge embed later")
		}
	}); err != nil {
		return err
	}
	switch {
	case run.Cancelled:
		return &quietError{code: 130}
	case run.Failed > 0:
		return &quietError{code: ExitError}
	}
	return nil
}

// runMCP serves the knowledge tools over stdin/stdout. Nothing but MCP
// messages may go to stdout.
func runMCP(c *cli) error {
	if c.p.args[0] != "knowledge" {
		return usagef("unknown MCP server %q (available: knowledge)", c.p.args[0])
	}
	srv := &mcp.Server{Name: "tiegel-knowledge", Version: version.Version,
		Instructions: "Project knowledge base generated by Tiegel from the source code: data flows (entry points, transformations, persistence, external calls, events) per module, with links to files and lines. " +
			"Search it before reading many files to find where something happens; read a unit's page for the full flow. The pages can be outdated: check the code before changing it.",
		Tools: knowledgeTools(c)}
	return srv.Serve(c.ctx, c.env.Stdin, c.env.Stdout)
}

func knowledgeTools(c *cli) []mcp.Tool {
	call := func(ctx context.Context, method string, params, out any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		res, err := c.sess.Call(ctx, method, raw)
		if err != nil {
			return err
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	return []mcp.Tool{
		{Name: "knowledge_search", Description: "Search the project knowledge base (data flows per module) with a natural language or keyword query. Returns the best matching sections with their page and heading.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"what you look for, e.g. 'where are orders persisted'"},"k":{"type":"integer","description":"number of results (default 6, max 20)","minimum":1,"maximum":20}},"required":["query"]}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Query string `json:"query"`
					K     int    `json:"k"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				if a.K <= 0 {
					a.K = 6
				}
				var res api.KnowledgeSearchResult
				if err := call(ctx, "knowledge.search", api.KnowledgeSearchParams{Query: a.Query, K: min(a.K, 20)}, &res); err != nil {
					return "", err
				}
				var b strings.Builder
				if res.Note != "" {
					b.WriteString("(" + res.Note + ")\n\n")
				}
				if len(res.Hits) == 0 {
					b.WriteString("No results. knowledge_list shows the pages.")
				}
				for i, h := range res.Hits {
					fmt.Fprintf(&b, "## %d. %s\npage: %s  section: #%s  score: %.3f\n\n%s\n\n", i+1, h.Heading, h.Path, h.Anchor, h.Score, strings.TrimSpace(h.Text))
				}
				return b.String(), nil
			}},
		{Name: "knowledge_read", Description: "Read a page of the project knowledge base: 'index.md' (table of contents), 'units/<id>.md' or a unit id.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"index.md, units/<id>.md or a unit id"}},"required":["path"]}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				var doc api.KnowledgeDoc
				if err := call(ctx, "knowledge.read", api.KnowledgeReadParams{Path: a.Path}, &doc); err != nil {
					return "", err
				}
				return doc.Content, nil
			}},
		{Name: "knowledge_list", Description: "List the pages (units) of the project knowledge base with their folder, number of files and confidence.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				var us []api.KnowledgeUnit
				if err := call(ctx, "knowledge.list", nil, &us); err != nil {
					return "", err
				}
				var b strings.Builder
				n := 0
				for _, u := range us {
					if u.MD == "" {
						continue
					}
					n++
					fmt.Fprintf(&b, "- units/%s.md  %s  (%s, %d files, confidence %.2f", u.ID, u.Title, u.Dir, u.Files, u.Confidence)
					if u.Stale != "" {
						b.WriteString(", outdated: " + u.Stale)
					}
					b.WriteString(")\n")
				}
				if n == 0 {
					return "The knowledge base is empty (build it in Tiegel: Knowledge window or `tiegel --headless knowledge build`).", nil
				}
				return b.String(), nil
			}},
	}
}
