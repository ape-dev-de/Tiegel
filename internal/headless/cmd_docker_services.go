package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
)

// Services window commands (AP14.3): docker ps|up|down|start|stop|restart|
// logs|shell|open|watch.

func init() {
	flagSpec["f"] = false
	flagSpec["follow"] = false
	flagSpec["tail"] = true
	flagSpec["filter"] = true
	flagSpec["timestamps"] = false
	flagSpec["container"] = false
	flagSpec["shell"] = true
}

// dockerServiceUsage is appended to the usage of the docker command.
const dockerServiceUsage = " | ps [--all] | up|start|stop|restart [SERVICE] [--container] | down --yes | logs [SERVICE] [-f] [--tail N|all] [--filter TEXT] [--timestamps] [--container] | shell SERVICE [--shell SH] [--container] | open URL | watch [--all]"

// runDockerServices handles the Services subcommands; ok is false for an
// unknown subcommand.
func runDockerServices(c *cli, sub, arg string) (bool, error) {
	switch sub {
	case "ps":
		if arg != "" {
			return true, usagef("usage: tiegel --headless docker ps [--all]")
		}
		return true, dockerPS(c)
	case "up", "start", "stop", "restart", "down":
		return true, dockerAction(c, sub, arg)
	case "logs":
		return true, dockerLogs(c, arg)
	case "shell":
		if arg == "" {
			return true, usagef("usage: tiegel --headless docker shell SERVICE [--shell SH] [--container]")
		}
		p := dockermod.ShellParams{Service: arg, Shell: c.p.value("shell")}
		if c.p.has("container") {
			p.Service, p.Container = "", arg
		}
		var res dockermod.LaunchResult
		if err := c.call("docker.shell", p, &res); err != nil {
			return true, err
		}
		return true, c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "%s\nopened with: %s\n", strings.Join(res.Command, " "), strings.Join(res.Launched, " "))
		})
	case "open":
		if arg == "" {
			return true, usagef("usage: tiegel --headless docker open URL")
		}
		var res dockermod.LaunchResult
		if err := c.call("docker.openURL", dockermod.URLParams{URL: arg}, &res); err != nil {
			return true, err
		}
		return true, c.emit(res, func(w io.Writer) { fmt.Fprintln(w, strings.Join(res.Launched, " ")) })
	case "watch":
		return true, dockerWatch(c)
	}
	return false, nil
}

func dockerPS(c *cli) error {
	var res dockermod.ServicesResult
	if err := c.call("docker.services", dockermod.ServicesParams{Standalone: c.p.has("all")}, &res); err != nil {
		return err
	}
	if err := c.emit(res, func(w io.Writer) { printServices(w, res) }); err != nil {
		return err
	}
	failed := res.Error != ""
	for _, p := range res.Projects {
		failed = failed || p.Error != ""
	}
	if failed {
		return &quietError{code: ExitError}
	}
	return nil
}

func portList(ports []dk.Port) string {
	var out []string
	for _, p := range ports {
		switch {
		case p.URL != "":
			out = append(out, p.URL)
		case p.Published > 0:
			out = append(out, fmt.Sprintf("%d->%d/%s", p.Published, p.Target, p.Protocol))
		default:
			out = append(out, fmt.Sprintf("%d/%s", p.Target, p.Protocol))
		}
	}
	return strings.Join(out, " ")
}

func printServices(w io.Writer, res dockermod.ServicesResult) {
	ctxName := res.Context
	if ctxName == "" {
		ctxName = "(docker's current)"
	}
	fmt.Fprintf(w, "context %s %s\n", ctxName, res.Endpoint)
	if res.Error != "" {
		fmt.Fprintf(w, "error: %s\n", res.Error)
	}
	if len(res.Projects) == 0 {
		fmt.Fprintln(w, "no compose project")
	}
	for _, p := range res.Projects {
		fmt.Fprintf(w, "project %s (%s)\n", p.Name, strings.Join(p.Files, ", "))
		if p.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", p.Error)
		}
		for _, s := range p.Services {
			state := s.State
			if s.Health != "" {
				state += " (" + s.Health + ")"
			}
			if !s.Defined {
				state += " [not in compose files]"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", s.Name, state, portList(s.Ports))
			for _, ct := range s.Containers {
				fmt.Fprintf(w, "    %s\t%s\t%s\n", ct.Name, ct.State, ct.Status)
			}
		}
	}
	if len(res.Standalone) > 0 {
		fmt.Fprintln(w, "other containers")
		for _, ct := range res.Standalone {
			name := ct.Name
			if ct.ComposeProject != "" {
				name += " [" + ct.ComposeProject + "/" + ct.ComposeService + "]"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", name, ct.State, ct.Status, portList(ct.Ports))
		}
	}
}

func dockerAction(c *cli, action, arg string) error {
	p := dockermod.ServiceActionParams{Action: action, Service: arg}
	if c.p.has("container") {
		p.Service, p.Container = "", arg
	}
	if action == "down" {
		if arg != "" {
			return usagef("usage: tiegel --headless docker down --yes (down applies to the whole compose project)")
		}
		if !c.p.has("yes") {
			return usagef("docker down stops and removes the containers and networks of the compose project; repeat with --yes")
		}
		p.Confirm = true
	}
	var res dockermod.ServiceActionResult
	err := c.call("docker.serviceAction", p, &res)
	if err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, l := range res.Output {
			fmt.Fprintln(w, l)
		}
		fmt.Fprintf(w, "%s: %s done\n", res.Target, res.Action)
	})
}

