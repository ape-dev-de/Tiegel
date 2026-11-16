package main

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	gitui "github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// gitPages are the pages for the components added with the Git UI
// (AP6.3–6.5): molecules used by the Git organisms and the organisms
// themselves (surfacePage is listed with the atoms). The sample data comes
// from gitsample, i.e. from the gitcli parsers run on embedded fixtures.
func gitPages() []*page {
	return []*page{
		codeLinePage(), listRowPage(), bannerPage(), scrollListPage(), splitPage(), dropdownPage(), contextMenuPage(),
		commitPanelPage(), diffViewPage(), logViewPage(), branchListPage(), mergeViewPage(),
	}
}

// fixedSize gives w an exact size: the available width (at most maxW dp,
// 0 = unlimited) and h dp tall. Organisms fill their constraints, while
// the catalog's preview list is unbounded vertically.
func fixedSize(maxW, h unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		x := gtx.Constraints.Max.X
		if maxW > 0 {
			x = min(x, gtx.Dp(maxW))
		}
		gtx.Constraints = layout.Exact(image.Pt(x, gtx.Dp(h)))
		return w(gtx)
	}
}

// eventLog remembers the last events of an organism for display.
type eventLog struct{ last []string }

func (l *eventLog) add(ev []gitui.Event) {
	for _, e := range ev {
		s := fmt.Sprintf("%T %+v", e, e)
		s = strings.TrimPrefix(s, "git.")
		if len(s) > 160 {
			s = s[:160] + "…"
		}
		l.last = append(l.last, s)
	}
	if n := len(l.last); n > 4 {
		l.last = l.last[n-4:]
	}
}

func (l *eventLog) widget(th *tokens.Theme) layout.Widget {
	if len(l.last) == 0 {
		return atoms.Muted("No events yet – interact with the component above.").Fn(th)
	}
	ws := make([]layout.Widget, len(l.last))
	for i, s := range l.last {
		ws[i] = atoms.Label{Text: s, Role: atoms.RoleCode, MaxLines: 1}.Fn(th)
	}
	return column(th.Space.XS, ws...)
}

// ---- Atoms / molecules -----------------------------------------------------

func surfacePage() *page {
	return &page{
		Level: "Atoms", Name: "Surface", Glyph: atoms.GlyphFile,
		Summary: "Themed background behind arbitrary content (panel, alt, window, editor), optional border and rounding.",
		Variants: func(th *tokens.Theme) []variant {
			ws := make([]layout.Widget, 0, len(atoms.SurfaceKinds))
			for _, k := range atoms.SurfaceKinds {
				ws = append(ws, func(gtx layout.Context) layout.Dimensions {
					return atoms.Surface{Kind: k, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Text(k.String()).Fn(th))
					})
				})
			}
			return []variant{{"Kinds", flow(th.Space.S, ws...)}}
		},
	}
}

func codeLinePage() *page {
	return &page{
		Level: "Molecules", Name: "CodeLine", Glyph: atoms.GlyphFileCode,
		Summary: "One line of a diff or merge view: line number columns, marker, tone background, emphasized word spans.",
		Variants: func(th *tokens.Theme) []variant {
			txt := "        private readonly UserRepository $users,"
			ws := []layout.Widget{
				molecules.CodeLine{No: 14, No2: 14, Columns: 2, Digits: 3, Marker: " ", Text: "    public function __construct(", Tone: molecules.ToneContext}.Fn(th),
				molecules.CodeLine{No: 15, Columns: 2, Digits: 3, Marker: "-", Text: "        private UserRepository $users,", Tone: molecules.ToneRemoved}.Fn(th),
				molecules.CodeLine{No2: 15, Columns: 2, Digits: 3, Marker: "+", Text: txt, Tone: molecules.ToneAdded,
					Spans: []molecules.Span{{Start: 16, End: 24}}}.Fn(th),
				molecules.CodeLine{Columns: 1, Digits: 3, Tone: molecules.ToneFiller}.Fn(th),
				molecules.CodeLine{No: 7, Columns: 1, Digits: 3, Text: "    private const REDUCED_VAT = 0.07;", Tone: molecules.ToneConflict}.Fn(th),
				molecules.CodeLine{No: 8, Columns: 1, Digits: 3, Text: "declare(strict_types=1);", Tone: molecules.ToneChanged}.Fn(th),
				molecules.CodeLine{Text: "@@ -12,14 +12,16 @@ final class UserController", Tone: molecules.ToneHeader, Muted: true}.Fn(th),
			}
			return []variant{{"Tones: context, removed, added with word span, filler, conflict, changed, header", column(0, ws...)}}
		},
	}
}

