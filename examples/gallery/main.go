// Gallery tours MyGo's own user interface toolkit: a window drawn on the
// GPU from Go, without a web page. It shows layout, the widgets, text
// editing, a list of ten thousand rows with context menus, custom drawing,
// overlays, file drops and updates from other goroutines.
//
//	go run ./examples/gallery
package main

import (
	"fmt"
	"log"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type gallery struct {
	win  *mygo.Window
	page string

	count   int
	agree   bool
	notify  bool
	size    string
	plan    string
	volume  float64
	name    string
	email   string
	bio     string
	filter  string
	picked  int
	starred map[int]bool
	dialog  bool
	menu    bool
	files   []string
	now     time.Time
	samples []float64
}

var pages = []string{"Overview", "Controls", "Text", "List", "Drawing", "Overlays"}

func (g *gallery) view(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		g.sidebar(c)
		ui.Scroll(c).Grow(1).Padding(28, 32).Gap(18).Children(func() {
			ui.Text(c, g.page).FontSize(26).Bold()
			switch g.page {
			case "Overview":
				g.overview(c)
			case "Controls":
				g.controls(c)
			case "Text":
				g.text(c)
			case "List":
				g.list(c)
			case "Drawing":
				g.drawing(c)
			case "Overlays":
				g.overlays(c)
			}
		})
	})
	// Ctrl+1…6 (Cmd on macOS) switch pages.
	for i, p := range pages {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			g.page = p
		}
	}
}

func (g *gallery) sidebar(c *ui.Context) {
	t := c.Theme()
	side := ui.Column(c).Width(200).Padding(16, 10).Gap(2).Background(t.Surface).Shrink(0)
	side.Children(func() {
		ui.Text(c, "MyGo UI").FontSize(13).Bold().TextColor(t.TextMuted).Padding(4, 10, 10)
		for _, p := range pages {
			item := ui.Row(c).Key(p).Padding(7, 10).Radius(6).Focusable()
			if p == g.page {
				item.Background(t.Accent).TextColor(t.AccentText)
			} else if item.Hovered() {
				item.Background(t.SurfaceHover)
			}
			if item.Clicked() {
				g.page = p
			}
			item.Children(func() { ui.Text(c, p) })
		}
		ui.Spacer(c)
		ui.Text(c, g.now.Format("15:04:05")).FontSize(12).TextColor(t.TextMuted).Padding(0, 10)
	})
}

func card(c *ui.Context, title string, body func()) {
	t := c.Theme()
	ui.Column(c).Padding(18).Gap(12).Radius(10).Background(t.Background).Border(1, t.Border).
		Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.06)).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(15).Bold()
		}
		body()
	})
}

func (g *gallery) overview(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Everything here is laid out with flexbox and drawn by MyGo itself: no HTML, no JavaScript, no cgo. "+
		"The view is a Go function of the app's state that runs again after every event.").TextColor(t.TextMuted)
	ui.Row(c).Gap(16).Wrap().AlignItems(ui.Start).Children(func() {
		card(c, "Counter", func() {
			ui.Text(c, fmt.Sprint(g.count)).FontSize(40).Bold()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "−").Width(44).Clicked() {
					g.count--
				}
				if ui.PrimaryButton(c, "Increment").Clicked() {
					g.count++
				}
			})
		})
		card(c, "Live data", func() {
			ui.Text(c, "A goroutine pushes a sample every 200 ms with Window.Update.").TextColor(t.TextMuted).MaxWidth(260)
			g.sparkline(c).Size(260, 80)
		})
		card(c, "Files", func() {
			zone := ui.Column(c).Size(260, 80).Padding(8, 12).Gap(2).Radius(6).Background(t.Surface).
				Border(1, t.Border).Justify(ui.Center).AlignItems(ui.Center)
			if files := zone.DroppedFiles(); files != nil {
				g.files = files
			}
			if zone.FileDragOver() {
				zone.Border(2, t.Accent)
			}
			zone.Children(func() {
				if len(g.files) == 0 {
					ui.Text(c, "Drop files here").TextColor(t.TextMuted)
				}
				for i, f := range g.files {
					if i == 3 {
						ui.Text(c, fmt.Sprintf("and %d more", len(g.files)-i)).FontSize(12).TextColor(t.TextMuted)
						break
					}
					ui.Text(c, filepath.Base(f)).FontSize(12).MaxLines(1)
				}
			})
		})
	})
}

