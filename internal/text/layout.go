package text

import (
	"math"
	"slices"
	"sync"
	"unicode"

	"github.com/egoist/mygo/internal/scene"
)

// Style selects the font of a text.
type Style struct {
	// Family is a comma-separated list of font families, where "system-ui"
	// and "monospace" are the system's own; "" is the system UI font.
	Family string
	// Size in DIPs; 0 is 14.
	Size float32
	// Weight from 100 (thin) to 900 (black); 0 is 400.
	Weight int
	Italic bool
	// LineHeight is the height of a line as a multiple of Size; 0 takes
	// the font's own line spacing.
	LineHeight float32
}

func (s Style) weight() int {
	if s.Weight <= 0 {
		return 400
	}
	return min(s.Weight, 999)
}

// FontSize returns the size in DIPs.
func (s Style) FontSize() float32 {
	if s.Size <= 0 {
		return 14
	}
	return s.Size
}

// Align is the horizontal alignment of the lines of a layout.
type Align uint8

const (
	// Start aligns lines to the left, or to the right in right-to-left
	// paragraphs.
	Start Align = iota
	Center
	End
)

// Params describe a text to lay out.
type Params struct {
	Text  string
	Style Style
	// Width wraps lines to this many DIPs; 0 or less only breaks lines at
	// newlines.
	Width float32
	// MaxLines truncates the text to that many lines, ending it with an
	// ellipsis; 0 is unlimited.
	MaxLines int
	Align    Align
	// KeepSpaces keeps the advance of whitespace at the end of wrapped
	// lines, as text editors do.
	KeepSpaces bool
	// NoBreakWords only breaks lines between words, letting a long word
	// overflow the width instead of breaking it.
	NoBreakWords bool
}

// Layout is shaped and wrapped text. Positions are in DIPs relative to the
// top-left corner of the text.
type Layout struct {
	Params Params
	Runes  []rune
	Lines  []Line
	// Width is the width of the longest line, Height the sum of the line
	// heights.
	Width, Height float32
	// Truncated reports that MaxLines cut the text.
	Truncated bool
}

// Line is a line of a Layout.
type Line struct {
	// X, Y, Width and Height are the line's box; X includes the alignment.
	X, Y, Width, Height float32
	// Baseline is the y of the baseline; Ascent and Descent the extent of
	// the fonts above and below it.
	Baseline, Ascent, Descent float32
	// Start and End are the line's runes; a newline ending it is not
	// included.
	Start, End int
	// RTL reports a right-to-left paragraph.
	RTL    bool
	Glyphs []Glyph

	carets []float32
}

// Glyph is a positioned glyph.
type Glyph struct {
	Font *Font
	ID   uint32
	// Size is the font size in DIPs.
	Size float32
	// X is the left of the glyph's advance box, Y its baseline.
	X, Y    float32
	Advance float32
	// Cluster is the first rune of the glyph's cluster, Runes how many
	// runes the cluster holds; glyphs of the ellipsis have Runes 0.
	Cluster, Runes int
	RTL            bool
}

// System lays out text and rasterizes glyphs with the system's own text
// engine. Use Shared.
type System struct {
	mu  sync.Mutex
	eng engine
	// fonts caches the font of each style.
	fonts map[Style]*Font

	layouts map[Params]*cached
	frame   uint64

	glyphs map[glyphKey]*atlasEntry
	masks  map[uint64]*atlasEntry
	// transient holds the masks drawn for the frame being painted alone,
	// recent the frame each of them was last drawn in.
	transient map[uint64]GlyphImage
	recent    map[uint64]uint64
	// MaskAtlas holds coverage masks, ColorAtlas color glyphs.
	MaskAtlas, ColorAtlas *scene.Atlas
	// full tells which atlases (mask, color) left out something the frame
	// draws, and want how many pixels that needed; failed counts failed
	// allocations.
	full   [2]bool
	want   [2]int
	failed int
	// rooms counts MakeRoom calls during the frame.
	rooms int
}

type cached struct {
	layout *Layout
	used   uint64
}

var shared = sync.OnceValue(newSystem)

func newSystem() *System {
	return &System{
		fonts:      map[Style]*Font{},
		layouts:    map[Params]*cached{},
		glyphs:     map[glyphKey]*atlasEntry{},
		masks:      map[uint64]*atlasEntry{},
		transient:  map[uint64]GlyphImage{},
		recent:     map[uint64]uint64{},
		MaskAtlas:  scene.NewAtlas(1, 1024, 1024),
		ColorAtlas: scene.NewAtlas(4, 512, 512),
	}
}

// Shared returns the process-wide text system.
func Shared() *System { return shared() }

// engine starts the system's text engine on first use.
func (s *System) engine() engine {
	if s.eng == nil {
		s.eng = newEngine()
	}
	return s.eng
}

// RegisterFont adds a TrueType or OpenType font (or collection) to the
// fonts text can use, under family, or the font's own family name when
// family is empty.
func (s *System) RegisterFont(data []byte, family string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.engine().register(data, family); err != nil {
		return err
	}
	clear(s.fonts)
	clear(s.layouts)
	return nil
}