func listRowPage() *page {
	rows := make([]molecules.ListRow, 4)
	rows[1].Selected = true
	names := []string{"feature/invoices", "main", "bugfix/rounding", "origin/main"}
	return &page{
		Level: "Molecules", Name: "ListRow", Glyph: atoms.GlyphBranch,
		Summary: "Clickable, selectable row wrapping arbitrary content (logs, branch lists, change lists).",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{{"Rows (click to select)", fixedWidth(420, func(gtx layout.Context) layout.Dimensions {
				ws := make([]layout.Widget, len(rows))
				for i := range rows {
					if c, _ := rows[i].Update(gtx); c {
						for k := range rows {
							rows[k].Selected = k == i
						}
					}
					ws[i] = func(gtx layout.Context) layout.Dimensions {
						return rows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(atoms.Icon{Glyph: atoms.GlyphBranch, Color: th.Color.TextMuted}.Fn(th)),
								layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
								layout.Rigid(atoms.Text(names[i]).Fn(th)))
						})
					}
				}
				return column(0, ws...)(gtx)
			})}}
		},
	}
}

func bannerPage() *page {
	return &page{
		Level: "Molecules", Name: "Banner", Glyph: atoms.GlyphWarning,
		Summary: "Inline message with glyph, title and detail – errors and notices inside panels.",
		Variants: func(th *tokens.Theme) []variant {
			var vs []variant
			for _, k := range molecules.BannerKinds {
				vs = append(vs, variant{k.String(), fixedWidth(520, molecules.Banner{Kind: k,
					Title: "Push rejected: the remote branch has new commits.", Detail: "Pull (or rebase) first, then push again."}.Fn(th))})
			}
			return vs
		},
	}
}

func scrollListPage() *page {
	var list molecules.ScrollList
	return &page{
		Level: "Molecules", Name: "ScrollList", Glyph: atoms.GlyphFileText,
		Summary: "Virtualized vertical list with overlay scrollbar; only visible rows are laid out.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{{"100 000 rows", fixedSize(520, 220, func(gtx layout.Context) layout.Dimensions {
				return list.Layout(gtx, th, 100_000, func(gtx layout.Context, i int) layout.Dimensions {
					return molecules.CodeLine{No: i + 1, Columns: 1, Digits: 6, Text: fmt.Sprintf("// generated line %d", i+1)}.Layout(gtx, th)
				})
			})}}
		},
	}
}

func splitPage() *page {
	h := &molecules.Split{Ratio: 0.35}
	v := &molecules.Split{Vertical: true}
	return &page{
		Level: "Molecules", Name: "Split", Glyph: atoms.GlyphSettings,
		Summary: "Two panes with a draggable divider, side by side or stacked.",
		Variants: func(th *tokens.Theme) []variant {
			p := func(s string, k atoms.SurfaceKind) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					return atoms.Surface{Kind: k}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted(s).Fn(th))
					})
				}
			}
			return []variant{
				{"Horizontal (drag the divider)", fixedSize(0, 160, func(gtx layout.Context) layout.Dimensions {
					return h.Layout(gtx, th, p("Files", atoms.SurfacePanel), p("Diff preview", atoms.SurfaceEditor))
				})},
				{"Vertical", fixedSize(520, 200, func(gtx layout.Context) layout.Dimensions {
					return v.Layout(gtx, th, p("File list", atoms.SurfacePanel), p("Commit message", atoms.SurfaceAlt))
				})},
			}
		},
	}
}