func (g *gallery) sparkline(c *ui.Context) *ui.Element {
	t := c.Theme()
	return ui.Box(c).Radius(6).Background(t.Surface).Draw(func(p *ui.Painter, r ui.Rect) {
		if len(g.samples) < 2 {
			return
		}
		var path ui.Path
		for i, v := range g.samples {
			x := r.X + 6 + float32(i)/float32(len(g.samples)-1)*(r.W-12)
			y := r.Y + r.H - 6 - float32(v)*(r.H-12)
			if i == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		p.StrokePath(&path, 2, t.Accent)
	})
}

func (g *gallery) controls(c *ui.Context) {
	t := c.Theme()
	card(c, "Choices", func() {
		ui.Checkbox(c, &g.agree, "I agree to the terms")
		ui.Row(c).Gap(10).Children(func() {
			ui.Switch(c, &g.notify).Label("Notifications")
			ui.Text(c, map[bool]string{true: "Notifications on", false: "Notifications off"}[g.notify])
		})
		ui.Row(c).Gap(18).Children(func() {
			for _, p := range []string{"Free", "Pro", "Team"} {
				ui.Radio(c, &g.plan, p, p)
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "Size")
			ui.Select(c, &g.size, []string{"Small", "Medium", "Large", "Extra large"})
		})
	})
	card(c, "Ranges", func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Slider(c, &g.volume, 0, 100).Label("Volume").Grow(1)
			ui.Textf(c, "%3.0f%%", g.volume).Width(48).TextAlign(ui.End)
		})
		ui.Progress(c, g.volume/100)
		ui.Progress(c, -1)
	})
	card(c, "Buttons", func() {
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.PrimaryButton(c, "Save")
			ui.Button(c, "Cancel")
			ui.Button(c, "Disabled").Disabled(true)
			ui.Link(c, "Open mygo.dev", "https://github.com/egoist/mygo")
		})
		ui.Text(c, "Tab moves the focus; Enter or Space presses the focused button.").TextColor(t.TextMuted)
	})
}

func (g *gallery) text(c *ui.Context) {
	t := c.Theme()
	card(c, "Form", func() {
		label := func(s string) { ui.Text(c, s).FontSize(12).Bold().TextColor(t.TextMuted) }
		label("Name")
		ui.TextInput(c, &g.name).Placeholder("Ada Lovelace").Label("Name")
		label("Email")
		in := ui.TextInput(c, &g.email).Placeholder("ada@example.com").Label("Email")
		if in.Submitted() {
			g.dialog = true
		}
		label("About you")
		ui.TextArea(c, &g.bio).Placeholder("Multiple lines, with undo, selection and input methods.").Label("About you").Height(110)
		ui.Textf(c, "%d characters", len([]rune(g.bio))).FontSize(12).TextColor(t.TextMuted)
	})
	card(c, "Typography", func() {
		ui.Text(c, "Display 28").FontSize(28).Bold()
		ui.Text(c, "Italic, underlined and struck through").Italic().Underline().Strikethrough()
		ui.Text(c, "Monospace: func main() {}").Font("monospace")
		ui.Text(c, "Mixed scripts: English, Ελληνικά, Русский, 日本語, 한국어, العربية, עברית, हिन्दी 🎉")
		ui.Text(c, strings.Repeat("Long text wraps to the width it gets. ", 6)).TextColor(t.TextMuted)
		ui.Text(c, strings.Repeat("A single line that ends with an ellipsis when it does not fit. ", 4)).SingleLine()
	})
}

func (g *gallery) list(c *ui.Context) {
	t := c.Theme()
	ui.TextInput(c, &g.filter).Placeholder("Filter 10,000 rows").Label("Filter")
	var rows []int
	for i := 0; i < 10000; i++ {
		if g.filter == "" || strings.Contains(fmt.Sprint(i), g.filter) {
			rows = append(rows, i)
		}
	}
	ui.Textf(c, "%d rows; only those in view are built. Right-click one for its menu.", len(rows)).TextColor(t.TextMuted)
	ui.List(c, len(rows), 32, func(i int) {
		n := rows[i]
		row := ui.Row(c).Fill().PaddingX(12).Gap(10).Radius(6)
		switch {
		case n == g.picked:
			row.Background(t.Accent).TextColor(t.AccentText)
		case row.Hovered():
			row.Background(t.SurfaceHover)
		}
		if row.Clicked() {
			g.picked = n
		}
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("Pick").Chosen() {
				g.picked = n
			}
			if m.Item("Starred").Checked(g.starred[n]).Chosen() {
				g.starred[n] = !g.starred[n]
			}
			m.Separator()
			if m.Item("Copy Square").Chosen() {
				mygo.Clipboard.WriteText(fmt.Sprint(n * n))
			}
		})
		row.Children(func() {
			label := fmt.Sprintf("Row %d", n)
			if g.starred[n] {
				label += "  ★"
			}
			ui.Text(c, label).Grow(1)
			ui.Textf(c, "%d²  =  %d", n, n*n).Font("monospace").FontSize(12)
		})
	}).Height(420).Border(1, t.Border).Radius(8).Padding(4)
}

