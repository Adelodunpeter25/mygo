package ui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // DecodeBitmap
	_ "image/jpeg" // DecodeBitmap
	_ "image/png"  // DecodeBitmap
	"math"
	"time"

	"github.com/egoist/mygo/internal/scene"
)

// Box creates a container that lays its children out in a column.
func Box(c *Context) *Element { return c.newElement(kindBox) }

// Column creates a container that lays its children out from top to
// bottom, stretched to its width.
func Column(c *Context) *Element { return c.newElement(kindBox) }

// Row creates a container that lays its children out from left to right,
// centered vertically.
func Row(c *Context) *Element { return c.newElement(kindBox).Row() }

// Text creates a text, which wraps at the width it gets.
func Text(c *Context, s string) *Element {
	e := c.newElement(kindText)
	e.text = s
	return e
}

// Textf creates a text formatted with fmt.Sprintf.
func Textf(c *Context, format string, args ...any) *Element {
	return Text(c, fmt.Sprintf(format, args...))
}

// Spacer creates an empty element that takes the free space of its row or
// column, pushing its siblings apart.
func Spacer(c *Context) *Element { return Box(c).Grow(1) }

// Divider creates a thin line across its row or column.
func Divider(c *Context) *Element {
	row := c.parent.row
	e := Box(c).Background(c.theme.Border).Shrink(0).AlignSelf(Stretch)
	if row {
		return e.Width(1)
	}
	return e.Height(1)
}

// Animate returns a value that moves to target over d, easing out, and
// keeps frames coming while it moves; key tells apart the animations of
// the element. The value starts at the first target.
func (e *Element) Animate(key any, target float32, d time.Duration) float32 {
	st := e.st
	if st.anims == nil {
		st.anims = map[any]*anim{}
	}
	a := st.anims[key]
	now := e.c.now
	if a == nil {
		st.anims[key] = &anim{from: target, to: target, value: target}
		return target
	}
	if a.to != target {
		a.from, a.to, a.start, a.dur = a.value, target, now, d
	}
	if a.value != a.to {
		t := float32(now.Sub(a.start)) / float32(max(a.dur, time.Millisecond))
		if t >= 1 {
			a.value = a.to
		} else {
			u := 1 - t
			a.value = a.from + (a.to-a.from)*(1-u*u*u)
			e.c.AnimationFrame()
		}
	}
	return a.value
}