func logParams(c *cli, arg string) (dockermod.LogsParams, error) {
	p := dockermod.LogsParams{Service: arg, Follow: c.p.has("f") || c.p.has("follow"), Filter: c.p.value("filter"), Timestamps: c.p.has("timestamps")}
	if c.p.has("container") {
		if arg == "" {
			return p, usagef("usage: tiegel --headless docker logs CONTAINER --container")
		}
		p.Service, p.Container = "", arg
	}
	switch v := c.p.value("tail"); {
	case !c.p.has("tail"):
	case strings.EqualFold(v, "all"):
		p.Tail = -1
	default:
		n, err := c.p.intValue("tail")
		if err != nil {
			return p, err
		}
		if n <= 0 {
			n = -1 // all
		}
		p.Tail = n
	}
	return p, nil
}

func printLogLine(w io.Writer, l dockermod.LogLine) {
	if l.Container != "" {
		fmt.Fprintf(w, "%s | %s\n", l.Container, l.Text)
		return
	}
	fmt.Fprintln(w, l.Text)
}

func dockerLogs(c *cli, arg string) error {
	p, err := logParams(c, arg)
	if err != nil {
		return err
	}
	if !p.Follow {
		var res dockermod.LogsResult
		if err := c.call("docker.logs", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			for _, l := range res.Lines {
				printLogLine(w, l)
			}
		})
	}
	// Follow: print docker.log events until Ctrl+C or the stream ends.
	var (
		mu sync.Mutex
		id string
	)
	done := make(chan string, 1)
	out := c.env.Stdout
	sess := c.app.NewSession(func(ev api.Event) {
		var le dockermod.LogEvent
		if json.Unmarshal(ev.Payload, &le) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if le.ID != id {
			return
		}
		if c.json {
			b, _ := json.Marshal(le)
			fmt.Fprintf(out, "%s\n", b)
		} else {
			for _, l := range le.Lines {
				printLogLine(out, l)
			}
		}
		if le.Done {
			select {
			case done <- le.Error:
			default:
			}
		}
	})
	defer sess.Close()
	sess.Subscribe(dockermod.TopicLog)
	raw, _ := json.Marshal(p)
	mu.Lock()
	r, err := sess.Call(c.ctx, "docker.logs", raw)
	if err != nil {
		mu.Unlock()
		return err
	}
	var res dockermod.LogsResult
	if err := decodeResult(r, &res); err != nil {
		mu.Unlock()
		return fmt.Errorf("docker.logs: %w", err)
	}
	id = res.ID
	mu.Unlock()
	select {
	case <-c.ctx.Done():
		stop, _ := json.Marshal(dockermod.IDParams{ID: id})
		_, _ = sess.Call(context.WithoutCancel(c.ctx), "docker.logsStop", stop)
		return nil
	case msg := <-done:
		if msg != "" {
			return fmt.Errorf("docker logs: %s", msg)
		}
		return nil
	}
}

func dockerWatch(c *cli) error {
	var mu sync.Mutex
	out := c.env.Stdout
	sess := c.app.NewSession(func(ev api.Event) {
		var res dockermod.ServicesResult
		if json.Unmarshal(ev.Payload, &res) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if c.json {
			b, _ := json.Marshal(res)
			fmt.Fprintf(out, "%s\n", b)
			return
		}
		fmt.Fprintf(out, "--- (%s)\n", res.Refresh)
		printServices(out, res)
	})
	defer sess.Close()
	sess.Subscribe(dockermod.TopicServicesChanged)
	raw, _ := json.Marshal(dockermod.WatchParams{Standalone: c.p.has("all")})
	r, err := sess.Call(c.ctx, "docker.watchStart", raw)
	if err != nil {
		return err
	}
	var w dockermod.WatchResult
	_ = decodeResult(r, &w)
	<-c.ctx.Done()
	stop, _ := json.Marshal(dockermod.IDParams{ID: w.ID})
	_, _ = sess.Call(context.WithoutCancel(c.ctx), "docker.watchStop", stop)
	return nil
}

// decodeResult converts a session call result into out.
func decodeResult(r, out any) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
