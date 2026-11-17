package headless

import (
	"fmt"
	"io"
	"strings"

	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

func init() {
	flagSpec["global"] = false
	flagSpec["clear"] = false
	flagSpec["refresh"] = false
	register(
		&command{name: "docker", usage: "docker [contexts | status [CONTEXT] | use NAME [--global] | use --clear [--global] | path [PATH | --clear]]", summary: "Docker binary and contexts (OrbStack, Docker Desktop, …); status contacts the engine (exit 1 if unreachable)", maxArgs: 2, cwdProject: true, run: runDocker},
		&command{name: "interp-probe", usage: "interp-probe [ID] [--refresh] [--start]", summary: "interpreter info: PHP version, extensions, Xdebug, ini files (cached; --refresh re-reads)", maxArgs: 1, cwdProject: true, run: runInterpProbe},
		&command{name: "interp-status", usage: "interp-status [ID]", summary: "Docker context and service/image state of a compose or image interpreter", maxArgs: 1, cwdProject: true, run: runInterpStatus},
		&command{name: "interp-image", usage: "interp-image [add IMAGE [REMOTE] | remove IMAGE]", summary: "list, add or remove Docker image interpreters (docker run --rm -v project:REMOTE)", maxArgs: 3, cwdProject: true, run: runInterpImage},
	)
}

func runDocker(c *cli) error {
	sub := "contexts"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	arg := ""
	if len(c.p.args) > 1 {
		arg = c.p.args[1]
	}
	switch sub {
	case "contexts":
		if arg != "" {
			return usagef("usage: tiegel --headless %s", commands["docker"].usage)
		}
		var res dockermod.ContextsResult
		if err := c.call("docker.contexts", nil, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "binary %s (%s)\n", res.Binary.Path, res.Binary.Source)
			for _, x := range res.Contexts {
				mark := " "
				if x.Name == res.Effective {
					mark = "*"
				}
				remote := ""
				if x.Remote {
					remote = " remote"
				}
				fmt.Fprintf(w, "%s %s\t%s\t%s%s\n", mark, x.Name, x.Provider, x.Endpoint, remote)
			}
			sel := res.Selected
			if sel == "" {
				sel = "(docker's current: " + res.Effective + ")"
			}
			fmt.Fprintf(w, "selected %s [%s]\n", sel, res.Source)
			if res.DockerHost != "" {
				fmt.Fprintf(w, "DOCKER_HOST=%s\n", res.DockerHost)
			}
		})
	case "status":
		var res dockermod.StatusResult
		if err := c.call("docker.status", dockermod.StatusParams{Context: arg}, &res); err != nil {
			return err
		}
		if err := c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "binary %s (%s)\n", res.Binary.Path, res.Binary.Source)
			fmt.Fprintf(w, "context %s [%s] %s %s\n", res.Context, res.Source, res.Provider, res.Endpoint)
			if res.Version != nil {
				fmt.Fprintf(w, "client %s server %s %s\n", res.Version.Client, res.Version.Server, res.Version.Platform)
			}
			if res.Reachable {
				fmt.Fprintln(w, "reachable")
			} else {
				fmt.Fprintf(w, "unreachable: %s\n", res.Error)
			}
		}); err != nil {
			return err
		}
		if !res.Reachable {
			return &quietError{code: ExitError}
		}
		return nil
	case "use":
		p := dockermod.SetContextParams{Name: arg, Global: c.p.has("global"), Clear: c.p.has("clear")}
		if (p.Name == "") == !p.Clear {
			return usagef("usage: tiegel --headless %s", commands["docker"].usage)
		}
		var res dockermod.SetContextResult
		if err := c.call("docker.setContext", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			sel := res.Selected
			if sel == "" {
				sel = "(docker's current context)"
			}
			fmt.Fprintf(w, "context %s [%s]\n", sel, res.Source)
		})
	case "path":
		var out struct {
			Path   string `json:"path"`
			Source string `json:"source"`
		}
		if arg != "" || c.p.has("clear") {
			if err := c.call("docker.setPath", dockermod.SetPathParams{Path: arg, Clear: c.p.has("clear")}, &out); err != nil {
				return err
			}
		} else {
			var res dockermod.ContextsResult
			if err := c.call("docker.contexts", nil, &res); err != nil {
				return err
			}
			out.Path, out.Source = res.Binary.Path, res.Binary.Source
		}
		return c.emit(out, func(w io.Writer) { fmt.Fprintf(w, "%s\t%s\n", out.Path, out.Source) })
	}
	return usagef("usage: tiegel --headless %s", commands["docker"].usage)
}

