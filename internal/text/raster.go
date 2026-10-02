package text

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/jpeg" // bitmap glyphs
	_ "image/png"  // bitmap glyphs
	"math"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// SubpixelSteps is the number of horizontal positions within a pixel a
// glyph is rasterized for.
const SubpixelSteps = 4

type glyphKey struct {
	face  *font.Face
	id    font.GID
	size  uint32 // pixels, in 1/16
	subX  uint8
	color bool
}

// GlyphImage is a rasterized glyph in an atlas.
type GlyphImage struct {
	// OK is false for glyphs without pixels, such as spaces.
	OK bool
	// Colored glyphs are in the color atlas, the others in the mask atlas.
	Colored bool
	// X, Y, W and H are the bitmap's rectangle in its atlas.
	X, Y, W, H uint16
	// Left and Top place the bitmap relative to the glyph's origin on the
	// baseline, in pixels: Top is usually negative.
	Left, Top float32
}

// atlasEntry is a cached glyph or mask.
type atlasEntry struct {
	GlyphImage
	used uint64 // the last frame that drew it
}

// BeginFrame starts painting a frame: the masks drawn for the previous
// frame alone are forgotten.
func (s *System) BeginFrame() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MaskAtlas.ResetTransient()
	clear(s.transient)
	s.full, s.want = [2]bool{}, [2]int{}
	s.rooms = 0
}

// Full reports whether an atlas filled up during the frame being painted
// and left out glyphs or masks it draws. MakeRoom then makes room for them.
func (s *System) Full() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.full[0] || s.full[1]
}

// MakeRoom makes room in the atlases that filled up: it keeps what the
// frame being painted drew, growing the atlas when that and what the frame
// still needs take much of it, or when room made for the frame before did
// not suffice, and forgets everything else. Everything kept moves, so the
// frame must be painted again.
func (s *System) MakeRoom() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms++
	for i, color := range [2]bool{false, true} {
		if s.full[i] {
			s.makeRoom(color, s.want[i], s.rooms > 1)
		}
	}
	s.full, s.want = [2]bool{}, [2]int{}
}

func (s *System) makeRoom(color bool, want int, grow bool) {
	a := s.MaskAtlas
	if color {
		a = s.ColorAtlas
	}
	var keep []*atlasEntry
	need := want
	if !color {
		for _, g := range s.transient {
			need += (int(g.W) + 1) * (int(g.H) + 1)
		}
		clear(s.transient)
		a.ResetTransient()
		for k, e := range s.masks {
			if e.used != s.frame {
				delete(s.masks, k)
				continue
			}
			keep = append(keep, e)
		}
	}
	for k, e := range s.glyphs {
		if !e.OK || e.Colored != color {
			continue // blank glyphs take no room
		}
		if e.used != s.frame {
			delete(s.glyphs, k)
			continue
		}
		keep = append(keep, e)
	}
	rects := make([]image.Rectangle, len(keep))
	for i, e := range keep {
		rects[i] = image.Rect(int(e.X), int(e.Y), int(e.X)+int(e.W), int(e.Y)+int(e.H))
		need += (int(e.W) + 1) * (int(e.H) + 1)
	}
	// Shelves waste room: keep the atlas at most half full.
	if grow {
		a.Grow()
	}
	for need*2 > a.W*a.H && a.Grow() {
	}
	pos, ok := a.Repack(rects)
	if !ok {
		a.Reset()
		for k, e := range s.glyphs {
			if e.OK && e.Colored == color {
				delete(s.glyphs, k)
			}
		}
		if !color {
			clear(s.masks)
		}
		return
	}
	for i, e := range keep {
		e.X, e.Y = uint16(pos[i].X), uint16(pos[i].Y)
	}
}

// Glyph rasterizes a glyph at size pixels (its font size times the
// display's scale), shifted right by subX/SubpixelSteps of a pixel.
func (s *System) Glyph(face *font.Face, id font.GID, size float32, subX int) GlyphImage {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := glyphKey{face: face, id: id, size: uint32(size*16 + 0.5), subX: uint8(subX)}
	if e, ok := s.glyphs[key]; ok {
		e.used = s.frame
		return e.GlyphImage
	}
	failed := s.failed
	g := s.rasterize(face, id, size, float32(subX)/SubpixelSteps)
	if s.failed == failed { // not left out of a full atlas
		s.glyphs[key] = &atlasEntry{g, s.frame}
	}
	return g
}