func dropdownPage() *page {
	d := &molecules.Dropdown{Glyph: atoms.GlyphHistory, Description: "Recent messages",
		Items: []string{"Add invoice aggregate and numbering", "Send welcome mail after registration\n\nAlso logs the envelope.", "Fix rounding of gross prices"}}
	sel := molecules.NewSelect("Choose a PHP interpreter",
		molecules.DropdownOption{Label: "php 8.3 (Homebrew)", Detail: "/opt/homebrew/bin/php", Glyph: atoms.GlyphComputer, Badge: "8.3.4", BadgeKind: atoms.BadgeInfo},
		molecules.DropdownOption{Label: "compose:php", Detail: "php:8.3-fpm", Glyph: atoms.GlyphContainer, Badge: "score 115", BadgeKind: atoms.BadgeAccent},
		molecules.DropdownOption{Label: "compose:worker", Detail: "shop/worker", Glyph: atoms.GlyphContainer, Badge: "score 40"},
	)
	disabled := molecules.NewSelect("Disabled")
	disabled.Disabled = true
	chosen := ""
	return &page{
		Level: "Molecules", Name: "Dropdown", Glyph: atoms.GlyphChevronDown,
		Summary: "Popup list drawn above everything else; click outside to close. Menu button (Items) or select field (Options, NewSelect).",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{{"Message history", fixedSize(0, 140, func(gtx layout.Context) layout.Dimensions {
				if i, ok := d.Update(gtx); ok {
					chosen = d.Items[i]
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Flexed(1, atoms.Muted("Chosen: "+firstLineOf(chosen)).SingleLine().Fn(th)))
			})}, {"Select field", fixedSize(380, 120, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = 0
				return sel.Layout(gtx, th)
			})}, {"Select field, disabled", fixedSize(380, 28, func(gtx layout.Context) layout.Dimensions {
				return disabled.Layout(gtx, th)
			})}}
		},
	}
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// ---- Organisms -------------------------------------------------------------

func toChanges(in []gitsample.Change) []gitui.Change {
	out := make([]gitui.Change, len(in))
	for i, c := range in {
		out[i] = gitui.Change{Status: c.Status, Diff: c.Diff}
	}
	return out
}

