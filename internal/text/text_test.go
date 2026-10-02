package text

import (
	"strings"
	"testing"
	"time"
)

func TestLayoutWraps(t *testing.T) {
	s := Shared()
	start := time.Now()
	s.Preload()
	t.Logf("system fonts loaded in %v", time.Since(start))

	one := s.Layout(Params{Text: "Hello, world", Style: Style{Size: 16}})
	if len(one.Lines) != 1 || one.Width <= 50 || one.Height <= 16 {
		t.Fatalf("single line: %d lines, %vx%v", len(one.Lines), one.Width, one.Height)
	}
	long := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 6)
	wrapped := s.Layout(Params{Text: long, Style: Style{Size: 16}, Width: 200})
	if len(wrapped.Lines) < 4 {
		t.Fatalf("wrapped to %d lines", len(wrapped.Lines))
	}
	for i, line := range wrapped.Lines {
		if line.Width > 200.5 {
			t.Errorf("line %d is %v wide", i, line.Width)
		}
		if i > 0 && line.Start != wrapped.Lines[i-1].End {
			t.Errorf("line %d starts at %d, the previous ended at %d", i, line.Start, wrapped.Lines[i-1].End)
		}
	}
	if last := wrapped.Lines[len(wrapped.Lines)-1]; last.End != len(wrapped.Runes) {
		t.Errorf("last line ends at %d of %d", last.End, len(wrapped.Runes))
	}
}

func TestLayoutNewlinesAndEmpty(t *testing.T) {
	s := Shared()
	l := s.Layout(Params{Text: "a\n\nb", Style: Style{Size: 14}})
	if len(l.Lines) != 3 {
		t.Fatalf("%d lines", len(l.Lines))
	}
	if l.Lines[1].Start != 2 || l.Lines[1].End != 2 || l.Lines[2].Start != 3 {
		t.Errorf("lines %+v %+v", l.Lines[1], l.Lines[2])
	}
	empty := s.Layout(Params{Style: Style{Size: 14}})
	if len(empty.Lines) != 1 || empty.Height <= 0 {
		t.Errorf("empty text: %d lines, height %v", len(empty.Lines), empty.Height)
	}
}

func TestCarets(t *testing.T) {
	s := Shared()
	l := s.Layout(Params{Text: "office files", Style: Style{Size: 20}})
	prev := float32(-1)
	for i := 0; i <= len(l.Runes); i++ {
		x, _, h := l.Caret(i)
		if x < prev || h <= 0 {
			t.Fatalf("caret %d at %v after %v (height %v)", i, x, prev, h)
		}
		prev = x
		if got := l.IndexAt(x+0.1, 5); got != i {
			t.Errorf("IndexAt(caret %d) = %d", i, got)
		}
	}
	if rects := l.Selection(0, 6); len(rects) != 1 || rects[0].W <= 0 {
		t.Errorf("selection %+v", rects)
	}
}

func TestTruncation(t *testing.T) {
	s := Shared()
	l := s.Layout(Params{Text: strings.Repeat("word ", 50), Style: Style{Size: 14}, Width: 120, MaxLines: 2})
	if len(l.Lines) != 2 || !l.Truncated {
		t.Fatalf("%d lines, truncated %v", len(l.Lines), l.Truncated)
	}
}

func TestGlyphRaster(t *testing.T) {
	s := Shared()
	l := s.Layout(Params{Text: "Ag", Style: Style{Size: 32}})
	n := 0
	for _, g := range l.Lines[0].Glyphs {
		img := s.Glyph(g.Face, g.ID, 32, 0)
		if !img.OK || img.W == 0 || img.H == 0 || img.Top >= 0 {
			t.Fatalf("glyph %v: %+v", g.ID, img)
		}
		var ink int
		for y := 0; y < int(img.H); y++ {
			for x := 0; x < int(img.W); x++ {
				if s.MaskAtlas.Pix[(int(img.Y)+y)*s.MaskAtlas.W+int(img.X)+x] > 128 {
					ink++
				}
			}
		}
		if ink < 20 {
			t.Errorf("glyph %v has %d inked pixels", g.ID, ink)
		}
		n++
	}
	if n != 2 {
		t.Errorf("%d glyphs", n)
	}
}

