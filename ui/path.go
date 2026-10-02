package ui

import (
	"encoding/binary"
	"hash/maphash"
	"math"

	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/vec"
)

// Path is a vector shape, in DIPs, for Painter.FillPath and StrokePath:
// icons, check marks, charts.
type Path struct {
	cmds []pathCmd
}

type pathCmd struct {
	op  uint8 // 0 move, 1 line, 2 quad, 3 cubic, 4 close
	pts [3][2]float32
}

// MoveTo starts a new subpath at (x, y).
func (p *Path) MoveTo(x, y float32) *Path {
	p.cmds = append(p.cmds, pathCmd{op: 0, pts: [3][2]float32{{x, y}}})
	return p
}

// LineTo draws a line to (x, y).
func (p *Path) LineTo(x, y float32) *Path {
	p.cmds = append(p.cmds, pathCmd{op: 1, pts: [3][2]float32{{x, y}}})
	return p
}

// QuadTo draws a quadratic Bézier curve to (x, y) with control point
// (cx, cy).
func (p *Path) QuadTo(cx, cy, x, y float32) *Path {
	p.cmds = append(p.cmds, pathCmd{op: 2, pts: [3][2]float32{{cx, cy}, {x, y}}})
	return p
}

// CubeTo draws a cubic Bézier curve to (x, y) with control points (c1x,
// c1y) and (c2x, c2y).
func (p *Path) CubeTo(c1x, c1y, c2x, c2y, x, y float32) *Path {
	p.cmds = append(p.cmds, pathCmd{op: 3, pts: [3][2]float32{{c1x, c1y}, {c2x, c2y}, {x, y}}})
	return p
}

// Close closes the subpath.
func (p *Path) Close() *Path {
	p.cmds = append(p.cmds, pathCmd{op: 4})
	return p
}

// Circle adds a circle as a subpath.
func (p *Path) Circle(cx, cy, r float32) *Path {
	const k = 0.5522847498
	p.MoveTo(cx+r, cy)
	p.CubeTo(cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r)
	p.CubeTo(cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy)
	p.CubeTo(cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r)
	p.CubeTo(cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy)
	return p.Close()
}

// flatten returns the path's subpaths as polylines in device pixels.
func (p *Path) flatten(scale float32) (polys [][][2]float32, closed []bool) {
	var cur [][2]float32
	isClosed := false
	var start, pen [2]float32
	flush := func() {
		if len(cur) > 1 {
			polys = append(polys, cur)
			closed = append(closed, isClosed)
		}
		cur, isClosed = nil, false
	}
	for _, c := range p.cmds {
		pts := c.pts
		for i := range pts {
			pts[i][0] *= scale
			pts[i][1] *= scale
		}
		switch c.op {
		case 0:
			flush()
			start, pen = pts[0], pts[0]
			cur = append(cur, pen)
		case 1:
			pen = pts[0]
			cur = append(cur, pen)
		case 2:
			n := curveSteps(pen, pts[0], pts[1], pts[1])
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				cur = append(cur, [2]float32{u*u*pen[0] + 2*u*t*pts[0][0] + t*t*pts[1][0], u*u*pen[1] + 2*u*t*pts[0][1] + t*t*pts[1][1]})
			}
			pen = pts[1]
		case 3:
			n := curveSteps(pen, pts[0], pts[1], pts[2])
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				a, b, cc, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
				cur = append(cur, [2]float32{a*pen[0] + b*pts[0][0] + cc*pts[1][0] + d*pts[2][0], a*pen[1] + b*pts[0][1] + cc*pts[1][1] + d*pts[2][1]})
			}
			pen = pts[2]
		case 4:
			if len(cur) > 0 {
				isClosed = true
				flush()
				pen = start
			}
		}
	}
	flush()
	return polys, closed
}

func curveSteps(a, b, c, d [2]float32) int {
	l := dist(a, b) + dist(b, c) + dist(c, d)
	return max(2, min(int(math.Sqrt(float64(l))*2), 64))
}

func dist(a, b [2]float32) float32 {
	return float32(math.Hypot(float64(a[0]-b[0]), float64(a[1]-b[1])))
}

var pathSeed = maphash.MakeSeed()