func commitPanelPage() *page {
	panel := gitui.NewCommitPanel()
	sample, _ := gitsample.Changes()
	panel.SetChanges(toChanges(sample))
	panel.SetHistory([]string{
		"Add invoice aggregate and numbering",
		"Send welcome mail after registration",
		"Fix rounding of gross prices",
	})
	var log eventLog
	return &page{
		Level: "Organisms", Name: "CommitPanel", Glyph: atoms.GlyphCommit,
		Summary: "Commit tool window: grouped changes with checkboxes per file, hunk and line, diff preview, message history, Amend, Commit / Commit and Push (Cmd/Ctrl+Enter).",
		Variants: func(th *tokens.Theme) []variant {
			problems := []layout.Widget{}
			for _, err := range []error{
				&gitcli.PushError{Kind: gitcli.PushRejected, Message: "Updates were rejected because the remote contains work that you do not have locally."},
				&gitcli.PushError{Kind: gitcli.PushAuthFailed, Message: "Permission denied (publickey)."},
				gitcli.ErrEmptyMessage,
			} {
				p := gitui.ProblemFromError(err)
				problems = append(problems, fixedWidth(640, molecules.Banner{Kind: bannerKind(p), Title: p.Title, Detail: p.Detail}.Fn(th)))
			}
			return []variant{
				{"Sample working tree (untracked diffs load on selection)", fixedSize(0, 560, func(gtx layout.Context) layout.Dimensions {
					ev := panel.Update(gtx)
					log.add(ev)
					for _, e := range ev {
						switch e := e.(type) {
						case gitui.DiffRequested:
							if d, err := gitsample.UntrackedDiff(); err == nil && d.Path() == e.Path {
								panel.SetDiff(e.Path, d)
							} else {
								panel.SetDiff(e.Path, gitui.WholeFileDiff("", e.Path, nil, []byte("# Invoices\n\nHow invoices are numbered and sent.\n")))
							}
						case gitui.AmendMessageRequested:
							panel.SetAmendMessage("Add invoice aggregate and numbering")
						case gitui.RefreshRequested:
							s, _ := gitsample.Changes()
							panel.SetChanges(toChanges(s))
						case gitui.CommitRequested:
							panel.SetProblem(&gitui.Problem{Warning: true,
								Title: fmt.Sprintf("Catalog demo: would commit %d file(s)%s.", len(e.Files), map[bool]string{true: " and push", false: ""}[e.Push])})
						}
					}
					return panel.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
				{"Error texts (ProblemFromError)", column(th.Space.S, problems...)},
			}
		},
	}
}

func bannerKind(p *gitui.Problem) molecules.BannerKind {
	if p.Warning {
		return molecules.BannerWarning
	}
	return molecules.BannerError
}

func diffViewPage() *page {
	sample, _ := gitsample.Changes()
	unified := gitui.NewDiffView()
	for _, c := range sample {
		if c.Status.Path == "src/Http/Controller/UserController.php" {
			unified.SetDiff(c.Diff)
		}
	}
	base, ours, _ := gitsample.MergeVersions()
	whole := gitui.NewDiffView()
	whole.Mode = gitui.SideBySide
	whole.SetDiff(gitui.WholeFileDiff("src/Billing/PriceCalculator.php", "src/Billing/PriceCalculator.php", base, ours))
	large := gitui.NewDiffView()
	if d, err := gitsample.LargeDiff(10_000); err == nil {
		large.SetDiff(d)
	}
	return &page{
		Level: "Organisms", Name: "DiffView", Glyph: atoms.GlyphBranch,
		Summary: "Virtualized diff of one file: unified or side by side, line numbers, change navigation, word highlighting.",
		Variants: func(th *tokens.Theme) []variant {
			w := func(v *gitui.DiffView, h unit.Dp) layout.Widget {
				return fixedSize(0, h, func(gtx layout.Context) layout.Dimensions { return v.Layout(gtx, th) })
			}
			return []variant{
				{"Unified with word highlighting", w(unified, 360)},
				{"Whole file side by side (WholeFileDiff)", w(whole, 420)},
				{"10 000 line diff (virtualized)", w(large, 300)},
			}
		},
	}
}

func logViewPage() *page {
	const total = 500
	v := gitui.NewLogView()
	v.SetCommits(gitsample.Commits(0, 60), true)
	var log eventLog
	return &page{
		Level: "Organisms", Name: "LogView", Glyph: atoms.GlyphHistory,
		Summary: "Virtualized commit log with ref badges; selection and paging are reported as events.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{fmt.Sprintf("Log (pages of 60, %d commits; scroll to the end to load more)", total), fixedSize(0, 380, func(gtx layout.Context) layout.Dimensions {
					ev := v.Update(gtx)
					log.add(ev)
					for _, e := range ev {
						if p, ok := e.(gitui.LogPageRequested); ok {
							n := min(60, total-p.Loaded)
							v.AppendCommits(gitsample.Commits(p.Loaded, n), p.Loaded+n < total)
						}
					}
					return v.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
			}
		},
	}
}

func branchListPage() *page {
	b := gitui.NewBranchList()
	b.SetBranches(gitsample.Branches())
	var log eventLog
	return &page{
		Level: "Organisms", Name: "BranchList", Glyph: atoms.GlyphBranch,
		Summary: "Local and remote branches with search; Checkout (double click), New branch and Delete as events.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Branches popup", fixedSize(380, 440, func(gtx layout.Context) layout.Dimensions {
					log.add(b.Update(gtx))
					return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return b.Layout(gtx, th)
					})
				})},
				{"Events", log.widget(th)},
			}
		},
	}
}

func mergeViewPage() *page {
	v := gitui.NewMergeView()
	v.SetMerge("src/Billing/PriceCalculator.php", gitsample.Merge(), gitsample.MergeLabels)
	var log eventLog
	return &page{
		Level: "Organisms", Name: "MergeView", Glyph: atoms.GlyphBranch,
		Summary: "Three-way merge ours | result | theirs: take ours/theirs/both, edit, reset; apply when all conflicts are resolved.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"src/Billing/PriceCalculator.php (gitcli.Merge3 on embedded versions)", fixedSize(0, 560, func(gtx layout.Context) layout.Dimensions {
					ev := v.Update(gtx)
					log.add(ev)
					for _, e := range ev {
						if _, ok := e.(gitui.MergeCancelled); ok {
							v.SetMerge("src/Billing/PriceCalculator.php", gitsample.Merge(), gitsample.MergeLabels)
						}
					}
					return v.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
			}
		},
	}
}
