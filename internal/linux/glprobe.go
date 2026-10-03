//go:build linux && (amd64 || arm64)

package linux

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Surfaces draw with OpenGL only where it runs on a GPU. A software
// renderer, such as Mesa's llvmpipe in virtual machines and in WSL, redraws
// the whole window on the CPU every frame, many times the work of drawing
// in memory, which redraws what changed; and once a window has a GL
// context, GTK composites the window with OpenGL, so the choice is made
// before: with a context GDK makes for a window that never shows, as the
// surfaces' would be.

var glProbe struct {
	once sync.Once
	gpu  bool
}

// gdkWindowAttr is GdkWindowAttr, for gdk_window_new.
type gdkWindowAttr struct {
	title                     ptr
	eventMask                 int32
	x, y, width, height       int32
	wclass                    int32
	visual                    ptr
	windowType                int32
	cursor                    ptr
	wmclassName, wmclassClass ptr
	overrideRedirect          int32
	typeHint                  int32
}

// gpuGL reports whether GDK's OpenGL contexts draw on a GPU.
func gpuGL() bool {
	glProbe.once.Do(func() {
		// MYGO_GPU=1 draws with OpenGL wherever GDK makes a context, on
		// the CPU too: tests of the GL surface run so without a GPU.
		if os.Getenv("MYGO_GPU") == "1" {
			glProbe.gpu = true
			return
		}
		// Without a GPU device, GL draws on the CPU: the probe, whose
		// driver stays loaded, would cost memory to say so.
		if !gpuDevice() {
			log.Print("mygo: native UI draws without the GPU: there is none")
			return
		}
		renderer, err := probeGL()
		switch {
		case err != "":
			log.Printf("mygo: native UI draws without the GPU: %s", err)
		case softwareGL(renderer):
			log.Printf("mygo: native UI draws without the GPU: OpenGL draws on the CPU (%s)", renderer)
		default:
			glProbe.gpu = true
		}
	})
	return glProbe.gpu
}

func softwareGL(renderer string) bool {
	r := strings.ToLower(renderer)
	for _, s := range []string{"llvmpipe", "softpipe", "swrast", "software rasterizer"} {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
}

// probeGL makes an OpenGL context as a GtkGLArea would and returns the name
// of its renderer, or why there is none.
func probeGL() (renderer, failure string) {
	var (
		windowNew      func(parent ptr, attr *gdkWindowAttr, mask int32) ptr
		windowDestroy  func(w ptr)
		createContext  func(w ptr, err *ptr) ptr
		requireVersion func(c ptr, major, minor int32)
		realizeContext func(c ptr, err *ptr) bool
		makeCurrent    func(c ptr)
		clearCurrent   func()
		getString      func(name uint32) ptr
		haveSyms       = true
	)
	for name, fn := range map[string]any{
		"gdk_window_new": &windowNew, "gdk_window_destroy": &windowDestroy, "gdk_window_create_gl_context": &createContext,
		"gdk_gl_context_set_required_version": &requireVersion, "gdk_gl_context_realize": &realizeContext,
		"gdk_gl_context_make_current": &makeCurrent, "gdk_gl_context_clear_current": &clearCurrent,
	} {
		haveSyms = bind(libGDK, fn, name) && haveSyms
	}
	if !haveSyms {
		return "", "GDK has no OpenGL"
	}
	epoxy, err := purego.Dlopen("libepoxy.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return "", "no libepoxy"
	}
	// libepoxy exports a pointer to each GL function.
	sym, err := purego.Dlsym(epoxy, "epoxy_glGetString")
	if err != nil || sym == 0 {
		return "", "no glGetString"
	}
	purego.RegisterFunc(&getString, **(**uintptr)(unsafe.Pointer(&sym)))

	const inputOutput, toplevel = 0, 1
	win := windowNew(0, &gdkWindowAttr{width: 1, height: 1, wclass: inputOutput, windowType: toplevel}, 0)
	if win == 0 {
		return "", "cannot make a window"
	}
	defer func() {
		windowDestroy(win)
		gObjectUnref(win) // with the GL context GDK made for painting it
	}()
	var gerr ptr
	ctx := createContext(win, &gerr)
	if ctx == 0 {
		return "", failed(gerr)
	}
	defer gObjectUnref(ctx)
	requireVersion(ctx, 3, 3)
	if !realizeContext(ctx, &gerr) {
		return "", failed(gerr)
	}
	makeCurrent(ctx)
	defer clearCurrent()
	const glRenderer = 0x1F01
	return goStr(getString(glRenderer)), ""
}

// failed describes a GError of GDK's OpenGL.
func failed(gerr ptr) string {
	if err := gErr(gerr); err != nil {
		return err.Error()
	}
	return "no OpenGL context"
}

// gpuDevice reports whether the system has a device a GPU driver could
// use: a DRM node, NVIDIA's, or WSL's.
func gpuDevice() bool {
	for _, pattern := range []string{"/dev/dri/*", "/dev/nvidia*", "/dev/dxg"} {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			return true
		}
	}
	return false
}