// drawPath rasterizes polygons (device pixels) into a cached mask and
// paints it with c.
func (p *Painter) drawPath(polys [][][2]float32, key uint64, c Color) {
	if len(polys) == 0 {
		return
	}
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	for _, poly := range polys {
		for _, pt := range poly {
			minX, maxX = min(minX, pt[0]), max(maxX, pt[0])
			minY, maxY = min(minY, pt[1]), max(maxY, pt[1])
		}
	}
	x0, y0 := float32(math.Floor(float64(minX))), float32(math.Floor(float64(minY)))
	w, h := int(math.Ceil(float64(maxX-x0)))+1, int(math.Ceil(float64(maxY-y0)))+1
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return
	}
	// The key covers the shape relative to the pixel grid.
	var buf [8]byte
	var hs maphash.Hash
	hs.SetSeed(pathSeed)
	binary.LittleEndian.PutUint64(buf[:], key)
	hs.Write(buf[:])
	for _, poly := range polys {
		for _, pt := range poly {
			binary.LittleEndian.PutUint32(buf[:4], math.Float32bits(round((pt[0]-x0)*4)))
			binary.LittleEndian.PutUint32(buf[4:], math.Float32bits(round((pt[1]-y0)*4)))
			hs.Write(buf[:])
		}
		hs.Write([]byte{0xff})
	}
	gi := p.rt.text.Mask(hs.Sum64(), func() (int, int, []byte) {
		var z vec.Rasterizer
		z.Reset(w, h)
		for _, poly := range polys {
			z.MoveTo(poly[0][0]-x0, poly[0][1]-y0)
			for _, pt := range poly[1:] {
				z.LineTo(pt[0]-x0, pt[1]-y0)
			}
			z.ClosePath()
		}
		pix := make([]byte, w*h)
		z.Mask(pix, w)
		return w, h, pix
	})
	if !gi.OK {
		return
	}
	start := int32(len(p.s.Glyphs))
	p.s.Glyphs = append(p.s.Glyphs, scene.Glyph{X: x0, Y: y0, W: float32(gi.W), H: float32(gi.H), U: gi.X, V: gi.Y, UW: gi.W, VH: gi.H, Color: c.Alpha(p.opacity).scene()})
	p.s.Ops = append(p.s.Ops, scene.Op{Kind: scene.OpGlyphs, Start: start, End: start + 1})
}

// FillPath fills a path (non-zero winding).
func (p *Painter) FillPath(path *Path, c Color) {
	polys, _ := path.flatten(p.scale)
	p.drawPath(polys, 1, c)
}

// StrokePath draws the outline of a path, width DIPs wide, with round
// joins and caps.
func (p *Painter) StrokePath(path *Path, width float32, c Color) {
	polys, closed := path.flatten(p.scale)
	hw := width * p.scale / 2
	var out [][][2]float32
	for i, poly := range polys {
		n := len(poly)
		segs := n - 1
		if closed[i] {
			segs = n
		}
		for s := 0; s < segs; s++ {
			a, b := poly[s], poly[(s+1)%n]
			dx, dy := b[0]-a[0], b[1]-a[1]
			l := float32(math.Hypot(float64(dx), float64(dy)))
			if l == 0 {
				continue
			}
			nx, ny := -dy/l*hw, dx/l*hw
			out = append(out, orient([][2]float32{{a[0] + nx, a[1] + ny}, {b[0] + nx, b[1] + ny}, {b[0] - nx, b[1] - ny}, {a[0] - nx, a[1] - ny}}))
		}
		for _, pt := range poly {
			out = append(out, disc(pt, hw))
		}
	}
	p.drawPath(out, 2+uint64(math.Float32bits(hw))<<8, c)
}

// orient makes a polygon counterclockwise, so that overlapping pieces of
// a stroke add up instead of cancelling.
func orient(poly [][2]float32) [][2]float32 {
	var area float32
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		area += a[0]*b[1] - b[0]*a[1]
	}
	if area < 0 {
		for i, j := 0, len(poly)-1; i < j; i, j = i+1, j-1 {
			poly[i], poly[j] = poly[j], poly[i]
		}
	}
	return poly
}

func disc(c [2]float32, r float32) [][2]float32 {
	n := max(8, min(int(r*4), 32))
	out := make([][2]float32, n)
	for i := range out {
		a := float64(i) * 2 * math.Pi / float64(n)
		out[i] = [2]float32{c[0] + r*float32(math.Cos(a)), c[1] + r*float32(math.Sin(a))}
	}
	return out
}
