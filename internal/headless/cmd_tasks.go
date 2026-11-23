package headless

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

func init() {
	register(
		&command{name: "tasks", usage: "tasks [cancel ID]", summary: "running background tasks (indexing, git, Composer, QA, language server); cancel one", maxArgs: 2, run: runTasks},
	)
}

func runTasks(c *cli) error {
	var list []tasks.Info
	switch {
	case len(c.p.args) == 0:
		if err := c.call("tasks.list", nil, &list); err != nil {
			return err
		}
	case len(c.p.args) == 2 && c.p.args[0] == "cancel":
		var res api.TaskCancelResult
		if err := c.call("tasks.cancel", api.TaskIDParams{ID: c.p.args[1]}, &res); err != nil {
			return err
		}
		list = res.Tasks
	default:
		return usagef("usage: tiegel --headless %s", commands["tasks"].usage)
	}
	return c.emit(list, func(w io.Writer) { printTasks(w, list) })
}

// printTasks writes one line per task: id, progress, module, title, text.
func printTasks(w io.Writer, list []tasks.Info) {
	if len(list) == 0 {
		fmt.Fprintln(w, "no background tasks")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, t := range list {
		prog := "…"
		if t.Total > 0 {
			prog = fmt.Sprintf("%d/%d", t.Done, t.Total)
		}
		var flags []string
		if t.Cancellable {
			flags = append(flags, "cancellable")
		}
		if t.Cancelling {
			flags = append(flags, "cancelling")
		}
		detail := t.Text
		if t.Error != "" {
			detail = "failed: " + t.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, prog, t.Module, t.Title, detail,
			time.Since(t.StartedAt).Round(time.Second), strings.Join(flags, ","))
	}
	_ = tw.Flush()
}
