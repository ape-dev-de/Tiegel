package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["force"] = false
	register(
		&command{name: "projects", usage: "projects [list | open DIR | switch NAME | hibernate NAME [--force] | resume NAME | close NAME [--force] | busy NAME | client-state NAME JSON]",
			summary: "several open projects: list, open, switch, hibernate (release language server, watcher, index), resume, close, client-state (attach to a hibernated project); NAME is the root or the directory name",
			maxArgs: 3, run: runProjects},
	)
}

func runProjects(c *cli) error {
	sub := "list"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	usage := func() error { return usagef("usage: tiegel --headless %s", commands["projects"].usage) }
	arg := ""
	switch {
	case sub == "list" && len(c.p.args) <= 1:
	case sub == "client-state" && len(c.p.args) == 3:
		arg = c.p.args[1]
	case sub != "list" && sub != "client-state" && len(c.p.args) == 2:
		arg = c.p.args[1]
	default:
		return usage()
	}
	if c.p.has("force") && sub != "hibernate" && sub != "close" {
		return usagef("--force only applies to hibernate and close")
	}
	if _, err := c.app.EnableWorkspace(api.WorkspaceOptions{}); err != nil {
		return err
	}
	var sess api.ProjectSession
	var err error
	switch sub {
	case "list":
		var list api.ProjectsListResult
		if err := c.call("projects.list", nil, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) { printProjects(w, list) })
	case "open":
		err = c.call("projects.open", api.PathParams{Path: arg}, &sess)
	case "switch":
		err = c.call("projects.switch", api.ProjectsNameParams{Name: arg}, &sess)
	case "hibernate":
		err = c.call("projects.hibernate", api.ProjectsHibernateParams{Name: arg, Force: c.p.has("force")}, &sess)
	case "resume":
		err = c.call("projects.resume", api.ProjectsNameParams{Name: arg}, &sess)
	case "close":
		var list api.ProjectsListResult
		if err := c.call("projects.close", api.ProjectsCloseParams{Name: arg, Force: c.p.has("force")}, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) { printProjects(w, list) })
	case "client-state":
		state := json.RawMessage(c.p.args[2])
		if !json.Valid(state) {
			return usagef("client-state: %q is not JSON", c.p.args[2])
		}
		var res api.ProjectsClientStateResult
		if err := c.call("projects.attachClientState", api.ProjectsClientStateParams{Name: arg, ClientState: state}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if res.Attached {
				fmt.Fprintln(w, "attached")
			} else {
				fmt.Fprintln(w, "not hibernated: dropped")
			}
		})
	case "busy":
		var busy api.ProjectsBusyResult
		if err := c.call("projects.busy", api.ProjectsNameParams{Name: arg}, &busy); err != nil {
			return err
		}
		return c.emit(busy, func(w io.Writer) {
			if len(busy.Busy) == 0 {
				fmt.Fprintln(w, "not busy")
			}
			for _, b := range busy.Busy {
				fmt.Fprintln(w, b)
			}
		})
	default:
		return usage()
	}
	if err != nil {
		return err
	}
	return c.emit(sess, func(w io.Writer) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", sess.Name, sess.State, sess.Root)
	})
}

func printProjects(w io.Writer, list api.ProjectsListResult) {
	if len(list.Projects) == 0 {
		fmt.Fprintln(w, "no open projects")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, p := range list.Projects {
		docs := ""
		if len(p.Docs) > 0 {
			docs = fmt.Sprintf("%d documents", len(p.Docs))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, p.State, p.Root, docs)
	}
	_ = tw.Flush()
}
