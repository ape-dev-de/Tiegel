package headless

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["permanent"] = false
	flagSpec["overwrite"] = false
	flagSpec["content"] = true
	flagSpec["kind"] = true
	flagSpec["namespace"] = true
	flagSpec["template"] = true
	register(
		&command{name: "new-file", usage: "new-file FILE [--content TEXT | --template class|abstract|interface|trait|enum|test [--namespace NS] [--dry-run]]", summary: "create a file in the project (parents as needed), optionally from a PHP file template", minArgs: 1, maxArgs: 1, cwdProject: true, run: runNewFile},
		&command{name: "new-dir", usage: "new-dir DIR", summary: "create a directory in the project", minArgs: 1, maxArgs: 1, cwdProject: true, run: runNewDir},
		&command{name: "rename", usage: "rename PATH NEW-NAME|NEW-PATH [--overwrite]", summary: "rename or move a file or directory (open documents and Local History follow)", minArgs: 2, maxArgs: 2, cwdProject: true, run: runRename},
		&command{name: "move", usage: "move PATH... DIR [--overwrite]", summary: "move files or directories into a directory", minArgs: 2, maxArgs: -1, cwdProject: true, run: runTransfer("files.move")},
		&command{name: "copy", usage: "copy PATH... DIR|NEW-PATH [--overwrite]", summary: "copy files or directories", minArgs: 2, maxArgs: -1, cwdProject: true, run: runTransfer("files.copy")},
		&command{name: "delete", usage: "delete PATH... [--permanent]", summary: "move files or directories to the trash (--permanent deletes)", minArgs: 1, maxArgs: -1, cwdProject: true, run: runDelete},
		&command{name: "new-class", usage: "new-class DIR NAME [--kind class|abstract|interface|trait|enum|test] [--namespace NS] [--dry-run]", summary: "create a PHP file from a file template with the PSR-4 namespace from composer.json", minArgs: 2, maxArgs: 2, cwdProject: true, run: runNewClass},
		&command{name: "file-templates", usage: "file-templates [KIND]", summary: "PHP file templates (built-in or user replacement fileTemplates.<kind>); with KIND its text", maxArgs: 1, cwdProject: true, run: runFileTemplates},
	)
}

func printPath(res api.FileOpResult) func(io.Writer) {
	return func(w io.Writer) {
		if res.From != "" {
			fmt.Fprintf(w, "%s -> %s\n", res.From, res.Path)
		} else {
			fmt.Fprintln(w, res.Path)
		}
		for _, d := range res.Docs {
			fmt.Fprintf(w, "  open document now %s\n", d)
		}
	}
}

func runNewFile(c *cli) error {
	if kind := c.p.value("template"); kind != "" {
		if c.p.has("content") {
			return usagef("--content and --template exclude each other")
		}
		file := filepath.ToSlash(c.p.args[0])
		dir, name := path.Split(file)
		return newFromTemplate(c, strings.TrimSuffix(dir, "/"), strings.TrimSuffix(name, ".php"), kind)
	}
	var res api.FileOpResult
	if err := c.call("files.createFile", api.CreateFileParams{Path: c.p.args[0], Content: c.p.value("content")}, &res); err != nil {
		return err
	}
	return c.emit(res, printPath(res))
}

func runNewDir(c *cli) error {
	var res api.FileOpResult
	if err := c.call("files.createDir", api.PathParams{Path: c.p.args[0]}, &res); err != nil {
		return err
	}
	return c.emit(res, printPath(res))
}

func runRename(c *cli) error {
	p := api.RenameParams{Path: c.p.args[0], Overwrite: c.p.has("overwrite")}
	if to := c.p.args[1]; strings.ContainsAny(to, `/\`) {
		p.To = to
	} else {
		p.NewName = to
	}
	var res api.FileOpResult
	if err := c.call("files.rename", p, &res); err != nil {
		return err
	}
	return c.emit(res, printPath(res))
}

func runTransfer(method string) func(c *cli) error {
	return func(c *cli) error {
		n := len(c.p.args)
		var res api.FilesOpResult
		if err := c.call(method, api.TransferParams{Paths: c.p.args[:n-1], To: c.p.args[n-1], Overwrite: c.p.has("overwrite")}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			for _, r := range res.Results {
				printPath(r)(w)
			}
		})
	}
}

func runDelete(c *cli) error {
	var res api.DeleteResult
	if err := c.call("files.delete", api.DeleteParams{Paths: c.p.args, Permanent: c.p.has("permanent")}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		how := "deleted"
		if res.Trashed {
			how = "moved to trash"
		}
		for _, p := range res.Deleted {
			fmt.Fprintf(w, "%s: %s\n", how, p)
		}
		for _, p := range res.Closed {
			fmt.Fprintf(w, "  closed document %s\n", p)
		}
	})
}

func runNewClass(c *cli) error {
	return newFromTemplate(c, c.p.args[0], c.p.args[1], c.p.value("kind"))
}

func newFromTemplate(c *cli, dir, name, kind string) error {
	p := api.PHPClassParams{Dir: dir, Name: name, Kind: kind, DryRun: c.p.has("dry-run")}
	if c.p.has("namespace") {
		ns := c.p.value("namespace")
		p.Namespace = &ns
	}
	var res api.PHPClassResult
	if err := c.call("files.createPhpClass", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		fmt.Fprintf(w, "%s\t%s %s\n", res.Path, res.Kind, res.Class)
		if p.DryRun {
			fmt.Fprint(w, res.Content)
		}
	})
}

func runFileTemplates(c *cli) error {
	var list []api.FileTemplateInfo
	if err := c.call("files.templates", nil, &list); err != nil {
		return err
	}
	if len(c.p.args) == 1 {
		for _, t := range list {
			if t.Kind == c.p.args[0] {
				return c.emit(t, func(w io.Writer) { fmt.Fprint(w, t.Template) })
			}
		}
		return usagef("unknown file template %q (one of %s)", c.p.args[0], strings.Join(api.FileTemplateKinds(), ", "))
	}
	return c.emit(list, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, t := range list {
			src := "built-in"
			if t.Custom {
				src = "user (" + t.Setting + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Kind, t.Label, src)
		}
		_ = tw.Flush()
	})
}