type anim struct {
	from, to, value float32
	start           time.Time
	dur             time.Duration
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// Button creates a button showing label. Ask Clicked whether it was
// clicked; give it other content with Children and an empty label.
func Button(c *Context, label string) *Element { return button(c, label, false) }

// PrimaryButton creates a button in the accent color, for the main action.
func PrimaryButton(c *Context, label string) *Element { return button(c, label, true) }

func button(c *Context, label string, primary bool) *Element {
	t := c.theme
	b := Row(c).Center().Padding(6, 14).Gap(6).Radius(t.Radius).Focusable().Shrink(0)
	b.flags |= flagClickable | flagHover
	base, hover, pressed, fg, border := t.Surface, t.SurfaceHover, t.SurfacePressed, t.Text, t.Border
	if primary {
		base, hover, pressed, fg, border = t.Accent, t.AccentHover, t.AccentPressed, t.AccentText, Color{}
	}
	b.Background(base).TextColor(fg)
	if border.A > 0 {
		b.Border(1, border)
	}
	b.styleFn = func(b *Element) {
		if b.bg != base || b.IsDisabled() {
			return
		}
		if b.Pressed() {
			b.bg = pressed
		} else if b.Hovered() {
			b.bg = hover
		}
	}
	if label != "" {
		b.Children(func() { Text(c, label).SingleLine() })
	}
	return b
}

// Link creates a text that opens url in the browser when clicked.
func Link(c *Context, label, url string) *Element {
	t := c.theme
	e := Text(c, label).TextColor(t.Accent).Cursor(CursorPointer).Focusable()
	e.widget, e.role = "Link", RoleLink
	if e.Clicked() && url != "" {
		c.rt.host.openURL(url)
	}
	if e.Hovered() {
		e.Underline()
	}
	return e
}

func checkPath(r Rect) *Path {
	var p Path
	return p.MoveTo(r.X+r.W*0.22, r.Y+r.H*0.52).LineTo(r.X+r.W*0.42, r.Y+r.H*0.71).LineTo(r.X+r.W*0.78, r.Y+r.H*0.31)
}

// Checkbox creates a check box toggling *checked, with a label.
func Checkbox(c *Context, checked *bool, label string) *Element {
	t := c.theme
	row := Row(c).Gap(8).Focusable().Shrink(0)
	row.flags |= flagClickable | flagHover | flagOwnRing
	row.widget, row.role = "Checkbox", RoleCheckBox
	if row.Clicked() {
		*checked = !*checked
		row.st.changed = true
	}
	on := *checked
	row.checked = 1 + int8(b2f(on))
	row.Children(func() {
		box := Box(c).Size(16, 16).Radius(4).Shrink(0)
		if on {
			box.Background(t.Accent)
		} else {
			box.Background(t.Background).Border(1, t.Border.Mix(t.Text, 0.25))
		}
		box.DrawOver(func(p *Painter, r Rect) {
			if on {
				p.StrokePath(checkPath(r), 2, t.AccentText)
			}
			if row.FocusVisible() {
				p.FocusRing(r, [4]float32{4, 4, 4, 4})
			}
		})
		box.styleFn = func(box *Element) {
			if !on && row.Hovered() {
				box.borderC = t.Accent
			}
		}
		if label != "" {
			Text(c, label)
		}
	})
	return row
}

// Radio creates a radio button that selects value into *selected, with a
// label.
func Radio[T comparable](c *Context, selected *T, value T, label string) *Element {
	t := c.theme
	row := Row(c).Gap(8).Focusable().Shrink(0)
	row.flags |= flagClickable | flagHover | flagOwnRing
	row.widget, row.role = "Radio", RoleRadio
	if row.Clicked() && *selected != value {
		*selected = value
		row.st.changed = true
	}
	on := *selected == value
	row.checked = 1 + int8(b2f(on))
	row.Children(func() {
		dot := Box(c).Size(16, 16).Radius(8).Shrink(0)
		if on {
			dot.Background(t.Accent)
		} else {
			dot.Background(t.Background).Border(1, t.Border.Mix(t.Text, 0.25))
		}
		dot.DrawOver(func(p *Painter, r Rect) {
			if on {
				p.Fill(Rect{r.X + 5, r.Y + 5, 6, 6}, t.AccentText, 3)
			}
			if row.FocusVisible() {
				p.FocusRing(r, [4]float32{8, 8, 8, 8})
			}
		})
		dot.styleFn = func(dot *Element) {
			if !on && row.Hovered() {
				dot.borderC = t.Accent
			}
		}
		if label != "" {
			Text(c, label)
		}
	})
	return row
}

// Switch creates a switch toggling *on.
func Switch(c *Context, on *bool) *Element {
	t := c.theme
	sw := Box(c).Size(36, 20).Radius(10).Focusable().Shrink(0)
	sw.flags |= flagClickable | flagHover
	sw.widget, sw.role = "Switch", RoleSwitch
	if sw.Clicked() {
		*on = !*on
		sw.st.changed = true
	}
	sw.checked = 1 + int8(b2f(*on))
	pos := sw.Animate("knob", b2f(*on), 140*time.Millisecond)
	off := t.Border.Mix(t.Text, 0.15)
	sw.Background(off.Mix(t.Accent, pos))
	sw.Draw(func(p *Painter, r Rect) {
		d := r.H - 4
		knob := Rect{r.X + 2 + pos*(r.W-r.H), r.Y + 2, d, d}
		p.Shadow(Rect{knob.X, knob.Y + 1, knob.W, knob.H}, d/2, 3, RGBA(0, 0, 0, 0.25))
		p.Fill(knob, RGB(255, 255, 255), d/2)
	})
	return sw
}

// Slider creates a slider setting *value between lo and hi.
func Slider(c *Context, value *float64, lo, hi float64) *Element {
	t := c.theme
	s := Box(c).Height(20).MinWidth(80).Focusable()
	s.flags |= flagDraggable | flagHover | flagOwnRing
	s.widget = "Slider"
	st := s.st
	set := func(v float64) {
		v = math.Max(lo, math.Min(hi, v))
		if v != *value {
			*value = v
			st.changed = true
			c.rt.consumed = true
		}
	}
	const knob = 16
	if st.pressed && st.w > knob {
		frac := (c.rt.pointerX - st.x - knob/2) / (st.w - knob)
		set(lo + float64(max(0, min(1, frac)))*(hi-lo))
	}
	step := (hi - lo) / 100
	if s.Shortcut(0, KeyLeft) || s.Shortcut(0, KeyDown) {
		set(*value - step)
	}
	if s.Shortcut(0, KeyRight) || s.Shortcut(0, KeyUp) {
		set(*value + step)
	}
	if s.Shortcut(0, KeyHome) {
		set(lo)
	}
	if s.Shortcut(0, KeyEnd) {
		set(hi)
	}
	frac := float32(0)
	if hi > lo {
		frac = float32((*value - lo) / (hi - lo))
	}
	s.role, s.hasRange, s.accRange = RoleSlider, true, [3]float64{lo, hi, *value}
	s.Draw(func(p *Painter, r Rect) {
		track := Rect{r.X + knob/2, r.Y + r.H/2 - 2, r.W - knob, 4}
		p.Fill(track, t.Border.Mix(t.Text, 0.1), 2)
		p.Fill(Rect{track.X, track.Y, track.W * frac, 4}, t.Accent, 2)
		k := Rect{r.X + (r.W-knob)*frac, r.Y + r.H/2 - knob/2, knob, knob}
		p.Shadow(Rect{k.X, k.Y + 1, k.W, k.H}, knob/2, 3, RGBA(0, 0, 0, 0.3))
		p.Fill(k, RGB(255, 255, 255), knob/2)
		p.Stroke(k, t.Border, knob/2, 1)
		if s.FocusVisible() {
			p.FocusRing(k, [4]float32{knob / 2, knob / 2, knob / 2, knob / 2})
		}
	})
	return s
}

// Progress creates a progress bar filled to value between 0 and 1; a
// negative value shows activity of unknown length.
func Progress(c *Context, value float64) *Element {
	t := c.theme
	e := Box(c).Height(6).Radius(3).Background(t.Border).Clip()
	e.role, e.hasRange, e.accRange = RoleProgress, true, [3]float64{0, 1, value}
	now := c.now
	if value < 0 {
		c.AnimationFrame()
	}
	e.Draw(func(p *Painter, r Rect) {
		if value >= 0 {
			p.Fill(Rect{r.X, r.Y, r.W * float32(math.Min(value, 1)), r.H}, t.Accent, 3)
			return
		}
		phase := float32(now.UnixMilli()%1400) / 1400
		w := r.W * 0.3
		x := r.X - w + (r.W+w)*phase
		p.Clip(r, 3, func() { p.Fill(Rect{x, r.Y, w, r.H}, t.Accent, 3) })
	})
	return e
}

// Scroll creates a container that scrolls its children vertically. Give
// it a size, or Grow it within its parent.
func Scroll(c *Context) *Element {
	e := Box(c)
	e.flags |= flagScrollY | flagHover
	return e
}

// ScrollHorizontal creates a row that scrolls its children horizontally.
func ScrollHorizontal(c *Context) *Element {
	e := Row(c)
	e.flags |= flagScrollX | flagHover
	return e
}

// List creates a vertical scroll container for n rows of rowHeight DIPs
// that only builds the rows in view, with row(i).
func List(c *Context, n int, rowHeight float32, row func(i int)) *Element {
	e := Scroll(c)
	e.widget, e.role = "List", RoleList
	st := e.st
	view := st.h
	if view <= 0 {
		view = c.h
	}
	first := max(0, int(st.scrollY/rowHeight)-2)
	last := min(n, int((st.scrollY+view)/rowHeight)+3)
	e.Children(func() {
		if first > 0 {
			Box(c).Height(float32(first) * rowHeight).Shrink(0)
		}
		for i := first; i < last; i++ {
			r := Box(c).Key(i).Height(rowHeight).Shrink(0)
			r.Children(func() { row(i) })
		}
		if last < n {
			Box(c).Height(float32(n-last) * rowHeight).Shrink(0)
		}
	})
	return e
}

// Bitmap is an image to show with Image. Create it once: converting an
// image is not free.
type Bitmap struct {
	img  *scene.Image
	w, h int
}

// NewBitmap converts img.
func NewBitmap(img image.Image) *Bitmap {
	s := scene.NewImage(img)
	return &Bitmap{img: s, w: s.W, h: s.H}
}

// DecodeBitmap decodes a PNG, JPEG or GIF image.
func DecodeBitmap(data []byte) (*Bitmap, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return NewBitmap(img), nil
}

// Size returns the bitmap's size in pixels, which Image shows as DIPs.
func (b *Bitmap) Size() (w, h int) { return b.w, b.h }

func (b *Bitmap) imageSize() (float32, float32) {
	if b == nil {
		return 0, 0
	}
	return float32(b.w), float32(b.h)
}

// ImageSource is what Image shows: a *Bitmap, or an *SVG in its own
// colors.
type ImageSource interface {
	imageSize() (w, h float32)
}

// Image creates an element showing a bitmap, or an SVG in its own colors
// (with the text color for its currentColor), by default at its size as
// DIPs, scaled to fit when given another size.
func Image(c *Context, src ImageSource) *Element {
	e := c.newElement(kindImage)
	switch s := src.(type) {
	case *Bitmap:
		e.image = s
	case *SVG:
		e.svg = s
	}
	if src != nil {
		if w, h := src.imageSize(); h > 0 {
			e.aspect = w / h
		}
	}
	return e
}

// intrinsicSize returns the size of an image's picture, or of an icon: as
// high as the font size.
func (e *Element) intrinsicSize() (w, h float32) {
	switch e.kind {
	case kindImage:
		if e.image != nil {
			return e.image.imageSize()
		}
		return e.svg.imageSize()
	case kindIcon:
		em := e.resolvedText().size
		if s := e.svg; s != nil && s.h > 0 {
			return em * s.w / s.h, em
		}
		return em, em
	}
	return 0, 0
}

// Fit sets how an Image fills its box.
func (e *Element) Fit(f Fit) *Element { e.fit = f; return e }

// Tooltip shows s near the pointer when it rests on the element.
func (e *Element) Tooltip(s string) *Element {
	e.flags |= flagHover
	rt := e.c.rt
	if !e.Hovered() || rt.pressed != nil || s == "" {
		return e
	}
	// Only the innermost element with a tooltip shows it.
	if rt.tooltipFrame == rt.frame && rt.tooltipDepth >= e.depth {
		return e
	}
	rt.tooltipFrame, rt.tooltipDepth = rt.frame, e.depth
	wait := 600*time.Millisecond - e.c.now.Sub(rt.hoverSince)
	if wait > 0 {
		e.c.After(wait)
		return e
	}
	c := e.c
	t := c.theme
	x, y := rt.pointerX+12, rt.pointerY+18
	Overlay(c, func() {
		tip := Box(c).Absolute().Left(x).Top(y).MaxWidth(320).Padding(5, 8).Radius(5).
			Background(t.Text).TextColor(t.Background).FontSize(t.FontSize - 1).PassThrough().Role(RoleTooltip)
		tip.Shadow(0, 2, 8, 0, RGBA(0, 0, 0, 0.2))
		tip.Children(func() { Text(c, s) })
		keepInWindow(c, tip, x, y, y-30)
	})
	return e
}

// Overlay builds fn's elements above the rest of the window. Place them
// with Absolute, Left and Top, in DIPs relative to the window.
func Overlay(c *Context, fn func()) {
	saved := c.parent
	c.parent = c.overlayRoot()
	fn()
	c.parent = saved
}

// Modal shows a dialog built by fn over a dimmed window while *open is
// true; clicking outside it or pressing Escape sets *open to false.
func Modal(c *Context, open *bool, fn func()) *Element {
	if !*open {
		return nil
	}
	t := c.theme
	var panel *Element
	Overlay(c, func() {
		back := Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(RGBA(0, 0, 0, 0.4)).Center()
		back.flags |= flagClickable
		if back.Clicked() || c.Shortcut(0, KeyEscape) {
			*open = false
		}
		back.Children(func() {
			panel = Box(c).Padding(20).Gap(12).Radius(10).Background(t.Background).MaxWidth(c.w - 40).MaxHeight(c.h - 40).Role(RoleDialog)
			panel.Shadow(0, 10, 30, 0, RGBA(0, 0, 0, 0.3))
			panel.flags |= flagClickable
			panel.Children(fn)
		})
	})
	return panel
}

// Popover shows fn's elements in a panel below anchor while *open is
// true; clicking outside it or pressing Escape sets *open to false.
func Popover(c *Context, anchor *Element, open *bool, fn func()) *Element {
	if !*open {
		return nil
	}
	t := c.theme
	b := anchor.Bounds()
	var panel *Element
	Overlay(c, func() {
		back := Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0)
		back.flags |= flagClickable
		if back.Clicked() || c.Shortcut(0, KeyEscape) {
			*open = false
		}
		panel = Box(c).Absolute().Left(b.X).Top(b.Y+b.H+4).MinWidth(b.W).Padding(4).Radius(t.Radius+2).
			Background(t.Background).Border(1, t.Border).Role(RolePopup)
		panel.Shadow(0, 6, 20, 0, RGBA(0, 0, 0, 0.18))
		panel.flags |= flagClickable
		panel.Children(fn)
		keepInWindow(c, panel, b.X, b.Y+b.H+4, b.Y-4)
	})
	return panel
}

