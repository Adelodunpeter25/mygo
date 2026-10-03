package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

func TestTabs(t *testing.T) {
	tab := 0
	changes := 0
	tt := NewTester(func(c *Context) {
		Column(c).Padding(10).Children(func() {
			if Tabs(c, &tab, "One", "Two", "Three").Changed() {
				changes++
			}
			Textf(c, "page %d", tab)
		})
	}, 400, 200)
	if err := tt.Click("Two"); err != nil {
		t.Fatal(err)
	}
	if tab != 1 || !tt.HasText("page 1") || changes != 1 {
		t.Fatalf("a click on Two: tab %d, %d changes, texts %q", tab, changes, tt.Texts())
	}
	// The arrows move the choice, and the focus with it.
	tt.Key(0, KeyRight)
	if tab != 2 || !tt.Focused("Three") {
		t.Errorf("Right: tab %d, Three focused %v", tab, tt.Focused("Three"))
	}
	tt.Key(0, KeyRight)
	if tab != 0 || !tt.Focused("One") {
		t.Errorf("Right past the last tab: tab %d", tab)
	}
	tt.Key(0, KeyEnd)
	if tab != 2 {
		t.Errorf("End: tab %d", tab)
	}
	tt.Key(0, KeyHome)
	if tab != 0 {
		t.Errorf("Home: tab %d", tab)
	}

	tt.rt.accessibilityOn()
	tt.Frame()
	var tabs, selected int
	for _, n := range tt.h.access.Nodes {
		if n.Role == platform.RoleTab {
			tabs++
			if n.States&platform.AccessChecked != 0 {
				selected++
			}
		}
	}
	if tabs != 3 || selected != 1 {
		t.Errorf("assistive technology sees %d tabs, %d selected", tabs, selected)
	}
}

func TestSplit(t *testing.T) {
	size := float32(120)
	tt := NewTester(func(c *Context) {
		Split(c, &size, func() { Text(c, "left") }, func() { Text(c, "right") }).Fill()
	}, 400, 200)
	if r, _ := tt.Find("right"); r.X < 120+6-0.5 || r.X > 120+6+0.5 {
		t.Fatalf("the second pane starts at %v", r.X)
	}
	// The divider moves with the pointer, within the window.
	x, y := float32(123), float32(100)
	tt.Press(x, y)
	tt.Move(x+50, y)
	tt.Release(x+50, y)
	if size != 170 {
		t.Errorf("dragged 50 DIPs right, the first pane is %v wide", size)
	}
	tt.Press(x+50, y)
	tt.Move(0, y)
	tt.Release(0, y)
	if size != 40 {
		t.Errorf("dragged to the edge, the first pane is %v wide", size)
	}
	// The arrows move it once it has the focus.
	tt.ClickAt(43, y)
	tt.Key(0, KeyRight)
	if size != 50 {
		t.Errorf("Right on the divider: %v", size)
	}
	if r, _ := tt.Find("right"); r.X != 56 {
		t.Errorf("the second pane follows to %v", r.X)
	}
}

func TestNumberInput(t *testing.T) {
	v := 5.0
	tt := NewTester(func(c *Context) {
		Column(c).Padding(10).AlignItems(Start).Children(func() {
			NumberInput(c, &v, 0, 10, 0.5).Label("Count")
		})
	}, 400, 100)
	if err := tt.Click("Increase"); err != nil {
		t.Fatal(err)
	}
	tt.Click("Decrease")
	tt.Click("Decrease")
	if v != 4.5 {
		t.Errorf("one step up and two down from 5: %v", v)
	}
	r, _ := tt.Find("Count")
	tt.ClickAt(r.X+20, r.Y+r.H/2)
	tt.Key(Cmd, KeyA)
	tt.Type("7.5")
	if v != 7.5 {
		t.Errorf("typed 7.5: %v", v)
	}
	tt.Key(Cmd, KeyA)
	tt.Type("12")
	if v != 7.5 {
		t.Errorf("12 is out of range, yet the value is %v", v)
	}
	tt.Key(0, KeyUp)
	if v != 8 {
		t.Errorf("Up from 7.5: %v", v)
	}
	for range 5 {
		tt.Key(0, KeyUp)
	}
	if v != 10 {
		t.Errorf("Up past the top: %v", v)
	}
}

func TestToast(t *testing.T) {
	tt := NewTester(func(c *Context) {
		if Button(c, "Save").Clicked() {
			c.Toast("Saved")
		}
	}, 400, 300)
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Saved") {
		t.Fatalf("no toast after the click: %q", tt.Texts())
	}
	tt.Click("Save")
	if len(tt.rt.toasts) != 1 {
		t.Errorf("the same message shows %d times", len(tt.rt.toasts))
	}
	tt.rt.toasts[0].at = time.Now().Add(-toastTime)
	tt.Frame()
	if tt.HasText("Saved") {
		t.Error("the toast stays once its time is over")
	}
}
