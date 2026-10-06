package ui

import (
	"testing"
	"time"
)

func TestSpacingScalesWidgets(t *testing.T) {
	type sizes struct{ button, check, tab Rect }
	measure := func(spacing float32) sizes {
		tt := NewTester(func(c *Context) {
			th := *c.Theme()
			th.Spacing = spacing
			c.SetTheme(&th)
			Column(c).AlignItems(Start).Children(func() {
				Button(c, "OK").Label("button")
				on, tab := false, 0
				Checkbox(c, &on, "").Label("check")
				Tabs(c, &tab, "One", "Two")
			})
		}, 400, 300)
		var s sizes
		s.button, _ = tt.Find("button")
		s.check, _ = tt.Find("check")
		s.tab, _ = tt.Find("One")
		return s
	}
	compact, normal, roomy, unset := measure(3), measure(4), measure(5), measure(0)
	if normal.check.W != 16 || normal.check.H != 16 {
		t.Errorf("the check box is %vx%v at the default spacing, not 16x16", normal.check.W, normal.check.H)
	}
	if unset != normal {
		t.Errorf("no spacing lays widgets out as %+v, not as the default %+v", unset, normal)
	}
	for name, got := range map[string][3]Rect{
		"button":    {compact.button, normal.button, roomy.button},
		"check box": {compact.check, normal.check, roomy.check},
	} {
		if !(got[0].W < got[1].W && got[1].W < got[2].W && got[0].H < got[1].H && got[1].H < got[2].H) {
			t.Errorf("the %s does not grow with the spacing: %v", name, got)
		}
	}
	// The text of a tab sits in its padding: 3 units to the left.
	for _, s := range []sizes{compact, normal, roomy} {
		if s.tab.W <= 0 {
			t.Fatal("no tab")
		}
	}
	if !(compact.tab.X < normal.tab.X && normal.tab.X < roomy.tab.X) {
		t.Errorf("the tabs' padding does not follow the spacing: %v, %v, %v", compact.tab.X, normal.tab.X, roomy.tab.X)
	}
}

func TestParseHex(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Color
		ok   bool
	}{
		{"#2563eb", Color{0x25, 0x63, 0xeb, 255}, true},
		{"2563EB", Color{0x25, 0x63, 0xeb, 255}, true},
		{" #00ff0080 ", Color{0, 255, 0, 0x80}, true},
		{"#fA0", Color{255, 0xaa, 0, 255}, true},
		{"#fa08", Color{255, 0xaa, 0, 0x88}, true},
		{"#12345", Color{}, false},
		{"#1234567", Color{}, false},
		{"#123456789", Color{}, false},
		{"#12345g", Color{}, false},
		{"#+12345", Color{}, false},
		{"#é12", Color{}, false},
		{"", Color{}, false},
	} {
		got, err := parseHex(tc.in)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("parseHex(%q) = %v, %v; want %v, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
	if n := testing.AllocsPerRun(10, func() { Hex("#2563eb") }); n != 0 {
		t.Errorf("Hex allocates %v times", n)
	}
}

func TestTooltipColors(t *testing.T) {
	th := *DarkTheme()
	if bg, text := th.tooltipColors(); bg != th.Text || text != th.Background {
		t.Errorf("a theme without tooltip colors gives %v on %v, not its text on its background", text, bg)
	}
	th.TooltipBackground = Hex("#3a3a40")
	if bg, text := th.tooltipColors(); bg != Hex("#3a3a40") || text != th.Background {
		t.Errorf("a tooltip background alone gives %v on %v", text, bg)
	}
	th.TooltipText = Hex("#f2f2f7")
	if bg, text := th.tooltipColors(); bg != Hex("#3a3a40") || text != Hex("#f2f2f7") {
		t.Errorf("both tooltip colors give %v on %v", text, bg)
	}
}

// tooltipBackdrop shows a tooltip of a button in a window of the theme the
// function returns, and returns the color of the tooltip's background.
func tooltipBackdrop(t *testing.T, theme func(*Context) *Theme) Color {
	t.Helper()
	tt := NewTester(func(c *Context) {
		c.SetTheme(theme(c))
		Column(c).Padding(40).Children(func() {
			Button(c, "Go").Label("button").Tooltip("Split right")
		})
	}, 400, 200)
	b, ok := tt.Find("button")
	if !ok {
		t.Fatalf("no button; texts %q", tt.Texts())
	}
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	deadline := time.Now().Add(3 * time.Second)
	for !tt.HasText("Split right") {
		if time.Now().After(deadline) {
			t.Fatal("the tooltip did not show")
		}
		time.Sleep(50 * time.Millisecond)
		tt.Frame()
	}
	tip, _ := tt.Find("Split right")
	// Left of the text, inside the tooltip's padding.
	px := tt.Image().RGBAAt(int(tip.X)-3, int(tip.Y+tip.H/2))
	return Color{px.R, px.G, px.B, px.A}
}

func TestTooltipBackgroundOfTheTheme(t *testing.T) {
	dark := func(c *Context) *Theme { th := *DarkTheme(); return &th }
	if got, want := tooltipBackdrop(t, dark), DarkTheme().Text; got != want {
		t.Errorf("a dark theme's tooltip is %v, not its text color %v", got, want)
	}
	custom := func(c *Context) *Theme {
		th := *DarkTheme()
		th.TooltipBackground = Hex("#3a3a40")
		th.TooltipText = Hex("#f2f2f7")
		return &th
	}
	if got, want := tooltipBackdrop(t, custom), Hex("#3a3a40"); got != want {
		t.Errorf("the tooltip is %v, not the theme's TooltipBackground %v", got, want)
	}
}