// Select creates a drop-down choosing one of options into *selected.
func Select(c *Context, selected *string, options []string) *Element {
	t := c.theme
	b := Button(c, "")
	b.widget, b.role, b.accValue = "Select", RolePopUpButton, *selected
	b.Justify(SpaceBetween).MinWidth(140)
	open := Local(b, "open", func() bool { return false })
	if b.Clicked() {
		*open = !*open
	}
	b.expanded = *open
	b.Children(func() {
		Text(c, *selected).SingleLine()
		Box(c).Size(10, 10).Shrink(0).Draw(func(p *Painter, r Rect) {
			var path Path
			path.MoveTo(r.X+1, r.Y+3).LineTo(r.X+5, r.Y+7).LineTo(r.X+9, r.Y+3)
			p.StrokePath(&path, 1.5, t.TextMuted)
		})
	})
	Popover(c, b, open, func() {
		for _, opt := range options {
			item := Row(c).Key(opt).Padding(6, 10).Radius(t.Radius)
			item.flags |= flagClickable | flagHover
			if opt == *selected {
				item.Background(t.Surface)
			}
			if item.Clicked() {
				if *selected != opt {
					*selected = opt
					b.st.changed = true
				}
				*open = false
			}
			item.styleFn = func(item *Element) {
				if item.Hovered() {
					item.bg = t.Accent
					item.ts.color, item.ts.set = t.AccentText, item.ts.set|setColor
				}
			}
			item.Children(func() { Text(c, opt).SingleLine() })
		}
	})
	return b
}

// keepInWindow moves an overlay element placed at (x, y) so that it fits
// in the window, by the size it had in the last frame: left when it would
// overflow the right edge, above (ending at aboveY) when it would overflow
// the bottom. Without a last frame it asks for another one to settle.
func keepInWindow(c *Context, e *Element, x, y, aboveY float32) {
	b := e.Bounds()
	if b.W == 0 && b.H == 0 {
		c.AnimationFrame()
		return
	}
	if x+b.W > c.w-4 {
		e.Left(max(4, c.w-4-b.W))
	}
	if y+b.H > c.h-4 && aboveY-b.H > 4 {
		e.Top(aboveY - b.H)
	}
}
