//go:build linux && (amd64 || arm64)

package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/egoist/mygo/internal/cef"
	"github.com/egoist/mygo/internal/platform"
)

// Apps that bundle CEF show their pages in CEF browsers instead of WebKit
// web views (package cef). A window holds an empty widget where the web
// view would be, and over it an X11 window of its own (the host), which
// holds the browser's X11 window: CEF embeds in X11 windows only, so GDK
// runs on X11, through XWayland on Wayland. The host has a 24-bit visual of
// its own, as Chromium draws wrong in the 32-bit ones GTK gives some
// windows. CEF runs the GLib main loop (cef_run_message_loop), which
// dispatches GTK's events too, and nested loops let it run its tasks.

// cefDir is the directory of CEF relative to the executable when the app
// bundles it; mygo build and mygo dev link it (-ldflags -X). MYGO_CEF_DIR
// overrides it.
var cefDir string

// helperName is the executable of CEF's child processes in the directory.
const helperName = "mygo-helper"

func cefDirectory() string {
	if d := os.Getenv("MYGO_CEF_DIR"); d != "" {
		return d
	}
	if cefDir == "" || filepath.IsAbs(cefDir) {
		return cefDir
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), cefDir)
}

// X11 functions for the hosts, on GDK's connection.
var xl struct {
	matchVisualInfo  func(dpy ptr, screen, depth, class int32, info *xVisualInfo) int32
	createColormap   func(dpy ptr, window uint64, visual ptr, alloc int32) uint64
	freeColormap     func(dpy ptr, colormap uint64) int32
	createWindow     func(dpy ptr, parent uint64, x, y int32, width, height, border uint32, depth int32, class uint32, visual ptr, mask uint64, attrs *xSetWindowAttributes) uint64
	mapWindow        func(dpy ptr, window uint64) int32
	unmapWindow      func(dpy ptr, window uint64) int32
	reparentWindow   func(dpy ptr, window, parent uint64, x, y int32) int32
	getInputFocus    func(dpy ptr, focus *uint64, revert *int32) int32
	setInputFocus    func(dpy ptr, focus uint64, revert int32, time uint64) int32
	queryTree        func(dpy ptr, window uint64, root, parent *uint64, children *ptr, n *uint32) int32
	free             func(data ptr) int32
	moveResizeWindow func(dpy ptr, window uint64, x, y int32, width, height uint32) int32
	setBackground    func(dpy ptr, window uint64, pixel uint64) int32
	defaultScreen    func(dpy ptr) int32
	rootWindow       func(dpy ptr, screen int32) uint64
	ungrabPointer    func(dpy ptr, time uint64) int32
	flush            func(dpy ptr) int32
	shapeRectangles  func(dpy ptr, window uint64, kind, x, y int32, rects *xRectangle, n, op, ordering int32)
	// GDK.
	setAllowedBackends func(backends string)
	windowXID          func(window ptr) uint64
	keymapForDisplay   func(display ptr) ptr
	translateKey       func(keymap ptr, keycode, state uint32, group int32, keyval *uint32, group2, level *int32, consumed *uint32) bool
	keyvalToLower      func(keyval uint32) uint32
	// GTK.
	realize             func(widget ptr)
	translateCoords     func(src, dst ptr, x, y int32, dx, dy *int32) bool
	scaleFactor         func(widget ptr) int32
	accelGroupsActivate func(object ptr, key uint32, mods uint32) bool
	defaultModMask      func() uint32
	// The resize edges of frameless windows.
	newWindow      func(parent ptr, attrs *gdkWindowAttr, mask int32) ptr
	ensureNative   func(window ptr) bool
	setUserData    func(window, widget ptr)
	moveResize     func(window ptr, x, y, width, height int32)
	showWindow     func(window ptr)
	hideWindow     func(window ptr)
	raiseWindow    func(window ptr)
	destroyWindow  func(window ptr)
	inputShape     func(window, region ptr, x, y int32)
	regionRect     func(rect *gdkRectangle) ptr
	regionSubtract func(region ptr, rect *gdkRectangle) int32
	regionDestroy  func(region ptr)
	// Keys handed to GTK.
	eventNew       func(typ int32) ptr
	eventSetDevice func(event, device ptr)
	seatKeyboard   func(seat ptr) ptr
	mainDoEvent    func(event ptr)
	// CEF's own connection, whose pointer grab a window drag ends.
	cefDisplay ptr
}