func (s *System) rasterize(face *font.Face, id font.GID, size, dx float32) GlyphImage {
	if face == nil {
		return GlyphImage{}
	}
	switch data := face.GlyphData(id).(type) {
	case font.GlyphOutline:
		return s.outline(face, data, size, dx)
	case font.GlyphBitmap:
		if g := s.bitmap(face, id, data, size); g.OK {
			return g
		}
		if data.Outline != nil {
			return s.outline(face, *data.Outline, size, dx)
		}
	case font.GlyphSVG:
		return s.outline(face, data.Outline, size, dx)
	}
	// Color (COLR) glyphs draw their monochrome outline.
	if o, ok := face.GlyphDataOutline(id); ok {
		return s.outline(face, o, size, dx)
	}
	return GlyphImage{}
}

func (s *System) outline(face *font.Face, o font.GlyphOutline, size, dx float32) GlyphImage {
	if len(o.Segments) == 0 {
		return GlyphImage{}
	}
	scale := size / float32(face.Upem())
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	for _, seg := range o.Segments {
		for _, p := range seg.ArgsSlice() {
			x, y := p.X*scale+dx, -p.Y*scale
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	x0, y0 := int(math.Floor(float64(minX))), int(math.Floor(float64(minY)))
	x1, y1 := int(math.Ceil(float64(maxX))), int(math.Ceil(float64(maxY)))
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 || w > 2048 || h > 2048 {
		return GlyphImage{}
	}
	r := &s.raster
	r.Reset(w, h)
	ox, oy := -float32(x0)+dx, -float32(y0)
	pt := func(p font.SegmentPoint) (float32, float32) { return p.X*scale + ox, -p.Y*scale + oy }
	for _, seg := range o.Segments {
		a := seg.Args
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			r.MoveTo(pt(a[0]))
		case ot.SegmentOpLineTo:
			x, y := pt(a[0])
			r.LineTo(x, y)
		case ot.SegmentOpQuadTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			r.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			dx, dy := pt(a[2])
			r.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	r.Mask(mask.Pix, mask.Stride)
	ax, ay, ok := s.alloc(false, w, h)
	if !ok {
		return GlyphImage{}
	}
	s.MaskAtlas.Put(ax, ay, w, h, mask.Pix, mask.Stride)
	return GlyphImage{OK: true, X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h), Left: float32(x0), Top: float32(y0)}
}

// bitmap scales a bitmap glyph (a PNG emoji) to the glyph's box.
func (s *System) bitmap(face *font.Face, id font.GID, data font.GlyphBitmap, size float32) GlyphImage {
	if data.Format != font.PNG && data.Format != font.JPG {
		return GlyphImage{}
	}
	src, _, err := image.Decode(bytes.NewReader(data.Data))
	if err != nil {
		return GlyphImage{}
	}
	ext, ok := face.GlyphExtents(id)
	scale := size / float32(face.Upem())
	left, top := float32(0), -size*0.8
	w, h := int(size+0.5), int(size+0.5)
	if ok && ext.Width > 0 && ext.Height != 0 {
		left, top = ext.XBearing*scale, -ext.YBearing*scale
		w, h = int(ext.Width*scale+0.5), int(-ext.Height*scale+0.5)
	}
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return GlyphImage{}
	}
	dst := scaleRGBA(src, w, h)
	ax, ay, okAlloc := s.alloc(true, w, h)
	if !okAlloc {
		return GlyphImage{}
	}
	s.ColorAtlas.Put(ax, ay, w, h, dst.Pix, dst.Stride)
	return GlyphImage{OK: true, Colored: true, X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h), Left: float32(math.Round(float64(left))), Top: float32(math.Round(float64(top)))}
}

