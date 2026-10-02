package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// savePNG writes the tester's frame into the directory MYGO_UI_PNG names,
// for looking at.
func savePNG(t *testing.T, tt *Tester, name string) {
	dir := os.Getenv("MYGO_UI_PNG")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

type demo struct {
	count    int
	name     string
	agree    bool
	dark     bool
	volume   float64
	choice   string
	size     string
	notes    string
	selected int
}

func (d *demo) view(c *Context) {
	t := c.Theme()
	Column(c).Fill().Padding(20).Gap(14).Children(func() {
		Row(c).Gap(10).Children(func() {
			Text(c, "MyGo UI").FontSize(24).Bold()
			Spacer(c)
			Text(c, "GPU rendered, pure Go").TextColor(t.TextMuted)
		})
		Row(c).Gap(8).Children(func() {
			if Button(c, "Increment").Clicked() {
				d.count++
			}
			if PrimaryButton(c, "Reset").Clicked() {
				d.count = 0
			}
			Textf(c, "Count: %d", d.count)
		})
		Row(c).Gap(16).Children(func() {
			Checkbox(c, &d.agree, "I agree")
			Switch(c, &d.dark)
			Radio(c, &d.choice, "a", "Alpha")
			Radio(c, &d.choice, "b", "Beta")
		})
		Row(c).Gap(10).Children(func() {
			TextInput(c, &d.name).Placeholder("Your name").Grow(1)
			Select(c, &d.size, []string{"Small", "Medium", "Large"})
		})
		Slider(c, &d.volume, 0, 100)
		Progress(c, d.volume/100)
		Box(c).Padding(12).Radius(10).Background(t.Surface).Border(1, t.Border).Shadow(0, 2, 10, 0, RGBA(0, 0, 0, 0.12)).Children(func() {
			Text(c, "A card with a shadow and a long text that wraps across lines when the window is narrow enough to need it.")
		})
		List(c, 1000, 28, func(i int) {
			row := Row(c).Fill().PaddingX(8).Gap(8)
			if i == d.selected {
				row.Background(t.Accent).TextColor(t.AccentText).Radius(4)
			}
			if row.Clicked() {
				d.selected = i
			}
			row.Children(func() {
				Textf(c, "Row %d", i).Grow(1)
				Text(c, fmt.Sprint(i*i)).TextColor(t.TextMuted)
			})
		}).Grow(1).Border(1, t.Border).Radius(6)
	})
}

func TestDemoRenders(t *testing.T) {
	d := &demo{choice: "a", size: "Medium", volume: 40}
	tt := NewTester(d.view, 640, 600)
	savePNG(t, tt, "demo")
	for _, s := range []string{"MyGo UI", "Count: 0", "I agree", "Row 0", "Medium"} {
		if _, ok := tt.Find(s); !ok {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	tt.SetScale(2)
	savePNG(t, tt, "demo@2x")
	tt.SetDark(true)
	savePNG(t, tt, "demo-dark")
}

func TestClickCounts(t *testing.T) {
	d := &demo{}
	tt := NewTester(d.view, 640, 600)
	for i := 0; i < 3; i++ {
		if err := tt.Click("Increment"); err != nil {
			t.Fatal(err)
		}
	}
	if d.count != 3 || !tt.HasText("Count: 3") {
		t.Fatalf("count %d, texts %q", d.count, tt.Texts())
	}
	tt.Click("I agree")
	if !d.agree {
		t.Error("the checkbox did not toggle")
	}
	tt.Click("Beta")
	if d.choice != "b" {
		t.Errorf("radio chose %q", d.choice)
	}
}

func TestTyping(t *testing.T) {
	d := &demo{}
	tt := NewTester(d.view, 640, 600)
	r, ok := tt.Find("Your name")
	if !ok {
		// The placeholder is painted, not an element: find the input by
		// its neighbor instead.
		r, _ = tt.Find("I agree")
		r.Y += 40
	}
	tt.ClickAt(r.X+20, r.Y+r.H/2)
	tt.Type("Héllo wörld")
	if d.name != "Héllo wörld" {
		t.Fatalf("typed %q", d.name)
	}
	tt.Key(0, KeyBackspace)
	tt.Key(Ctrl, KeyLeft)
	tt.Type("big ")
	if d.name != "Héllo big wörl" {
		t.Fatalf("edited to %q", d.name)
	}
	tt.Key(Cmd, KeyA)
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != "Héllo big wörl" {
		t.Errorf("copied %q", tt.Clipboard())
	}
	tt.Key(Cmd, KeyZ)
	if d.name != "Héllo wörl" {
		t.Errorf("undid to %q", d.name)
	}
	savePNG(t, tt, "typing")
}

func TestListScrollsAndSelects(t *testing.T) {
	d := &demo{}
	tt := NewTester(d.view, 640, 600)
	r, ok := tt.Find("Row 0")
	if !ok {
		t.Fatal("no first row")
	}
	tt.Scroll(r.X+10, r.Y+10, 0, 28*50)
	if _, ok := tt.Find("Row 0"); ok {
		t.Error("row 0 still built after scrolling")
	}
	if !tt.HasText("Row 52") {
		t.Errorf("row 52 not in view: %q", tt.Texts())
	}
	tt.Click("Row 52")
	if d.selected != 52 {
		t.Errorf("selected %d", d.selected)
	}
	savePNG(t, tt, "scrolled")
}

func TestTabFocus(t *testing.T) {
	d := &demo{}
	tt := NewTester(d.view, 640, 600)
	tt.Key(0, KeyTab)
	if !tt.Focused("Increment") {
		t.Fatal("Tab did not focus the first button")
	}
	tt.Key(0, KeyEnter)
	if d.count != 1 {
		t.Errorf("Enter on the focused button: count %d", d.count)
	}
	tt.Key(0, KeyTab)
	if !tt.Focused("Reset") {
		t.Error("Tab did not move to the second button")
	}
	savePNG(t, tt, "focus")
}

func TestAutoFocus(t *testing.T) {
	var open bool
	var name string
	view := func(c *Context) {
		if Button(c, "Rename").Clicked() {
			open = true
		}
		Modal(c, &open, func() {
			TextInput(c, &name).Label("Name").AutoFocus()
			if Button(c, "Done").Clicked() {
				open = false
			}
		})
	}
	tt := NewTester(view, 400, 300)
	tt.Click("Rename")
	if !tt.Focused("Name") {
		t.Fatal("the input of the opened dialog has no focus")
	}
	tt.Type("Ada")
	tt.Key(0, KeyTab)
	tt.Frame()
	if !tt.Focused("Done") {
		t.Fatal("Tab did not move the focus away from the input")
	}
	tt.Key(0, KeyEnter)
	if open || name != "Ada" {
		t.Fatalf("open %v, name %q", open, name)
	}
	tt.Click("Rename")
	if !tt.Focused("Name") {
		t.Error("the input of the reopened dialog has no focus")
	}
}

// listScroll returns how far the demo's list of a thousand rows scrolled,
// and its height.
func listScroll(tt *Tester) (y, h float32) {
	for _, s := range tt.rt.states {
		if s.flags&flagScrollY != 0 && s.contentH > 1000*28-1 {
			return s.scrollY, s.h
		}
	}
	return -1, 0
}

func TestKeyboardScrolling(t *testing.T) {
	d := &demo{volume: 40}
	tt := NewTester(d.view, 640, 600)
	row, _ := tt.Find("Row 1")
	// Without a focus, keys scroll what the pointer is over, although the
	// slider elsewhere takes arrow keys while it has the focus.
	tt.Move(row.X+10, row.Y+10)
	tt.Key(0, KeyDown)
	if y, _ := listScroll(tt); y != 40 {
		t.Fatalf("Down scrolled to %v", y)
	}
	tt.Key(0, KeyPageDown)
	if y, h := listScroll(tt); abs32(y-h) > 0.01 {
		t.Errorf("PageDown scrolled to %v, the list being %v high", y, h)
	}
	tt.Key(0, KeyEnd)
	if y, h := listScroll(tt); abs32(y+h-1000*28-2) > 0.01 { // 2: the border
		t.Errorf("End scrolled to %v, the list being %v high", y, h)
	}
	if !tt.HasText("Row 999") {
		t.Error("the last row is not in view")
	}
	tt.Key(Shift, KeySpace)
	tt.Key(0, KeyHome)
	if y, _ := listScroll(tt); y != 0 {
		t.Errorf("Home scrolled to %v", y)
	}

	// The focused slider takes the arrow keys.
	tt.Key(0, KeyTab)
	if !tt.Focused("Increment") {
		t.Fatal("no first focus")
	}
	for i := 0; i < 20 && d.volume == 40; i++ {
		tt.Key(0, KeyTab)
		tt.Key(0, KeyRight)
	}
	if d.volume != 41 {
		t.Fatalf("the slider is at %v", d.volume)
	}
	tt.Key(0, KeyDown)
	if y, _ := listScroll(tt); d.volume != 40 || y != 0 {
		t.Errorf("Down moved the slider to %v and the list to %v", d.volume, y)
	}
}
