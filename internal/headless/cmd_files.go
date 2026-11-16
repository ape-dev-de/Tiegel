package headless

import (
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	flagSpec["permanent"] = false
	flagSpec["overwrite"] = false
	flagSpec["content"] = true
	flagSpec["kind"] = true
	flagSpec["namespace"] = true
	register(
		&command{name: "new-file", usage: "new-file FILE [--content TEXT]", summary: "create a file in the project (parents as needed)", minArgs: 1, maxArgs: 1, cwdProject: true, run: runNewFile},
		&command{name: "new-dir", usage: "new-dir DIR", summary: "create a directory in the project", minArgs: 1, maxArgs: 1, cwdProject: true, run: runNewDir},
		&command{name: "rename", usage: "rename PATH NEW-NAME|NEW-PATH [--overwrite]", summary: "rename or move a file or directory (open documents and Local History follow)", minArgs: 2, maxArgs: 2, cwdProject: true, run: runRename},
		&command{name: "move", usage: "move PATH... DIR [--overwrite]", summary: "move files or directories into a directory", minArgs: 2, maxArgs: -1, cwdProject: true, run: runTransfer("files.move")},
		&command{name: "copy", usage: "copy PATH... DIR|NEW-PATH [--overwrite]", summary: "copy files or directories", minArgs: 2, maxArgs: -1, cwdProject: true, run: runTransfer("files.copy")},
		&command{name: "delete", usage: "delete PATH... [--permanent]", summary: "move files or directories to the trash (--permanent deletes)", minArgs: 1, maxArgs: -1, cwdProject: true, run: runDelete},
		&command{name: "new-class", usage: "new-class DIR NAME [--kind class|abstract|interface|trait|enum] [--namespace NS]", summary: "create a PHP class file with the PSR-4 namespace from composer.json", minArgs: 2, maxArgs: 2, cwdProject: true, run: runNewClass},
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
	p := api.PHPClassParams{Dir: c.p.args[0], Name: c.p.args[1], Kind: c.p.value("kind")}
	if c.p.has("namespace") {
		ns := c.p.value("namespace")
		p.Namespace = &ns
	}
	var res api.PHPClassResult
	if err := c.call("files.createPhpClass", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) { fmt.Fprintf(w, "%s\t%s %s\n", res.Path, res.Kind, res.Class) })
}