func (g *gallery) drawing(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Element.Draw paints with rectangles, shadows, paths and text.").TextColor(t.TextMuted)
	ui.Box(c).Height(320).Radius(10).Background(t.Surface).Draw(func(p *ui.Painter, r ui.Rect) {
		phase := float64(c.Now().UnixMilli()%4000) / 4000 * 2 * math.Pi
		// Bars.
		for i := 0; i < 12; i++ {
			h := float32(60 + 50*math.Sin(phase+float64(i)*0.6))
			x := r.X + 24 + float32(i)*28
			p.Fill(ui.Rect{X: x, Y: r.Y + r.H - 24 - h, W: 18, H: h}, t.Accent.Alpha(0.35+0.05*float32(i)), 4)
		}
		// A sine wave.
		var wave ui.Path
		for i := 0; i <= 100; i++ {
			x := r.X + 380 + float32(i)*3
			y := r.Y + r.H/2 + float32(60*math.Sin(phase*2+float64(i)/12))
			if i == 0 {
				wave.MoveTo(x, y)
			} else {
				wave.LineTo(x, y)
			}
		}
		p.StrokePath(&wave, 3, t.Danger)
		var dot ui.Path
		dot.Circle(r.X+r.W-70, r.Y+70, 36)
		p.FillPath(&dot, t.Accent)
		p.Text(r.X+24, r.Y+20, "Animated at the display's rate", 14, t.Text)
	})
	c.AnimationFrame()
}

func (g *gallery) overlays(c *ui.Context) {
	t := c.Theme()
	card(c, "Overlays", func() {
		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "Open dialog").Clicked() {
				g.dialog = true
			}
			menu := ui.Button(c, "Menu ▾")
			if menu.Clicked() {
				g.menu = !g.menu
			}
			ui.Popover(c, menu, &g.menu, func() {
				for _, item := range []string{"New file", "Open…", "Save as…"} {
					entry := ui.Row(c).Key(item).Padding(6, 12).Radius(5).Width(180)
					if entry.Hovered() {
						entry.Background(t.Accent).TextColor(t.AccentText)
					}
					if entry.Clicked() {
						g.menu = false
					}
					entry.Children(func() { ui.Text(c, item) })
				}
			})
			ui.Button(c, "Hover me").Tooltip("Tooltips show after the pointer rests a moment.")
			if ui.Button(c, "Native dialog").Clicked() {
				go mygo.Dialog.Message(mygo.MessageOptions{Parent: g.win, Message: "Native dialogs work from MyGo UI windows too."})
			}
		})
	})
	ui.Modal(c, &g.dialog, func() {
		ui.Text(c, "A modal dialog").FontSize(18).Bold()
		ui.Text(c, "Click outside or press Escape to close it.").TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Done").Clicked() {
				g.dialog = false
			}
		})
	})
}

func main() {
	g := &gallery{page: "Overview", size: "Medium", plan: "Pro", volume: 35, picked: -1, starred: map[int]bool{}, now: time.Now()}
	mygo.App.WhenReady(func() {
		g.win = mygo.NewWindow(mygo.WindowOptions{
			Title:    "MyGo UI Gallery",
			Width:    980,
			Height:   720,
			MinWidth: 640, MinHeight: 480,
			StateKey: "gallery",
			Content:  ui.View(g.view),
		})
		go func() {
			x := 0.0
			for range time.Tick(200 * time.Millisecond) {
				x += 0.35
				v := 0.5 + 0.35*math.Sin(x) + 0.1*math.Sin(x*3.1)
				g.win.Update(func() {
					g.now = time.Now()
					g.samples = append(g.samples, v)
					if len(g.samples) > 60 {
						g.samples = g.samples[1:]
					}
				})
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
