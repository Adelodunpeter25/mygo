package ui

import "testing"

// BenchmarkFrame builds, lays out, paints and renders on the CPU a whole
// frame of a 980×720 window at twice the density, as on macOS and Linux.
func BenchmarkFrame(b *testing.B) {
	d := &demo{choice: "a", size: "Medium", volume: 40}
	tt := NewTester(d.view, 980, 720)
	tt.SetScale(2)
	for b.Loop() {
		tt.h.img.Invalidate()
		tt.Frame()
	}
}

// BenchmarkFrameHover renders the frames of the pointer going over a
// button and off it: the CPU renderer redraws the button alone.
func BenchmarkFrameHover(b *testing.B) {
	d := &demo{choice: "a", size: "Medium", volume: 40}
	tt := NewTester(d.view, 980, 720)
	tt.SetScale(2)
	r, _ := tt.Find("Increment")
	on := false
	for b.Loop() {
		if on = !on; on {
			tt.Move(r.X+5, r.Y+5)
		} else {
			tt.Move(r.X-5, r.Y-5)
		}
	}
}