// gdkWindowAttr is GdkWindowAttr.
type gdkWindowAttr struct {
	title                     ptr
	eventMask                 int32
	x, y, width, height       int32
	wclass                    int32
	visual                    ptr
	windowType                int32
	_                         int32
	cursor                    ptr
	wmclassName, wmclassClass ptr
	overrideRedirect          int32
	typeHint                  int32
}

// xRectangle is XRectangle.
type xRectangle struct {
	x, y          int16
	width, height uint16
}

// xVisualInfo is XVisualInfo.
type xVisualInfo struct {
	visual                       ptr
	visualID                     uint64
	screen, depth, class         int32
	_                            int32
	redMask, greenMask, blueMask uint64
	colormapSize, bitsPerRGB     int32
}

// xSetWindowAttributes is XSetWindowAttributes.
type xSetWindowAttributes struct {
	backgroundPixmap, backgroundPixel, borderPixmap, borderPixel uint64
	bitGravity, winGravity, backingStore                         int32
	_                                                            int32
	backingPlanes, backingPixel                                  uint64
	saveUnder                                                    int32
	_                                                            int32
	eventMask, doNotPropagateMask                                int64
	overrideRedirect                                             int32
	_                                                            int32
	colormap, cursor                                             uint64
}

const (
	xTrueColor   = 4
	xInputOutput = 1
	cwBackPixel  = 1 << 1
	cwBorderPx   = 1 << 3
	cwColormap   = 1 << 13
)

// loadCEF loads CEF from dir and the X11 functions the hosts use, before
// GTK starts: GDK must open an X11 display.
func (b *Backend) loadCEF(dir string) error {
	if err := cef.Load(dir); err != nil {
		return err
	}
	x, err := open("libX11.so.6")
	if err != nil {
		return err
	}
	mustBind(x, &xl.matchVisualInfo, "XMatchVisualInfo")
	mustBind(x, &xl.createColormap, "XCreateColormap")
	mustBind(x, &xl.freeColormap, "XFreeColormap")
	mustBind(x, &xl.createWindow, "XCreateWindow")
	mustBind(x, &xl.mapWindow, "XMapWindow")
	mustBind(x, &xl.unmapWindow, "XUnmapWindow")
	mustBind(x, &xl.reparentWindow, "XReparentWindow")
	mustBind(x, &xl.getInputFocus, "XGetInputFocus")
	mustBind(x, &xl.setInputFocus, "XSetInputFocus")
	mustBind(x, &xl.queryTree, "XQueryTree")
	mustBind(x, &xl.free, "XFree")
	mustBind(x, &xl.moveResizeWindow, "XMoveResizeWindow")
	mustBind(x, &xl.setBackground, "XSetWindowBackground")
	mustBind(x, &xl.defaultScreen, "XDefaultScreen")
	mustBind(x, &xl.rootWindow, "XRootWindow")
	mustBind(x, &xl.ungrabPointer, "XUngrabPointer")
	mustBind(x, &xl.flush, "XFlush")
	ext, err := open("libXext.so.6")
	if err != nil {
		return err
	}
	mustBind(ext, &xl.shapeRectangles, "XShapeCombineRectangles")
	// The host windows hold X11 windows: no Wayland, where XWayland stands
	// in. GDK_BACKEND set to wayland would leave GDK no display to open.
	_ = os.Setenv("GDK_BACKEND", "x11")
	return nil
}

