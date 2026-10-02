package ui

import (
	"log"
	"os"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/raster"
	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/surface"
	"github.com/egoist/mygo/internal/text"
)

// Content is a user interface for a window, the value of
// mygo.WindowOptions.Content. Create it with View.
type Content struct {
	view func(*Context)
}

// View returns the content of a window whose user interface view builds,
// for mygo.WindowOptions.Content:
//
//	mygo.NewWindow(mygo.WindowOptions{Title: "Counter", Content: ui.View(app.View)})
//
// view runs on the main thread whenever the window needs a frame: after
// input, after Context.Invalidate or Window.Invalidate, and while
// something animates. A Content can serve several windows, each with its
// own state.
func View(view func(c *Context)) *Content { return &Content{view: view} }

// RegisterFont adds a TrueType or OpenType font, or collection, that text
// can use with Font(family). An empty family keeps the font's own name.
func RegisterFont(data []byte, family string) error {
	return text.Shared().RegisterFont(data, family)
}

// AttachContent connects the content to a window; package mygo calls it.
func (v *Content) AttachContent(conn *surface.Conn) {
	h := &windowHost{conn: conn}
	rt := newRuntime(v.view, h)
	h.rt = rt
	conn.Event = rt.event
	conn.ThemeChanged = rt.themeChanged
	conn.Capture = h.capture
	conn.Detach = h.detach
	// Load the fonts while the window shows up.
	go text.Shared().Preload()
	conn.Surface.RequestFrame()
}

// windowHost presents frames on a window's surface, with a GPU renderer
// when the platform has one and in memory otherwise.
type windowHost struct {
	conn     *surface.Conn
	rt       *engine
	gpu      gpuRenderer
	gpuTried bool
	soft     raster.Renderer
	last     *scene.Scene
}

// gpuRenderer draws scenes into a surface on the GPU.
type gpuRenderer interface {
	Render(s *scene.Scene) error
	Release()
}

func (h *windowHost) size() (float32, float32, float32) {
	w, ht, s := h.conn.Surface.Size()
	if s <= 0 {
		s = 1
	}
	return float32(w), float32(ht), float32(s)
}

func (h *windowHost) present(s *scene.Scene) {
	h.last = s
	if s.Width <= 0 || s.Height <= 0 {
		return
	}
	if !h.gpuTried {
		h.gpuTried = true
		if n := h.conn.Surface.Native(); os.Getenv("MYGO_GPU") != "0" && n != (platform.SurfaceNative{}) {
			r, err := newGPURenderer(n)
			if err != nil {
				log.Printf("mygo: drawing without the GPU: %v", err)
			} else {
				h.gpu = r
			}
		}
	}
	if h.gpu != nil {
		if err := h.gpu.Render(s); err == nil {
			return
		} else {
			log.Printf("mygo: drawing without the GPU: %v", err)
			h.gpu.Release()
			h.gpu = nil
		}
	}
	h.soft.Render(s)
	m := &h.soft.Image
	h.conn.Surface.PresentPixels(m.Pix, m.Stride, m.W, m.H)
}

// capture renders the last frame in memory.
func (h *windowHost) capture() (int, int, []byte) {
	if h.last == nil {
		h.rt.runFrame()
	}
	s := h.last
	if s == nil {
		return 0, 0, nil
	}
	var img raster.Image
	img.Resize(s.Width, s.Height)
	raster.Render(&img, s)
	return img.W, img.H, img.RGBA()
}

func (h *windowHost) detach() {
	h.rt.close()
	if h.gpu != nil {
		h.gpu.Release()
		h.gpu = nil
	}
}

func (h *windowHost) requestFrame() { h.conn.Surface.RequestFrame() }

func (h *windowHost) setCursor(c Cursor) { h.conn.Surface.SetCursor(platform.Cursor(c)) }

func (h *windowHost) setTextInput(active bool, r Rect) {
	h.conn.Surface.SetTextInput(active, platform.RectF{X: float64(r.X), Y: float64(r.Y), W: float64(r.W), H: float64(r.H)})
}

func (h *windowHost) readClipboard() string {
	if h.conn.Clipboard == nil {
		return ""
	}
	return h.conn.Clipboard.ReadText()
}

func (h *windowHost) writeClipboard(s string) {
	if h.conn.Clipboard != nil {
		h.conn.Clipboard.WriteText(s)
	}
}

func (h *windowHost) startDrag() {
	if h.conn.StartDrag != nil {
		h.conn.StartDrag()
	}
}

func (h *windowHost) titleBarDoubleClicked() {
	if h.conn.TitleBarDoubleClicked != nil {
		h.conn.TitleBarDoubleClicked()
	}
}

func (h *windowHost) isDark() bool { return h.conn.IsDark != nil && h.conn.IsDark() }

func (h *windowHost) invalidate() { h.conn.Invalidate() }

func (h *windowHost) openURL(u string) {
	if h.conn.OpenURL != nil {
		h.conn.OpenURL(u)
	}
}