// Preload starts the text engine and finds the system UI font, which the
// first layout otherwise waits for.
func (s *System) Preload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.font(Style{})
}

// EndFrame forgets layouts no frame used for a while. The ui package calls
// it after each frame.
func (s *System) EndFrame() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, f := range s.recent {
		// Windows take turns: a mask drawn again within a few frames is
		// probably drawn by every frame of its window.
		if s.frame-f >= 8 {
			delete(s.recent, k)
		}
	}
	s.frame++
	if s.frame%64 != 0 && len(s.layouts) < 4096 {
		return
	}
	for p, c := range s.layouts {
		if s.frame-c.used > 240 {
			delete(s.layouts, p)
		}
	}
}

// Layout shapes and wraps p.Text. The result is shared and must not be
// modified.
func (s *System) Layout(p Params) *Layout {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.layouts[p]; ok {
		c.used = s.frame
		return c.layout
	}
	l := s.layout(p)
	s.layouts[p] = &cached{l, s.frame}
	return l
}

// Metrics returns the ascent, descent and default line height of a style.
func (s *System) Metrics(style Style) (ascent, descent, lineHeight float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := metricsOf(s.font(style), style)
	return m.ascent, m.descent, m.lineHeight
}

// font returns the font of a style.
func (s *System) font(style Style) *Font {
	key := Style{Family: style.Family, Size: style.FontSize(), Weight: style.weight(), Italic: style.Italic}
	if f, ok := s.fonts[key]; ok {
		return f
	}
	f := s.engine().font(key)
	if len(s.fonts) >= 1024 {
		clear(s.fonts)
	}
	s.fonts[key] = f
	return f
}

// lineMetrics are the vertical metrics of a style's lines.
type lineMetrics struct {
	ascent, descent, lineHeight float32
}

func metricsOf(f *Font, style Style) lineMetrics {
	size := style.FontSize()
	m := lineMetrics{ascent: size * 0.8, descent: size * 0.2}
	gap := float32(0)
	if f != nil && f.Ascent > 0 {
		m.ascent, m.descent, gap = f.Ascent, f.Descent, max(f.LineGap, 0)
	}
	m.lineHeight = m.ascent + m.descent + gap
	if style.LineHeight > 0 {
		m.lineHeight = style.LineHeight * size
	}
	return m
}

func (s *System) layout(p Params) *Layout {
	m := metricsOf(s.font(p.Style), p.Style)
	runes := []rune(p.Text)
	l := &Layout{Params: p, Runes: runes}
	y := float32(0)
	for start := 0; start <= len(runes); {
		end := start
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		maxLines := 0
		if p.MaxLines > 0 {
			maxLines = p.MaxLines - len(l.Lines)
		}
		lines, truncated := s.paragraph(p, runes, start, end, maxLines, end < len(runes), m, &y)
		l.Lines = append(l.Lines, lines...)
		if truncated {
			l.Truncated = true
			break
		}
		start = end + 1
	}
	for _, line := range l.Lines {
		l.Width = max(l.Width, line.Width)
	}
	l.Height = y
	// Align each line within the wrap width, or the longest line.
	box := l.Width
	if p.Width > 0 {
		box = p.Width
	}
	for i := range l.Lines {
		line := &l.Lines[i]
		var dx float32
		align := p.Align
		if line.RTL {
			switch align {
			case Start:
				align = End
			case End:
				align = Start
			}
		}
		switch align {
		case Center:
			dx = (box - line.Width) / 2
		case End:
			dx = box - line.Width
		}
		if dx != 0 {
			line.X += dx
			for j := range line.Glyphs {
				line.Glyphs[j].X += dx
			}
		}
	}
	return l
}

// paragraph lays out runes[start:end], a paragraph without newlines, in at
// most maxLines lines (0 is unlimited) from *y down, and moves *y past it.
// It reports whether it cut the text, which continues after the paragraph
// when continues is set.
func (s *System) paragraph(p Params, runes []rune, start, end, maxLines int, continues bool, m lineMetrics, y *float32) ([]Line, bool) {
	text := runes[start:end]
	// A carriage return before the newline is part of it.
	if n := len(text); n > 0 && text[n-1] == '\r' {
		text = text[:n-1]
	}
	rtl := isRTL(text)
	var shaped []shapedLine
	if len(text) > 0 {
		shaped = s.engine().shape(text, p.Style, max(p.Width, 0), rtl, p.NoBreakWords)
	}
	if len(shaped) == 0 {
		shaped = []shapedLine{{end: len(text)}}
	}
	truncated := false
	if maxLines > 0 && (len(shaped) > maxLines || len(shaped) == maxLines && continues) {
		last := s.ellipsize(text, shaped[maxLines-1].start, p, rtl)
		shaped = append(shaped[:maxLines-1], last)
		truncated = true
	}
	lines := make([]Line, len(shaped))
	for i, sl := range shaped {
		lines[i] = line(p, text, sl, start, rtl, m, y)
	}
	// Runes between lines, if an engine left any out, belong to the line
	// before.
	for i := 0; i < len(lines)-1; i++ {
		lines[i].End = max(lines[i].End, lines[i+1].Start)
	}
	if !truncated {
		lines[len(lines)-1].End = end
	}
	return lines, truncated
}