// bindCEF binds the GDK and GTK functions of the hosts, once GTK is loaded.
func bindCEF() {
	mustBind(libGDK, &xl.setAllowedBackends, "gdk_set_allowed_backends")
	mustBind(libGDK, &xl.windowXID, "gdk_x11_window_get_xid")
	mustBind(libGDK, &xl.keymapForDisplay, "gdk_keymap_get_for_display")
	mustBind(libGDK, &xl.translateKey, "gdk_keymap_translate_keyboard_state")
	mustBind(libGDK, &xl.keyvalToLower, "gdk_keyval_to_lower")
	mustBind(libGTK, &xl.realize, "gtk_widget_realize")
	mustBind(libGTK, &xl.translateCoords, "gtk_widget_translate_coordinates")
	mustBind(libGTK, &xl.scaleFactor, "gtk_widget_get_scale_factor")
	mustBind(libGTK, &xl.accelGroupsActivate, "gtk_accel_groups_activate")
	mustBind(libGTK, &xl.defaultModMask, "gtk_accelerator_get_default_mod_mask")
	mustBind(libGDK, &xl.newWindow, "gdk_window_new")
	mustBind(libGDK, &xl.ensureNative, "gdk_window_ensure_native")
	mustBind(libGDK, &xl.setUserData, "gdk_window_set_user_data")
	mustBind(libGDK, &xl.moveResize, "gdk_window_move_resize")
	mustBind(libGDK, &xl.showWindow, "gdk_window_show")
	mustBind(libGDK, &xl.hideWindow, "gdk_window_hide")
	mustBind(libGDK, &xl.raiseWindow, "gdk_window_raise")
	mustBind(libGDK, &xl.destroyWindow, "gdk_window_destroy")
	mustBind(libGDK, &xl.inputShape, "gdk_window_input_shape_combine_region")
	mustBind(libCairo, &xl.regionRect, "cairo_region_create_rectangle")
	mustBind(libCairo, &xl.regionSubtract, "cairo_region_subtract_rectangle")
	mustBind(libCairo, &xl.regionDestroy, "cairo_region_destroy")
	mustBind(libGDK, &xl.eventNew, "gdk_event_new")
	mustBind(libGDK, &xl.eventSetDevice, "gdk_event_set_device")
	mustBind(libGDK, &xl.seatKeyboard, "gdk_seat_get_keyboard")
	mustBind(libGTK, &xl.mainDoEvent, "gtk_main_do_event")
}

// startCEF starts CEF once GTK runs.
func (b *Backend) startCEF(dir string, opts platform.AppOptions) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cache := profileDir(filepath.Join(config, opts.Name))
	return cef.Initialize(cef.Options{
		Dir:         dir,
		Helper:      filepath.Join(dir, helperName),
		Cache:       cache,
		Development: opts.Dev,
		Post:        b.post,
		Ready: func() {
			b.cefReady = true
			if b.running {
				b.h.Ready()
			}
		},
	})
}

// profileDir returns the CEF profile of the app in dir: Chromium lets one
// process use a profile, so another instance of the app gets one of its
// own.
func profileDir(dir string) string {
	p := filepath.Join(dir, "CEF")
	for n := 2; cef.ProfileInUse(p); n++ {
		p = filepath.Join(dir, "CEF-"+strconv.Itoa(n))
	}
	return p
}

// The functions posted to the main thread (Backend.post), which a GLib
// source runs.
var (
	postMu      sync.Mutex
	posted      []func()
	postPending atomic.Bool
	cbPosted    ptr
)

// post runs fn on the main thread soon. It is called from any thread.
func (b *Backend) post(fn func()) {
	postMu.Lock()
	posted = append(posted, fn)
	postMu.Unlock()
	if postPending.CompareAndSwap(false, true) {
		gIdleAddFull(0, cbPosted, 0, 0)
	}
}

