package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["backend"] = true
	flagSpec["exclude"] = true
	register(
		&command{name: "watch", usage: "watch [--backend auto|fsevents|fsnotify|poll] [--exclude DIR]...", summary: "watch the project and print file and Git changes until Ctrl+C", maxArgs: 0, cwdProject: true, run: runWatch},
	)
}

// watchTopics are the events `watch` prints.
var watchTopics = []string{api.TopicFilesChanged, api.TopicGitChanged, api.TopicWatchStatus}

func runWatch(c *cli) error {
	var mu sync.Mutex
	out := c.env.Stdout
	print := func(ev api.Event) {
		mu.Lock()
		defer mu.Unlock()
		if c.json {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(out, "%s\n", b)
			return
		}
		printWatchEvent(out, ev)
	}
	sess := c.app.NewSession(print)
	defer sess.Close()
	sess.Subscribe(watchTopics...)
	params := api.WatchStartParams{Backend: c.p.value("backend"), Exclude: c.p.flags["exclude"]}
	raw, _ := json.Marshal(params)
	if _, err := sess.Call(c.ctx, "watch.start", raw); err != nil {
		return err
	}
	<-c.ctx.Done()
	return nil // Run closes the App, which stops the watcher
}

func printWatchEvent(w io.Writer, ev api.Event) {
	switch ev.Topic {
	case api.TopicFilesChanged:
		var fc api.FilesChangedEvent
		if json.Unmarshal(ev.Payload, &fc) != nil {
			return
		}
		for _, ch := range fc.Changes {
			path := ch.Path
			if ch.Dir {
				path += "/"
			}
			if ch.OldPath != "" {
				fmt.Fprintf(w, "%-8s %s -> %s\n", ch.Kind, ch.OldPath, path)
				continue
			}
			fmt.Fprintf(w, "%-8s %s\n", ch.Kind, path)
		}
	case api.TopicGitChanged:
		var g api.GitChangedEvent
		if json.Unmarshal(ev.Payload, &g) != nil {
			return
		}
		fmt.Fprintf(w, "%-8s %s\n", "git", strings.Join(g.Paths, " "))
	case api.TopicWatchStatus:
		var st api.WatchStatus
		if json.Unmarshal(ev.Payload, &st) != nil {
			return
		}
		switch {
		case !st.Running:
			fmt.Fprintln(w, "watcher stopped")
		case st.Degraded != "":
			fmt.Fprintf(w, "watcher degraded: %s\n", st.Degraded)
		default:
			fmt.Fprintf(w, "watching %s (%s, %d files in %d directories, %.0f ms)\n", st.Root, st.Backend, st.Files, st.Dirs, st.StartupMs)
		}
	}
}