// line positions a line of the paragraph text, which starts at rune
// offset of the layout, with its top at *y, and moves *y past it.
func line(p Params, text []rune, sl shapedLine, offset int, rtl bool, m lineMetrics, y *float32) Line {
	line := Line{Start: offset + sl.start, End: offset + sl.end, RTL: rtl, Ascent: m.ascent, Descent: m.descent}
	// Whitespace ending a line takes no room, unless kept.
	trim := sl.end
	if !p.KeepSpaces {
		for trim > sl.start && unicode.IsSpace(text[trim-1]) {
			trim--
		}
	}
	size := p.Style.FontSize()
	x0, x1 := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	var starts []int
	for _, run := range sl.runs {
		if f := run.font; f != nil {
			line.Ascent = max(line.Ascent, f.Ascent)
			line.Descent = max(line.Descent, f.Descent)
		}
		// A cluster holds the runes up to the next one of its run.
		starts = starts[:0]
		for _, g := range run.glyphs {
			if g.Cluster >= 0 {
				starts = append(starts, g.Cluster)
			}
		}
		slices.Sort(starts)
		starts = slices.Compact(starts)
		for _, g := range run.glyphs {
			if g.Cluster < 0 { // the ellipsis
				g.Cluster = line.End
			} else {
				if g.Cluster >= trim && g.Cluster < sl.end {
					continue
				}
				next := run.end
				if i, _ := slices.BinarySearch(starts, g.Cluster); i+1 < len(starts) {
					next = starts[i+1]
				}
				g.Runes = max(next-g.Cluster, 1)
				g.Cluster += offset
			}
			g.Size = size
			x0, x1 = min(x0, g.X), max(x1, g.X+g.Advance)
			line.Glyphs = append(line.Glyphs, g)
		}
	}
	if len(line.Glyphs) == 0 {
		x0, x1 = 0, 0
	}
	line.Width = x1 - x0
	line.Height = max(m.lineHeight, line.Ascent+line.Descent)
	line.Y = *y
	line.Baseline = *y + (line.Height-line.Ascent-line.Descent)/2 + line.Ascent
	for i := range line.Glyphs {
		line.Glyphs[i].X -= x0
		line.Glyphs[i].Y += line.Baseline
	}
	*y += line.Height
	return line
}

// ellipsize lays out the paragraph text from rune start on one line ending
// with an ellipsis: as many of its graphemes as fit the width with it.
func (s *System) ellipsize(text []rune, start int, p Params, rtl bool) shapedLine {
	e := s.engine()
	rest := text[start:]
	cut := len(rest)
	if p.Width > 0 {
		var ellipsis float32
		for _, l := range e.shape([]rune{'…'}, p.Style, 0, rtl, false) {
			ellipsis = max(ellipsis, advance(l))
		}
		// The advance of each cluster, at its first rune.
		advances := make([]float32, len(rest))
		for _, l := range e.shape(rest, p.Style, 0, rtl, false) {
			for _, run := range l.runs {
				for _, g := range run.glyphs {
					if g.Cluster >= 0 && g.Cluster < len(rest) {
						advances[g.Cluster] += g.Advance
					}
				}
			}
		}
		var b Boundaries
		b.Reset(rest)
		w := float32(0)
		cut = 0
		for cut < len(rest) {
			next := b.NextGrapheme(cut)
			gw := float32(0)
			for _, a := range advances[cut:next] {
				gw += a
			}
			if w+gw+ellipsis > p.Width {
				break
			}
			w += gw
			cut = next
		}
	}
	for cut > 0 && unicode.IsSpace(rest[cut-1]) {
		cut--
	}
	t := make([]rune, cut+1)
	copy(t, rest[:cut])
	t[cut] = '…'
	out := shapedLine{start: start, end: start + cut}
	for _, l := range e.shape(t, p.Style, 0, rtl, false) {
		for _, run := range l.runs {
			run.start, run.end = start+min(run.start, cut), start+min(run.end, cut)
			for i := range run.glyphs {
				if g := &run.glyphs[i]; g.Cluster >= cut {
					g.Cluster = -1
				} else {
					g.Cluster += start
				}
			}
			out.runs = append(out.runs, run)
		}
	}
	return out
}

// advance returns the width a shaped line's glyphs take.
func advance(l shapedLine) float32 {
	x0, x1 := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for _, run := range l.runs {
		for _, g := range run.glyphs {
			x0, x1 = min(x0, g.X), max(x1, g.X+g.Advance)
		}
	}
	return max(x1-x0, 0)
}

// isRTL reports whether a paragraph is right-to-left: whether its first
// strongly directional rune is.
func isRTL(para []rune) bool {
	for _, r := range para {
		switch {
		case unicode.In(r, unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko, unicode.Samaritan, unicode.Mandaic, unicode.Adlam):
			return true
		case unicode.IsLetter(r):
			return false
		}
	}
	return false
}
