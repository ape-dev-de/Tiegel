// Command tiegel-catalog shows the Tiegel UI component catalog: every
// design token, atom and molecule with its variants, in light and dark mode.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"
)

func main() {
	light := flag.Bool("light", false, "start in light mode")
	page := flag.String("page", "", "name of the page to open (e.g. Button)")
	flag.Parse()

	go func() {
		w := new(app.Window)
		w.Option(app.Title("Tiegel – Component Catalog"), app.Size(unit.Dp(1180), unit.Dp(780)))
		if err := run(w, *light, *page); err != nil {
			log.Println(err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

func run(w *app.Window, light bool, page string) error {
	inv := w.Invalidate
	redraw.Store(&inv)
	c := newCatalog(light)
	if page != "" && !c.selectPage(page) {
		return fmt.Errorf("unknown page %q", page)
	}
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			c.Layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}