func initCEFCallbacks() {
	cbPosted = purego.NewCallback(func(data ptr) int32 {
		postPending.Store(false)
		postMu.Lock()
		fns := posted
		posted = nil
		postMu.Unlock()
		for _, fn := range fns {
			fn()
		}
		return 0 // G_SOURCE_REMOVE
	})
	cbPageAllocated = purego.NewCallback(func(widget, allocation, data ptr) {
		if w := theBackend.window(data); w != nil {
			w.layoutPage()
		}
	})
	// An edge that comes under the pointer, as the window resizes, shows its
	// cursor: no motion event tells.
	cbEdgesEntered = purego.NewCallback(func(widget, event, data ptr) bool {
		w := theBackend.window(data)
		if w == nil || field[ptr](event, 8) != w.edges {
			return false
		}
		var x, y int32
		pointer := gdkSeatGetPointer(gdkDisplayGetDefaultSeat(gdkDisplayGetDefault()))
		gdkWindowGetDevicePosition(w.edges, pointer, &x, &y, nil)
		// A motion event of the edges at the pointer: window 8, x 24, y 32.
		var motion [64]byte
		*(*ptr)(unsafe.Pointer(&motion[8])) = w.edges
		*(*float64)(unsafe.Pointer(&motion[24])) = float64(x)
		*(*float64)(unsafe.Pointer(&motion[32])) = float64(y)
		w.showResizeCursor(w.resizeEdge(ptr(unsafe.Pointer(&motion[0]))))
		return false
	})
	cbEdgesLeft = purego.NewCallback(func(widget, event, data ptr) bool {
		// GdkEventCrossing: window 8.
		if w := theBackend.window(data); w != nil && field[ptr](event, 8) == w.edges {
			w.showResizeCursor(-1)
		}
		return false
	})
}

var cbPageAllocated ptr

// nested runs fn, a nested main loop on the main thread, letting CEF run
// its tasks in it.
func (b *Backend) nested(fn func()) {
	if b.cef {
		cef.NestedLoop(fn)
	} else {
		fn()
	}
}

// createPage puts the widget that the page goes over where the web view
// would be.
func (w *window) createPage() {
	w.area = gtkBoxNew(1, 0)
	gtkWidgetSetSizeRequest(w.area, 1, 1)
	for _, s := range w.opts.Schemes {
		w.b.registerScheme(s)
	}
	connect(w.area, "size-allocate", cbPageAllocated, ptr(w.id))
}

// createBrowser makes the host and, unless CEF makes the window's browser
// for window.open(), the browser. The window is realized: it has an X11
// window.
func (w *window) createBrowser() error {
	o := w.opts
	xl.realize(w.win)
	dpy := x11.xdisplay(gdkDisplayGetDefault())
	screen := xl.defaultScreen(dpy)
	var info xVisualInfo
	if xl.matchVisualInfo(dpy, screen, 24, xTrueColor, &info) == 0 {
		return fmt.Errorf("mygo: no 24-bit X11 visual for CEF")
	}
	w.colormap = xl.createColormap(dpy, xl.rootWindow(dpy, screen), info.visual, 0)
	attrs := xSetWindowAttributes{backgroundPixel: w.backgroundPixel(), colormap: w.colormap}
	scale := xl.scaleFactor(w.win)
	width, height := uint32(max(o.Width, 1)*int(scale)), uint32(max(o.Height, 1)*int(scale))
	w.host = xl.createWindow(dpy, xl.windowXID(gtkWidgetGetWindow(w.win)), 0, 0, width, height, 0, info.depth, xInputOutput, info.visual,
		cwBackPixel|cwBorderPx|cwColormap, &attrs)
	xl.mapWindow(dpy, w.host)
	xl.flush(dpy)
	if w.undecorated() {
		w.createEdges()
	}

	scripts := make([]cef.Script, 0, len(o.UserScripts)+1)
	for _, s := range o.UserScripts {
		scripts = append(scripts, cef.Script{Source: s.Source, AtDocumentEnd: s.AtDocumentEnd, AllFrames: s.AllFrames})
	}
	if w.controls != nil {
		// After the bridge, which it tells.
		scripts = append(scripts, cef.Script{Source: w.titleBarScript()})
	}
	page, err := cef.NewBrowser(cef.BrowserOptions{
		Parent:     uintptr(w.host),
		Width:      int(width),
		Height:     int(height),
		Scripts:    scripts,
		Schemes:    o.Schemes,
		Background: o.BackgroundColor,
		UserAgent:  o.UserAgent,
		DevTools:   o.DevTools,
		Zoom:       o.Zoom,
		Popup:      o.Native != 0,
	}, cefHost{w.h, w})
	if err != nil {
		return err
	}
	w.page = page
	return nil
}