// square draws a w×w mask filled with v.
func square(w int, v byte, calls *int) func() (int, int, []byte) {
	return func() (int, int, []byte) {
		*calls++
		pix := make([]byte, w*w)
		for i := range pix {
			pix[i] = v
		}
		return w, w, pix
	}
}

func (s *System) maskPixel(g GlyphImage) byte {
	return s.MaskAtlas.Pix[int(g.Y)*s.MaskAtlas.W+int(g.X)]
}

func TestMaskLastsOnceDrawnAgain(t *testing.T) {
	s := newSystem()
	var calls int
	s.BeginFrame()
	first := s.Mask(1, square(8, 7, &calls))
	again := s.Mask(1, square(8, 7, &calls))
	s.EndFrame()
	if !first.OK || again != first || calls != 1 {
		t.Fatalf("first frame: %+v %+v, %d draws", first, again, calls)
	}
	if int(first.Y) < s.MaskAtlas.H/2 {
		t.Errorf("a new mask went to row %d, not to the transient rows", first.Y)
	}
	s.BeginFrame()
	second := s.Mask(1, square(8, 7, &calls))
	s.EndFrame()
	if !second.OK || calls != 2 || second.Y != 0 || s.maskPixel(second) != 7 {
		t.Fatalf("second frame: %+v, %d draws", second, calls)
	}
	s.BeginFrame()
	third := s.Mask(1, square(8, 7, &calls))
	s.EndFrame()
	if third != second || calls != 2 {
		t.Errorf("third frame: %+v, %d draws", third, calls)
	}
	// Masks drawn by one frame each, as in an animation, take the same
	// transient rows again and again.
	var y uint16
	for i := range 100 {
		s.BeginFrame()
		g := s.Mask(uint64(100+i), square(30, 9, &calls))
		s.EndFrame()
		if i == 0 {
			y = g.Y
		}
		if !g.OK || s.maskPixel(g) != 9 || g.Y != y || int(y) < s.MaskAtlas.H/2 {
			t.Fatalf("frame %d: %+v", i, g)
		}
	}
	if s.MaskAtlas.W != 1024 {
		t.Errorf("the atlas grew to %d", s.MaskAtlas.W)
	}
}

func TestMakeRoom(t *testing.T) {
	s := newSystem()
	var calls int
	// Fill the lasting rows with masks of earlier frames.
	var old []uint64
	for i := 0; ; i++ {
		key := uint64(i + 1)
		for range 2 { // the second frame makes it last
			s.BeginFrame()
			s.Mask(key, square(200, byte(i+1), &calls))
			s.EndFrame()
		}
		if s.Full() {
			break
		}
		old = append(old, key)
	}
	// A frame drawing one old mask and new ones runs out of room...
	s.BeginFrame()
	kept := s.Mask(old[2], square(200, 3, &calls))
	var left int
	for i := range 3 {
		key := uint64(1000 + i)
		s.recent[key] = s.frame - 1 // drawn by the previous frame: lasting
		if !s.Mask(key, square(300, 50, &calls)).OK {
			left++
		}
	}
	if !s.Full() || left == 0 {
		t.Fatalf("full %v, %d left out", s.Full(), left)
	}
	// ...and has it when painted again.
	s.MakeRoom()
	if s.Full() {
		t.Fatal("still full")
	}
	if g := s.Mask(old[2], square(200, 3, &calls)); g == kept || s.maskPixel(g) != 3 {
		t.Errorf("the kept mask is at %d,%d with %d", g.X, g.Y, s.maskPixel(g))
	}
	for i := range 3 {
		if g := s.Mask(uint64(1000+i), square(300, 50, &calls)); !g.OK || s.maskPixel(g) != 50 {
			t.Errorf("mask %d: %+v", i, g)
		}
	}
	if s.Full() {
		t.Error("full again")
	}
	if _, ok := s.masks[old[0]]; ok {
		t.Error("kept a mask the frame did not draw")
	}
	s.EndFrame()
}
