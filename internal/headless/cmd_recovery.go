package headless

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["dismiss"] = false
	flagSpec["start"] = false
	register(
		&command{name: "recovery", usage: "recovery [--dismiss [FILE...]]", summary: "unsaved changes preserved in Local History (e.g. a failed save on close)", maxArgs: -1, cwdProject: true, run: runRecovery},
	)
}

func runRecovery(c *cli) error {
	var pts []api.RecoveryPoint
	if c.p.has("dismiss") {
		if err := c.call("history.recoveryDismiss", api.RecoveryDismissParams{Paths: c.p.args}, &pts); err != nil {
			return err
		}
	} else {
		if len(c.p.args) > 0 {
			return usagef("usage: tiegel --headless %s", commands["recovery"].usage)
		}
		if err := c.call("history.recovery", nil, &pts); err != nil {
			return err
		}
	}
	return c.emit(pts, func(w io.Writer) {
		if len(pts) == 0 {
			fmt.Fprintln(w, "no recovery points")
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, p := range pts {
			fmt.Fprintf(tw, "%s\t#%d\t%s\t%s\t%s\n", p.Path, p.Revision, p.Time.Local().Format(time.DateTime), p.Reason, p.Error)
		}
		_ = tw.Flush()
		fmt.Fprintln(w, "show with: tiegel --headless history FILE --show ID")
	})
}