// GTK gives windows without decorations no resize edges: as over WebKit's
// page (resizeEdge), the outer pixels of CEF's resize them, through an
// input-only window over the host whose input shape is that border.
// Pointer events on it reach the window's GtkWindow in page coordinates.
func (w *window) createEdges() {
	const (
		pointerMotion = 1 << 2
		buttonPress   = 1 << 8
		buttonRelease = 1 << 9
		enterNotify   = 1 << 12
		leaveNotify   = 1 << 13
		inputOnly     = 1
		childWindow   = 2
	)
	attrs := gdkWindowAttr{
		eventMask: pointerMotion | buttonPress | buttonRelease | enterNotify | leaveNotify,
		width:     1, height: 1, wclass: inputOnly, windowType: childWindow,
	}
	w.edges = xl.newWindow(gtkWidgetGetWindow(w.win), &attrs, 0)
	xl.ensureNative(w.edges)
	xl.setUserData(w.edges, w.win)
	data := ptr(w.id)
	connect(w.win, "button-press-event", cbButtonPress, data)
	connect(w.win, "motion-notify-event", cbMotion, data)
	connect(w.win, "enter-notify-event", cbEdgesEntered, data)
	connect(w.win, "leave-notify-event", cbEdgesLeft, data)
}

// layoutEdges puts the resize edges over the page, in logical pixels, and
// hides them where nothing resizes the window.
func (w *window) layoutEdges(page gdkRectangle) {
	if w.edges == 0 {
		return
	}
	if !gtkWindowGetResizable(w.win) || w.state&(stateMaximized|stateFullscreen) != 0 {
		xl.hideWindow(w.edges)
		w.showResizeCursor(-1)
		return
	}
	xl.moveResize(w.edges, page.X, page.Y, page.Width, page.Height)
	region := xl.regionRect(&gdkRectangle{Width: page.Width, Height: page.Height})
	inner := gdkRectangle{X: resizeInset, Y: resizeInset, Width: page.Width - 2*resizeInset, Height: page.Height - 2*resizeInset}
	if page.Y != 0 { // a menu bar above: no top edge
		inner.Y, inner.Height = 0, page.Height-resizeInset
	}
	xl.regionSubtract(region, &inner)
	xl.inputShape(w.edges, region, 0, 0)
	xl.regionDestroy(region)
	xl.showWindow(w.edges)
	xl.raiseWindow(w.edges) // over the host
}

var cbEdgesLeft, cbEdgesEntered ptr

// backgroundPixel is what the host shows until the page paints.
func (w *window) backgroundPixel() uint64 {
	if c := w.opts.BackgroundColor; c != nil {
		return uint64(c.R)<<16 | uint64(c.G)<<8 | uint64(c.B)
	}
	return 0xffffff
}

// layoutPage puts the host, and the browser in it, over the widget of the
// page, in device pixels.
func (w *window) layoutPage() {
	if w.host == 0 || w.closed {
		return
	}
	var a gdkRectangle
	gtkWidgetGetAllocation(w.area, &a)
	var x, y int32
	xl.translateCoords(w.area, w.win, 0, 0, &x, &y)
	scale := xl.scaleFactor(w.win)
	width, height := uint32(max(a.Width*scale, 1)), uint32(max(a.Height*scale, 1))
	dpy := x11.xdisplay(gdkDisplayGetDefault())
	x11.errorTrapPush(gdkDisplayGetDefault())
	xl.moveResizeWindow(dpy, w.host, x*scale, y*scale, width, height)
	if bw := w.page.WindowHandle(); bw != 0 {
		xl.moveResizeWindow(dpy, uint64(bw), 0, 0, width, height)
	}
	w.shapeHost(x, y, scale, width, height)
	xl.flush(dpy)
	x11.errorTrapPop(gdkDisplayGetDefault())
	a.X, a.Y = x, y
	w.layoutEdges(a)
}

