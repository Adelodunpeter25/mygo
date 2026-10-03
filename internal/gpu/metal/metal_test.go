//go:build darwin

package metal

import (
	"testing"

	"github.com/egoist/mygo/internal/gpu"
	"github.com/egoist/mygo/internal/gpu/gputest"
)

func TestDrawsAsTheCPURenderer(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Skip("no Metal:", err)
	}
	defer r.Release()
	s := gputest.Scene()
	// Twice: the second frame updates what the first uploaded.
	for range 2 {
		pix, err := r.renderOffscreen(s)
		if err != nil {
			t.Fatal(err)
		}
		gputest.Compare(t, "metal", pix, s.Width*4, s)
	}
}

// TestShaderLibrary checks that the library compiled ahead of time comes
// from shader.metal as it is, and that Metal loads it.
func TestShaderLibrary(t *testing.T) {
	if gpu.SourceSum(shaderSource) != shaderLibrarySum {
		t.Fatal("shader.metal changed since shaderlib.go was generated: run go generate ./internal/gpu/metal on macOS")
	}
	r, err := newRenderer()
	if err != nil {
		t.Skip("no Metal:", err)
	}
	defer r.Release()
	var lib id
	pool(func() { lib = r.compiledLibrary() })
	if lib == 0 {
		t.Fatal("Metal does not load the compiled library")
	}
	pool(func() { release(&lib) })
}
