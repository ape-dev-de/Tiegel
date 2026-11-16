package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
)

func init() {
	for name, takes := range map[string]bool{
		"prune": false, "branch": true, "strategy": true, "autostash": false,
		"untracked": false, "keep-index": false, "restore-index": false, "tags": false,
	} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "git-fetch", usage: "git-fetch [--remote R] [--prune] [--tags]", summary: "fetch all remotes or one (network access)", maxArgs: 0, cwdProject: true, run: runGitFetch},
		&command{name: "git-pull", usage: "git-pull [--remote R] [--branch B] [--strategy merge|rebase|ff-only] [--autostash] [--prune]", summary: "fetch and integrate into the current branch (exit 1 on conflicts)", maxArgs: 0, cwdProject: true, run: runGitPull},
		&command{name: "git-update", usage: "git-update [--strategy merge|rebase|ff-only] [--autostash] [--prune]", summary: "update project: save, fetch upstream, integrate (exit 1 on conflicts)", maxArgs: 0, cwdProject: true, run: runGitUpdate},
		&command{name: "git-stash", usage: "git-stash [list | push [-m MSG] [--untracked] [--keep-index] [FILE...] | apply [N] [--restore-index] | pop [N] [--restore-index] | drop [N]]", summary: "stash local changes and manage stash entries", maxArgs: -1, cwdProject: true, run: runGitStash},
	)
}

func runGitFetch(c *cli) error {
	var res gitmod.FetchResult
	if err := c.call("git.fetch", gitmod.FetchParams{Remote: c.p.value("remote"), Prune: c.p.has("prune"), Tags: c.p.has("tags")}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, u := range res.Updated {
			switch {
			case u.Old == "":
				fmt.Fprintf(w, "new      %s %s\n", u.Ref, shortHash(u.New))
			case u.New == "":
				fmt.Fprintf(w, "pruned   %s\n", u.Ref)
			default:
				fmt.Fprintf(w, "updated  %s %s..%s\n", u.Ref, shortHash(u.Old), shortHash(u.New))
			}
		}
		if len(res.Updated) == 0 {
			fmt.Fprintf(w, "fetched %s: up to date\n", strings.Join(res.Remotes, ", "))
		}
	})
}

func runGitPull(c *cli) error {
	var res gitmod.PullResult
	p := gitmod.PullParams{Remote: c.p.value("remote"), Branch: c.p.value("branch"), Strategy: c.p.value("strategy"), Autostash: c.p.has("autostash"), Prune: c.p.has("prune")}
	if err := c.call("git.pull", p, &res); err != nil {
		return err
	}
	return c.pullOutput(res)
}

func runGitUpdate(c *cli) error {
	var res gitmod.PullResult
	p := gitmod.UpdateParams{Strategy: c.p.value("strategy"), Autostash: c.p.has("autostash"), Prune: c.p.has("prune")}
	if err := c.call("git.updateProject", p, &res); err != nil {
		return err
	}
	return c.pullOutput(res)
}

func (c *cli) pullOutput(res gitmod.PullResult) error {
	if err := c.emit(res, func(w io.Writer) {
		switch {
		case res.UpToDate:
			fmt.Fprintf(w, "%s is up to date with %s/%s\n", res.Branch, res.Remote, res.RemoteBranch)
		case len(res.Conflicts) > 0:
			fmt.Fprintf(w, "conflicts while integrating %s/%s (%s):\n", res.Remote, res.RemoteBranch, res.State)
			for _, f := range res.Conflicts {
				fmt.Fprintf(w, "U %s\n", f)
			}
		default:
			how := "merged"
			if res.FastForward {
				how = "fast-forwarded"
			}
			fmt.Fprintf(w, "%s %s to %s (%d files changed)\n", how, res.Branch, shortHash(res.After), len(res.Changed))
			for _, f := range res.Changed {
				fmt.Fprintf(w, "  %s\n", f)
			}
		}
	}); err != nil {
		return err
	}
	if len(res.Conflicts) > 0 {
		return &quietError{code: ExitError}
	}
	return nil
}

func runGitStash(c *cli) error {
	sub, args := "list", c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	index := func() (int, error) {
		if len(args) == 0 {
			return 0, nil
		}
		if len(args) > 1 {
			return 0, usagef("usage: tiegel --headless %s", commands["git-stash"].usage)
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(args[0], "stash@{"), "}"))
		if err != nil || n < 0 {
			return 0, usagef("stash index must be a number, got %q", args[0])
		}
		return n, nil
	}
	switch sub {
	case "list":
		if len(args) > 0 {
			return usagef("usage: tiegel --headless %s", commands["git-stash"].usage)
		}
		var list []gitmod.StashEntry
		if err := c.call("git.stashList", nil, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) { printStash(w, list) })
	case "push":
		msg := c.p.value("message")
		if msg == "" {
			msg = c.p.value("m")
		}
		var res gitmod.StashResult
		p := gitmod.StashParams{Message: msg, IncludeUntracked: c.p.has("untracked"), KeepIndex: c.p.has("keep-index"), Paths: args}
		if err := c.call("git.stash", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if !res.Created || res.Entry == nil {
				fmt.Fprintln(w, "no local changes to stash")
				return
			}
			printStash(w, []gitmod.StashEntry{*res.Entry})
		})
	case "apply", "pop":
		n, err := index()
		if err != nil {
			return err
		}
		var res gitmod.StashApplyResult
		if err := c.call("git.stash"+strings.ToUpper(sub[:1])+sub[1:], gitmod.StashRefParams{Index: n, RestoreIndex: c.p.has("restore-index")}, &res); err != nil {
			return err
		}
		if err := c.emit(res, func(w io.Writer) {
			for _, f := range res.Conflicts {
				fmt.Fprintf(w, "U %s\n", f)
			}
			switch {
			case len(res.Conflicts) > 0:
				fmt.Fprintf(w, "stash@{%d} applied with conflicts (entry kept)\n", n)
			case res.Dropped:
				fmt.Fprintf(w, "stash@{%d} applied and dropped\n", n)
			default:
				fmt.Fprintf(w, "stash@{%d} applied\n", n)
			}
		}); err != nil {
			return err
		}
		if len(res.Conflicts) > 0 {
			return &quietError{code: ExitError}
		}
		return nil
	case "drop":
		n, err := index()
		if err != nil {
			return err
		}
		var list []gitmod.StashEntry
		if err := c.call("git.stashDrop", gitmod.StashRefParams{Index: n}, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) { printStash(w, list) })
	}
	return usagef("usage: tiegel --headless %s", commands["git-stash"].usage)
}

func printStash(w io.Writer, list []gitmod.StashEntry) {
	for _, e := range list {
		fmt.Fprintf(w, "%s %s %s\n", e.Ref, shortHash(e.Hash), e.Message)
	}
}