// shapeHost leaves out of the host the title buttons of a window with a
// hidden title bar, which GTK draws in the window under it: an X11 window
// cannot show what is under it, so the buttons have the window's
// background around them rather than the page.
func (w *window) shapeHost(x, y, scale int32, width, height uint32) {
	if w.controls == nil {
		return
	}
	const shapeBounding, shapeSet, shapeSubtract, unsorted = 0, 0, 3, 0
	dpy := x11.xdisplay(gdkDisplayGetDefault())
	all := xRectangle{width: uint16(width), height: uint16(height)}
	xl.shapeRectangles(dpy, w.host, shapeBounding, 0, 0, &all, 1, shapeSet, unsorted)
	for _, bar := range w.controls.bars {
		if bar == 0 || !gtkWidgetGetVisible(bar) {
			continue
		}
		var a gdkRectangle
		gtkWidgetGetAllocation(bar, &a)
		var bx, by int32
		xl.translateCoords(bar, w.win, 0, 0, &bx, &by)
		r := xRectangle{x: int16(bx*scale - x*scale), y: int16(by*scale - y*scale), width: uint16(a.Width * scale), height: uint16(a.Height * scale)}
		xl.shapeRectangles(dpy, w.host, shapeBounding, 0, 0, &r, 1, shapeSubtract, unsorted)
	}
}

// closePage closes the browser; the host goes with the window.
// detachPage closes the page of a window that is about to be destroyed.
// GTK destroys a window's X11 windows before it reports the destroy, the
// browser's among them, and CEF closes the browser once its window gets
// the close request it sends itself: the browser's window waits for it on
// the root window, hidden.
func (w *window) detachPage() {
	if xid := uint64(w.page.WindowHandle()); xid != 0 {
		display := gdkDisplayGetDefault()
		dpy := x11.xdisplay(display)
		x11.errorTrapPush(display)
		if focusIn(dpy, xid) {
			// Hidden, the browser's window would pass the keyboard to its
			// parent and, once that is destroyed, to no window, which
			// stays so without a window manager. This window takes it, and
			// X gives it to the root window when this one is destroyed.
			const revertToParent = 2
			xl.setInputFocus(dpy, uint64(xl.windowXID(gtkWidgetGetWindow(w.win))), revertToParent, 0)
		}
		xl.unmapWindow(dpy, xid)
		xl.reparentWindow(dpy, xid, xl.rootWindow(dpy, xl.defaultScreen(dpy)), 0, 0)
		xl.flush(dpy)
		x11.errorTrapPop(display)
	}
	w.page.Close()
}

// focusIn reports whether the X11 window w or one of its descendants has
// the keyboard.
func focusIn(dpy ptr, w uint64) bool {
	var focus uint64
	var revert int32
	xl.getInputFocus(dpy, &focus, &revert)
	for focus > 1 { // None and PointerRoot are no windows
		if focus == w {
			return true
		}
		var root, parent uint64
		var children ptr
		var n uint32
		if xl.queryTree(dpy, focus, &root, &parent, &children, &n) == 0 {
			return false
		}
		if children != 0 {
			xl.free(children)
		}
		focus = parent
	}
	return false
}

func (w *window) closePage() {
	w.page.Close()
	if w.edges != 0 {
		xl.setUserData(w.edges, 0)
		xl.destroyWindow(w.edges)
		w.edges = 0
	}
	if w.colormap != 0 {
		xl.freeColormap(x11.xdisplay(gdkDisplayGetDefault()), w.colormap)
		w.colormap = 0
	}
}

