package git

import (
	"errors"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
)

// Problem is a user facing error or notice shown by an organism.
type Problem struct {
	// Warning shows the problem as a warning instead of an error.
	Warning bool
	Title   string
	Detail  string
}

func (p *Problem) banner() molecules.Banner {
	k := molecules.BannerError
	if p.Warning {
		k = molecules.BannerWarning
	}
	return molecules.Banner{Kind: k, Title: p.Title, Detail: p.Detail}
}

// PushErrorTitle returns an understandable explanation of a push failure
// kind.
func PushErrorTitle(k gitcli.PushErrorKind) string {
	switch k {
	case gitcli.PushRejected:
		return "Push rejected: the remote branch has new commits. Pull (or rebase) first, then push again."
	case gitcli.PushRemoteRejected:
		return "The remote declined the push (a server hook or branch protection rule)."
	case gitcli.PushAuthFailed:
		return "Authentication failed: check your credentials or SSH key and your access rights."
	case gitcli.PushNetwork:
		return "The remote host could not be reached. Check your network connection."
	case gitcli.PushRepoNotFound:
		return "The remote repository was not found. Check the remote URL."
	}
	return "Push failed."
}

// ProblemFromError turns an error of "Commit" or "Commit and Push" into a
// Problem. It understands gitcli's sentinel errors and *gitcli.PushError
// (worded for the push step after a successful commit) as well as a
// vetoing commit.before guard (*hooks.AbortError); other errors are shown
// with their message. A nil error yields nil. Errors that arrive through
// internal/api as plain data can be shown with a literal Problem.
func ProblemFromError(err error) *Problem {
	if err == nil {
		return nil
	}
	var pe *gitcli.PushError
	var ab *hooks.AbortError
	switch {
	case errors.As(err, &pe):
		detail := pe.Message
		if s := strings.TrimSpace(pe.Stderr); s != "" && !strings.Contains(detail, s) {
			detail = strings.TrimSpace(detail + "\n" + s)
		}
		return &Problem{Title: "Committed, but the push failed. " + PushErrorTitle(pe.Kind), Detail: detail}
	case errors.As(err, &ab):
		who := ab.Owner
		if who == "" {
			who = "a hook"
		}
		detail := ""
		if ab.Err != nil {
			detail = ab.Err.Error()
		}
		return &Problem{Title: "Commit blocked by " + who + " (" + ab.Hook + ").", Detail: detail}
	case errors.Is(err, gitcli.ErrEmptyMessage):
		return &Problem{Warning: true, Title: "Enter a commit message."}
	case errors.Is(err, gitcli.ErrEmptySelection):
		return &Problem{Warning: true, Title: "Nothing selected to commit."}
	case errors.Is(err, gitcli.ErrNoRemote):
		return &Problem{Title: "Committed, but there is no remote to push to."}
	case errors.Is(err, gitcli.ErrDetachedHead):
		return &Problem{Title: "Cannot push: HEAD is detached (no branch checked out)."}
	}
	return &Problem{Title: "Git reported an error.", Detail: err.Error()}
}
