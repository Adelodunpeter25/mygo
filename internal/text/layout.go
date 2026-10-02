package text

import (
	"sort"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"

	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/vec"
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
	return s.Weight
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
	// the glyphs above and below it.
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
	Face *font.Face
	ID   font.GID
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

// System lays out text and rasterizes glyphs. Use Shared.
type System struct {
	mu      sync.Mutex
	fonts   fonts
	shaper  shaping.HarfbuzzShaper
	seg     shaping.Segmenter
	wrapper shaping.LineWrapper
	lang    language.Language

	layouts map[Params]*cached
	frame   uint64

	glyphs map[glyphKey]*atlasEntry
	masks  map[uint64]*atlasEntry
	// transient holds the masks drawn for the frame being painted alone,
	// recent the frame each of them was last drawn in.
	transient map[uint64]GlyphImage
	recent    map[uint64]uint64
	raster    vec.Rasterizer
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
		lang:       language.DefaultLanguage(),
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

// RegisterFont adds a TrueType or OpenType font (or collection) to the
// fonts text can use, under family, or the font's own family name when
// family is empty.
func (s *System) RegisterFont(data []byte, family string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data = append([]byte(nil), data...)
	if s.fonts.fm == nil {
		s.fonts.pending = append(s.fonts.pending, registered{data, family})
		return nil
	}
	if err := addFont(s.fonts.fm, data, family); err != nil {
		return err
	}
	s.fonts.queried = false
	clear(s.layouts)
	return nil
}

// Preload loads the system fonts, which the first layout otherwise waits
// for.
func (s *System) Preload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fonts.load()
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
	s.fonts.load()
	s.fonts.setQuery(style)
	face := s.fonts.fm.ResolveFace('A')
	a, d, h := faceMetrics(face, style)
	return a, d, h
}

func faceMetrics(face *font.Face, style Style) (ascent, descent, lineHeight float32) {
	size := style.FontSize()
	ascent, descent = size*0.8, size*0.2
	gap := float32(0)
	if face != nil {
		if ext, ok := face.FontHExtents(); ok && ext.Ascender > 0 {
			upem := float32(face.Upem())
			ascent = ext.Ascender / upem * size
			descent = -ext.Descender / upem * size
			gap = max(ext.LineGap/upem*size, 0)
		}
	}
	lineHeight = ascent + descent + gap
	if style.LineHeight > 0 {
		lineHeight = style.LineHeight * size
	}
	return ascent, descent, lineHeight
}

func toFixed(v float32) fixed.Int26_6 { return fixed.Int26_6(v*64 + 0.5) }

func fromFixed(v fixed.Int26_6) float32 { return float32(v) / 64 }

func (s *System) layout(p Params) *Layout {
	s.fonts.load()
	s.fonts.setQuery(p.Style)
	size := p.Style.FontSize()
	primary := s.fonts.fm.ResolveFace('A')
	ascent, descent, lineHeight := faceMetrics(primary, p.Style)

	runes := []rune(p.Text)
	l := &Layout{Params: p, Runes: runes}
	y := float32(0)
	for start := 0; start <= len(runes); {
		end := start
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		remaining := 0
		if p.MaxLines > 0 {
			remaining = p.MaxLines - len(l.Lines)
		}
		lines, truncated := s.paragraph(p, runes, start, end, primary, ascent, descent, lineHeight, &y, remaining, end < len(runes))
		l.Lines = append(l.Lines, lines...)
		if truncated || (p.MaxLines > 0 && len(l.Lines) >= p.MaxLines && end < len(runes)) {
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
	_ = size
	return l
}

// paragraph lays out runes[start:end], a paragraph without newlines, from
// *y down, and moves *y past it.
func (s *System) paragraph(p Params, runes []rune, start, end int, primary *font.Face, ascent, descent, lineHeight float32, y *float32, maxLines int, continues bool) ([]Line, bool) {
	size := p.Style.FontSize()
	para := runes[start:end]
	dir := direction(para)
	rtl := dir == di.DirectionRTL
	if len(para) == 0 || primary == nil {
		line := Line{Y: *y, Height: lineHeight, Ascent: ascent, Descent: descent, Start: start, End: end, RTL: rtl}
		line.Baseline = *y + (lineHeight-ascent-descent)/2 + ascent
		*y += lineHeight
		return []Line{line}, false
	}
	in := shaping.Input{Text: para, RunEnd: len(para), Direction: dir, Size: toFixed(size), Language: s.lang}
	inputs := s.seg.Split(in, s.fonts.fm)
	outs := make([]shaping.Output, len(inputs))
	for i, input := range inputs {
		outs[i] = s.shaper.Shape(input)
	}
	cfg := shaping.WrapConfig{Direction: dir, DisableTrailingWhitespaceTrim: p.KeepSpaces}
	if p.NoBreakWords {
		cfg.BreakPolicy = shaping.Never
	}
	var ellipsis *shaping.Output
	if maxLines > 0 {
		cfg.TruncateAfterLines = maxLines
		cfg.TextContinues = continues
		cfg = cfg.WithTruncator(&s.shaper, shaping.Input{Text: []rune{'…'}, RunEnd: 1, Direction: dir, Face: primary, Size: toFixed(size), Language: s.lang})
		ellipsis = &cfg.Truncator
	}
	width := p.Width
	if width <= 0 {
		width = 1 << 24
	}
	wrapped, truncated := s.wrapper.WrapParagraphF(cfg, toFixed(width), para, shaping.NewSliceIterator(outs))
	lines := make([]Line, 0, len(wrapped))
	for li, runs := range wrapped {
		line := Line{Y: *y, Start: end, End: start, RTL: rtl, Ascent: ascent, Descent: descent}
		isLast := li == len(wrapped)-1
		visual := make([]int, len(runs))
		for i, run := range runs {
			visual[i] = i
			if isLast && truncated > 0 && ellipsis != nil && i == len(runs)-1 {
				continue
			}
			line.Start = min(line.Start, start+run.Runes.Offset)
			line.End = max(line.End, start+run.Runes.Offset+run.Runes.Count)
			line.Ascent = max(line.Ascent, fromFixed(run.LineBounds.Ascent))
			line.Descent = max(line.Descent, -fromFixed(run.LineBounds.Descent))
		}
		if line.Start > line.End {
			line.Start, line.End = start, start
		}
		sort.SliceStable(visual, func(a, b int) bool { return runs[visual[a]].VisualIndex < runs[visual[b]].VisualIndex })
		line.Height = max(lineHeight, line.Ascent+line.Descent)
		line.Baseline = *y + (line.Height-line.Ascent-line.Descent)/2 + line.Ascent
		pen := float32(0)
		for _, i := range visual {
			run := runs[i]
			isEllipsis := isLast && truncated > 0 && ellipsis != nil && i == len(runs)-1
			runRTL := run.Direction.Progression() == di.TowardTopLeft
			for _, g := range run.Glyphs {
				gl := Glyph{
					Face: run.Face, ID: g.GlyphID, Size: size,
					X:       pen + fromFixed(g.XOffset),
					Y:       line.Baseline - fromFixed(g.YOffset),
					Advance: fromFixed(g.Advance),
					Cluster: start + g.ClusterIndex, Runes: g.RuneCount,
					RTL: runRTL,
				}
				if isEllipsis {
					gl.Cluster, gl.Runes = line.End, 0
				}
				line.Glyphs = append(line.Glyphs, gl)
				pen += fromFixed(g.Advance)
			}
		}
		line.Width = pen
		*y += line.Height
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		line := Line{Y: *y, Height: lineHeight, Ascent: ascent, Descent: descent, Start: start, End: end, RTL: rtl}
		line.Baseline = *y + (lineHeight-ascent-descent)/2 + ascent
		*y += lineHeight
		lines = append(lines, line)
	}
	// Runes the wrapper trimmed (spaces at a soft break) belong to the line
	// before the next one.
	for i := 0; i < len(lines)-1; i++ {
		lines[i].End = max(lines[i].End, lines[i+1].Start)
	}
	if truncated == 0 {
		lines[len(lines)-1].End = end
	}
	return lines, truncated > 0
}

// direction returns the direction of a paragraph: that of its first
// strongly directional rune.
func direction(para []rune) di.Direction {
	for _, r := range para {
		switch {
		case unicode.In(r, unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko, unicode.Samaritan, unicode.Mandaic, unicode.Adlam):
			return di.DirectionRTL
		case unicode.IsLetter(r):
			return di.DirectionLTR
		}
	}
	return di.DirectionLTR
}