// alloc finds lasting room in an atlas, or records that it is full.
func (s *System) alloc(color bool, w, h int) (int, int, bool) {
	a := s.MaskAtlas
	if color {
		a = s.ColorAtlas
	}
	if x, y, ok := a.Alloc(w, h); ok {
		return x, y, true
	}
	s.leftOut(color, w, h)
	return 0, 0, false
}

func (s *System) leftOut(color bool, w, h int) {
	i := 0
	if color {
		i = 1
	}
	s.full[i] = true
	s.want[i] += (w + 1) * (h + 1)
	s.failed++
}

// scaleRGBA scales src to a w×h premultiplied RGBA image, averaging the
// source pixels each destination pixel covers when shrinking and
// interpolating bilinearly when growing.
func scaleRGBA(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	in := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(in, in.Bounds(), src, b.Min, draw.Src)
	sw, sh := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	fx, fy := float32(sw)/float32(w), float32(sh)/float32(h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c [4]float32
			if fx >= 1 && fy >= 1 {
				// Box filter over the covered source pixels.
				x0, x1 := int(float32(x)*fx), max(int(float32(x+1)*fx), int(float32(x)*fx)+1)
				y0, y1 := int(float32(y)*fy), max(int(float32(y+1)*fy), int(float32(y)*fy)+1)
				x1, y1 = min(x1, sw), min(y1, sh)
				n := float32(0)
				for sy := y0; sy < y1; sy++ {
					for sx := x0; sx < x1; sx++ {
						p := in.Pix[sy*in.Stride+sx*4:]
						c[0] += float32(p[0])
						c[1] += float32(p[1])
						c[2] += float32(p[2])
						c[3] += float32(p[3])
						n++
					}
				}
				for i := range c {
					c[i] /= max(n, 1)
				}
			} else {
				sx := (float32(x)+0.5)*fx - 0.5
				sy := (float32(y)+0.5)*fy - 0.5
				x0, y0 := int(math.Floor(float64(sx))), int(math.Floor(float64(sy)))
				tx, ty := sx-float32(x0), sy-float32(y0)
				at := func(px, py int) []uint8 {
					px, py = max(0, min(px, sw-1)), max(0, min(py, sh-1))
					return in.Pix[py*in.Stride+px*4:]
				}
				p00, p10, p01, p11 := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
				for i := 0; i < 4; i++ {
					top := float32(p00[i])*(1-tx) + float32(p10[i])*tx
					bot := float32(p01[i])*(1-tx) + float32(p11[i])*tx
					c[i] = top*(1-ty) + bot*ty
				}
			}
			o := out.Pix[y*out.Stride+x*4:]
			for i := 0; i < 4; i++ {
				o[i] = uint8(min(c[i]+0.5, 255))
			}
		}
	}
	return out
}

// Mask returns a coverage mask cached under key in the mask atlas,
// drawing it when it is not: draw returns its size and one byte per pixel.
// Icons and other vector shapes use it.
//
// A mask a frame draws for the first time may be one frame of an
// animation, never drawn again: it lasts for that frame only, in the
// atlas's transient room, and is cached once a later frame draws it too.
func (s *System) Mask(key uint64, draw func() (w, h int, pix []byte)) GlyphImage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.masks[key]; ok {
		e.used = s.frame
		return e.GlyphImage
	}
	if g, ok := s.transient[key]; ok {
		return g
	}
	last, seen := s.recent[key]
	lasting := seen && last != s.frame
	w, h, pix := draw()
	if w <= 0 || h <= 0 {
		return GlyphImage{}
	}
	var x, y int
	var ok bool
	if lasting {
		x, y, ok = s.MaskAtlas.Alloc(w, h)
	} else {
		x, y, ok = s.MaskAtlas.AllocTransient(w, h)
	}
	if !ok {
		s.leftOut(false, w, h)
		return GlyphImage{}
	}
	s.MaskAtlas.Put(x, y, w, h, pix, w)
	g := GlyphImage{OK: true, X: uint16(x), Y: uint16(y), W: uint16(w), H: uint16(h)}
	if lasting {
		delete(s.recent, key)
		s.masks[key] = &atlasEntry{g, s.frame}
	} else {
		s.transient[key] = g
		s.recent[key] = s.frame
	}
	return g
}
