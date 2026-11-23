package main

import (
	"fmt"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

func progressBarPage() *page {
	return &page{
		Level: "Atoms", Name: "ProgressBar", Glyph: atoms.GlyphRefresh,
		Summary: "Thin progress bar: a fraction fills in the accent color, a negative value sweeps (indeterminate), Danger marks a failure.",
		Variants: func(th *tokens.Theme) []variant {
			bar := func(p atoms.ProgressBar) layout.Widget { return fixedWidth(240, p.Fn(th)) }
			return []variant{
				{"0 %, 35 %, 100 %", flow(th.Space.L, bar(atoms.ProgressBar{Value: 0, Width: 160}), bar(atoms.ProgressBar{Value: 0.35, Width: 160}), bar(atoms.ProgressBar{Value: 1, Width: 160}))},
				{"Indeterminate", bar(atoms.ProgressBar{Value: -1, Width: 160})},
				{"Failed (Danger), 8dp high", bar(atoms.ProgressBar{Value: 0.6, Width: 160, Height: 8, Danger: true})},
			}
		},
	}
}

func backgroundTasksPage() *page {
	ind := &statusbar.TaskIndicator{Task: statusbar.Task{ID: "t1", Title: "Indexing PHP files", Value: 0.42}, More: 2,
		Tooltip: "3 background tasks – click to show them"}
	single := &statusbar.TaskIndicator{Task: statusbar.Task{ID: "t2", Title: "Pushing main", Value: -1}}
	failed := &statusbar.TaskIndicator{Task: statusbar.Task{ID: "t3", Title: "Pulling", Value: -1, Error: "rejected"}}
	list := &statusbar.TaskList{Empty: "No background tasks", Tasks: []statusbar.Task{
		{ID: "t1", Title: "Indexing PHP files", Module: "php-index", Value: 0.42, Text: "vendor/symfony/console/Application.php", Cancellable: true},
		{ID: "t2", Title: "Pushing main", Module: "git", Value: -1, Cancellable: true},
		{ID: "t4", Title: "Starting Phpactor", Module: "lang-php", Value: -1},
		{ID: "t3", Title: "Pulling", Module: "git", Value: -1, Error: "Your local changes would be overwritten by merge"},
	}}
	clicks, cancels := 0, []string{}
	return &page{
		Level: "Organisms", Name: "Background tasks", Glyph: atoms.GlyphRefresh,
		Summary: "Status bar entry of the background tasks (latest task, progress, “+N”) and the task list of its popup with cancel/dismiss buttons.",
		Variants: func(th *tokens.Theme) []variant {
			indicator := func(t *statusbar.TaskIndicator) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					if t.Clicked(gtx) {
						clicks++
					}
					return t.Layout(gtx, th)
				}
			}
			return []variant{
				{"Status bar: several tasks", indicator(ind)},
				{"Status bar: one indeterminate task", indicator(single)},
				{"Status bar: failed task", indicator(failed)},
				{"Task list (popup body)", fixedWidth(460, func(gtx layout.Context) layout.Dimensions {
					cancels = append(cancels, list.Update(gtx)...)
					return list.Layout(gtx, th)
				})},
				{"Events", atoms.Muted(fmt.Sprintf("Indicator clicked %d times, cancel/dismiss: %v", clicks, cancels)).Fn(th)},
			}
		},
	}
}