// CEFBrowser returns the browser of a window that bundles CEF: the
// browser of window.open() comes from CEF once the window exists.
func (w *window) CEFBrowser() *cef.Browser { return w.page }

// startDragCEF moves the window with the mouse: Chromium holds the pointer
// grab of the press in the page, which the window manager needs.
func (w *window) startDragCEF() {
	if xl.cefDisplay != 0 {
		xl.ungrabPointer(xl.cefDisplay, 0)
		xl.flush(xl.cefDisplay)
	}
	var x, y int32
	pointer := gdkSeatGetPointer(gdkDisplayGetDefaultSeat(gdkDisplayGetDefault()))
	gdkDeviceGetPosition(pointer, nil, &x, &y)
	gtkWindowBeginMoveDrag(w.win, 1, x, y, 0) // GDK_CURRENT_TIME
}

// cefHost is the cef.Host of a window: its WindowHandler gets the page's
// events.
type cefHost struct {
	platform.WindowHandler
	w *window
}

// Key does what GTK does with the window's keys, which it does not see
// while the page has the keyboard: a menu bar that hides shows for Alt
// alone or F10, and the menu accelerators run.
func (h cefHost) Key(e cef.KeyEvent) bool {
	w := h.w
	if w.closed {
		return false
	}
	var mods uint32
	if e.Shift {
		mods |= shiftMask
	}
	if e.Control {
		mods |= controlMask
	}
	if e.Alt {
		mods |= mod1Mask
	}
	if e.Super {
		mods |= 1 << 26 // GDK_SUPER_MASK
	}
	var keyval, consumed uint32
	keymap := xl.keymapForDisplay(gdkDisplayGetDefault())
	if !xl.translateKey(keymap, uint32(e.NativeCode), mods, 0, &keyval, nil, nil, &consumed) {
		return false
	}
	alt := keyval == 0xffe9 || keyval == 0xffea // Alt_L, Alt_R
	if alt {
		mods &^= mod1Mask // Chromium counts the key itself, GDK does not
	}
	if w.autoHideMenu && w.menubar != 0 && !gtkWidgetGetVisible(w.menubar) {
		// GTK gets the key as from the window, for cbMenuKey: Alt alone
		// and F10 show the menu bar, opening its menu from the key.
		w.sendKey(e.Up, keyval, uint16(e.NativeCode), mods)
		if !e.Up && keyval == 0xffc7 && mods&modifierMask == 0 { // F10
			return true
		}
	}
	if e.Up || w.accel == 0 || mods&^shiftMask == 0 {
		return false // no shortcut
	}
	return xl.accelGroupsActivate(w.win, xl.keyvalToLower(keyval), mods&xl.defaultModMask())
}

// sendKey has GTK handle a key event of the window.
func (w *window) sendKey(up bool, keyval uint32, code uint16, state uint32) {
	typ := int32(8) // GDK_KEY_PRESS
	if up {
		typ = 9
	}
	ev := xl.eventNew(typ)
	// GdkEventKey: window 8, send_event 16, time 20, state 24, keyval 28,
	// hardware_keycode 48; gdk_event_free releases the window.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&ev))
	*(*ptr)(unsafe.Add(p, 8)) = gObjectRef(gtkWidgetGetWindow(w.win))
	*(*int8)(unsafe.Add(p, 16)) = 1
	*(*uint32)(unsafe.Add(p, 24)) = state
	*(*uint32)(unsafe.Add(p, 28)) = keyval
	*(*uint16)(unsafe.Add(p, 48)) = code
	xl.eventSetDevice(ev, xl.seatKeyboard(gdkDisplayGetDefaultSeat(gdkDisplayGetDefault())))
	xl.mainDoEvent(ev)
	gdkEventFree(ev)
}

func (h cefHost) DragEntered(paths []string) { h.w.dropped = paths }
