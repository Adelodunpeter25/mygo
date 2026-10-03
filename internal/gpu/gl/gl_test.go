//go:build linux && (amd64 || arm64)

package gl

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/egoist/mygo/internal/gpu/gputest"
)

// EGL enumerations.
const (
	eglPlatformSurfaceless   = 0x31DD // EGL_PLATFORM_SURFACELESS_MESA
	eglNone                  = 0x3038
	eglRenderableType        = 0x3040
	eglOpenGLBit             = 0x0008
	eglSurfaceType           = 0x3033
	eglPbufferBit            = 0x0001
	eglOpenGLAPI             = 0x30A2
	eglContextMajorVersion   = 0x3098
	eglContextMinorVersion   = 0x30FB
	eglContextProfileMask    = 0x30FD
	eglContextCoreProfileBit = 0x1
)

// offscreen makes an OpenGL 3.3 context current on the calling thread,
// without a window, with Mesa's surfaceless EGL platform, and binds a
// framebuffer of w×h pixels. read returns its pixels as the CPU renderer
// draws them: premultiplied BGRA rows from the top.
func offscreen(t *testing.T, w, h int) (read func() []byte) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	lib, err := purego.Dlopen("libEGL.so.1", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip("no EGL:", err)
	}
	var (
		getPlatformDisplay func(platform uint32, native uintptr, attribs unsafe.Pointer) uintptr
		initialize         func(display uintptr, major, minor *int32) uint32
		bindAPI            func(api uint32) uint32
		chooseConfig       func(display uintptr, attribs *int32, configs *uintptr, size int32, n *int32) uint32
		createContext      func(display, config, share uintptr, attribs *int32) uintptr
		makeCurrent        func(display, draw, read, context uintptr) uint32
		destroyContext     func(display, context uintptr) uint32
		terminate          func(display uintptr) uint32
	)
	for name, fn := range map[string]any{
		"eglGetPlatformDisplay": &getPlatformDisplay, "eglInitialize": &initialize, "eglBindAPI": &bindAPI,
		"eglChooseConfig": &chooseConfig, "eglCreateContext": &createContext, "eglMakeCurrent": &makeCurrent,
		"eglDestroyContext": &destroyContext, "eglTerminate": &terminate,
	} {
		sym, err := purego.Dlsym(lib, name)
		if err != nil || sym == 0 {
			t.Skip("EGL has no", name)
		}
		purego.RegisterFunc(fn, sym)
	}
	display := getPlatformDisplay(eglPlatformSurfaceless, 0, nil)
	if display == 0 || initialize(display, nil, nil) == 0 {
		t.Skip("no surfaceless EGL display")
	}
	t.Cleanup(func() { terminate(display) })
	bindAPI(eglOpenGLAPI)
	// Configurations are for windows unless asked otherwise.
	configAttribs := []int32{eglRenderableType, eglOpenGLBit, eglSurfaceType, eglPbufferBit, eglNone}
	var config uintptr
	var n int32
	if chooseConfig(display, &configAttribs[0], &config, 1, &n) == 0 || n == 0 {
		t.Skip("no EGL configuration for OpenGL")
	}
	contextAttribs := []int32{eglContextMajorVersion, 3, eglContextMinorVersion, 3, eglContextProfileMask, eglContextCoreProfileBit, eglNone}
	context := createContext(display, config, 0, &contextAttribs[0])
	if context == 0 {
		t.Skip("no OpenGL 3.3 context")
	}
	t.Cleanup(func() {
		makeCurrent(display, 0, 0, 0)
		destroyContext(display, context)
	})
	if makeCurrent(display, 0, 0, context) == 0 {
		t.Skip("cannot make the context current without a surface")
	}
	if err := load(); err != nil {
		t.Skip(err)
	}
	t.Logf("%s, %s", goString(glGetString(glVersion)), goString(glGetString(glRenderer)))

	tex := newTexture(glRGBA8, glRGBA, w, h, nil, 0)
	var fb uint32
	glGenFramebuffers(1, &fb)
	glBindFramebuffer(glFramebuffer, fb)
	glFramebufferTexture2D(glFramebuffer, glColorAttachment0, glTexture2D, tex, 0)
	if status := glCheckFramebufferState(glFramebuffer); status != glFramebufferDone {
		t.Fatalf("framebuffer incomplete: %#x", status)
	}
	t.Cleanup(func() {
		glBindFramebuffer(glFramebuffer, 0)
		glDeleteFramebuffers(1, &fb)
		glDeleteTextures(1, &tex)
	})
	return func() []byte {
		pix := make([]byte, w*h*4)
		glPixelStorei(glPackAlignment, 1)
		glReadPixels(0, 0, int32(w), int32(h), glRGBA, glUnsignedByte, unsafe.Pointer(&pix[0]))
		// GL's rows go up.
		out := make([]byte, len(pix))
		for y := range h {
			src, dst := pix[(h-1-y)*w*4:][:w*4], out[y*w*4:][:w*4]
			for x := 0; x < w*4; x += 4 {
				dst[x], dst[x+1], dst[x+2], dst[x+3] = src[x+2], src[x+1], src[x], src[x+3]
			}
		}
		return out
	}
}

func TestDrawsAsTheCPURenderer(t *testing.T) {
	s := gputest.Scene()
	read := offscreen(t, s.Width, s.Height)

	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	// Twice: the second frame updates what the first uploaded.
	for range 2 {
		if err := r.Render(s); err != nil {
			t.Fatal(err)
		}
		gputest.Compare(t, "gl", read(), s.Width*4, s)
	}
}