func runInterpProbe(c *cli) error {
	p := phpruntime.ProbeParams{Refresh: c.p.has("refresh"), Start: c.p.has("start")}
	if len(c.p.args) > 0 {
		p.ID = c.p.args[0]
	}
	var res phpruntime.ProbeResult
	if err := c.call("interp.probe", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		i := res.Info
		cached := ""
		if res.Cached {
			cached = " (cached " + res.ProbedAt.Local().Format("2006-01-02 15:04") + ", --refresh re-reads)"
		}
		fmt.Fprintf(w, "interpreter %s (%s)%s\n", res.ID, res.Kind, cached)
		fmt.Fprintf(w, "php %s %s %s\n", i.Version, i.SAPI, i.OS)
		if i.Binary != "" {
			fmt.Fprintf(w, "binary %s\n", i.Binary)
		}
		xd := i.Xdebug
		if xd == "" {
			xd = "not loaded"
		}
		fmt.Fprintf(w, "xdebug %s\n", xd)
		ini := i.IniFile
		if ini == "" {
			ini = "(none)"
		}
		fmt.Fprintf(w, "ini %s\n", ini)
		for _, f := range i.IniScanned {
			fmt.Fprintf(w, "ini+ %s\n", f)
		}
		fmt.Fprintf(w, "extensions %s\n", strings.Join(i.Extensions, " "))
		if len(i.ZendExtensions) > 0 {
			fmt.Fprintf(w, "zend %s\n", strings.Join(i.ZendExtensions, " "))
		}
	})
}

func runInterpStatus(c *cli) error {
	var p phpruntime.StatusParams
	if len(c.p.args) > 0 {
		p.ID = c.p.args[0]
	}
	var res phpruntime.StatusResult
	if err := c.call("interp.status", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		ctxName := res.Context
		if ctxName == "" {
			ctxName = "(docker's current)"
		}
		fmt.Fprintf(w, "interpreter %s (%s)\n", res.ID, res.Kind)
		if res.Kind == "compose" || res.Kind == "docker" {
			fmt.Fprintf(w, "context %s\n", ctxName)
		}
		if res.Service != "" {
			fmt.Fprintf(w, "service %s %s\n", res.Service, res.State)
			for _, ct := range res.Containers {
				fmt.Fprintf(w, "container %s %s %s\n", ct.Name, ct.State, ct.Status)
			}
		}
		if res.ImagePresent != nil {
			fmt.Fprintf(w, "image %s present=%v\n", res.Image, *res.ImagePresent)
		}
		if res.Error != "" {
			fmt.Fprintf(w, "error %s\n", res.Error)
		}
	})
}

func runInterpImage(c *cli) error {
	a := c.p.args
	switch {
	case len(a) == 0:
	case a[0] == "add" && (len(a) == 2 || len(a) == 3):
		p := phpruntime.ImageParams{Image: a[1]}
		if len(a) == 3 {
			p.Remote = a[2]
		}
		if err := c.call("interp.addImage", p, nil); err != nil {
			return err
		}
	case a[0] == "remove" && len(a) == 2:
		if err := c.call("interp.removeImage", phpruntime.ImageParams{Image: a[1]}, nil); err != nil {
			return err
		}
	default:
		return usagef("usage: tiegel --headless %s", commands["interp-image"].usage)
	}
	f := false
	var list []phpruntime.InterpreterInfo
	if err := c.call("interp.list", phpruntime.InterpListParams{Compose: &f}, &list); err != nil {
		return err
	}
	images := []phpruntime.InterpreterInfo{}
	for _, i := range list {
		if i.Kind == "docker" {
			images = append(images, i)
		}
	}
	return c.emit(images, func(w io.Writer) {
		if len(images) == 0 {
			fmt.Fprintln(w, "no image interpreters")
		}
		for _, i := range images {
			mark := " "
			if i.Selected {
				mark = "*"
			}
			fmt.Fprintf(w, "%s %s\t%s\t%s\n", mark, i.ID, i.Image, i.Remote)
		}
	})
}
