//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/egoist/mygo/internal/platform"
)

// Host is the window of a browser. Its page events are those of
// platform.WindowHandler, which the browser reports on the main thread with
// app URLs (see BrowserOptions.Schemes).
type Host interface {
	platform.WindowHandler
	// Key gets the keys pressed in the page before the page does: true
	// takes the key from it (a menu accelerator, for example).
	Key(e KeyEvent) bool
	// DragEntered gives the paths of the files dragged onto the page.
	DragEntered(paths []string)
}

// KeyEvent is a key pressed or released in a page.
type KeyEvent struct {
	Up bool // released
	// NativeCode is the X11 key code.
	NativeCode int
	// The modifiers held, the key's own included (Chromium's).
	Shift, Control, Alt, Super bool
}

// BrowserOptions configure a browser.
type BrowserOptions struct {
	// Parent is the X11 window holding the browser, which fills it.
	Parent        uintptr
	Width, Height int // device pixels
	Scripts       []Script
	// Schemes are served by the app (RegisterScheme): their URLs,
	// <scheme>://localhost/…, load from http://<scheme>.localhost/…, which
	// Chromium treats as a secure origin. The browser reports app URLs.
	Schemes []string
	// Background is painted until the page does; nil is white. Windowed
	// browsers have no transparency: its alpha is ignored.
	Background *platform.Color
	UserAgent  string
	DevTools   bool
	Zoom       float64
	// Popup makes the browser of a window that window.open() asked for:
	// CEF creates it in the opener's process, with the opener.
	Popup bool
}

// Browser is a CEF browser in a window.
type Browser struct {
	host Host
	opts BrowserOptions
	obj  *object // the client and the handlers

	// Main thread only.
	browser, bh  uintptr // cef_browser_t and cef_browser_host_t, held
	id           int32
	pending      []func() // calls waiting for a popup's browser
	closed       bool
	closing      bool // in doClose, which answers CEF's close
	loading      bool
	programmatic bool
	devtools     uintptr // registration of the DevTools observer
	calls        map[int32]func([]byte, error)
	zoom         float64
	userAgent    string
}

// The structures of a browser's object, in order.
const (
	clientIdx = iota
	lifeSpanIdx
	loadIdx
	displayIdx
	requestIdx
	downloadIdx
	permissionIdx
	keyboardIdx
	dragIdx
)

var browserClasses []*class

// browsersByID finds browsers by CEF identifier, from any thread.
var browsersByID sync.Map

// NewBrowser creates a browser in opts.Parent. A popup's browser exists
// once CEF created it; calls wait for it meanwhile.
func NewBrowser(o BrowserOptions, h Host) (*Browser, error) {
	b := &Browser{host: h, opts: o, calls: map[int32]func([]byte, error){}, zoom: 1, userAgent: o.UserAgent}
	if o.Zoom > 0 {
		b.zoom = o.Zoom
	}
	b.obj = newObject(b, browserClasses...)
	browsers++
	if o.Popup {
		return b, nil
	}
	wi := b.windowInfo()
	bs := b.settings()
	extra := b.extraInfo()
	empty := newStr("")
	p := call(lib.createBrowserSync, addr(wi), b.obj.ref(clientIdx), empty.p(), addr(bs), extra, 0)
	runtime.KeepAlive(wi)
	runtime.KeepAlive(bs)
	runtime.KeepAlive(empty)
	if p == 0 {
		browsers--
		b.obj.release()
		return nil, errors.New("mygo: CEF could not create a browser")
	}
	b.attach(p)
	return b, nil
}

func (b *Browser) windowInfo() *cefWindowInfo {
	return &cefWindowInfo{
		size:         unsafe.Sizeof(cefWindowInfo{}),
		bounds:       cefRect{width: int32(b.opts.Width), height: int32(b.opts.Height)},
		parentWindow: b.opts.Parent,
		runtimeStyle: cefRuntimeStyleAlloy,
	}
}

func (b *Browser) settings() *cefBrowserSettings {
	s := &cefBrowserSettings{
		size:                      unsafe.Sizeof(cefBrowserSettings{}),
		javascriptAccessClipboard: 1, // STATE_ENABLED
		javascriptDomPaste:        1,
	}
	if c := b.opts.Background; c != nil {
		s.backgroundColor = 0xff<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	}
	return s
}

// extraInfo returns the dictionary of the renderer's scripts, with a
// reference for CEF.
func (b *Browser) extraInfo() uintptr {
	data, _ := json.Marshal(b.opts.Scripts)
	d := call(lib.dictionaryValueCreate)
	k, v := newStr(extraKey), newStr(string(data))
	call(at[cefDictionaryValue](d).setString, d, k.p(), v.p())
	runtime.KeepAlive(k)
	runtime.KeepAlive(v)
	return d
}

// attach takes the reference to a CEF browser that now exists.
func (b *Browser) attach(p uintptr) {
	b.browser = p
	b.bh = call(at[cefBrowser](p).getHost, p)
	b.id = browserID(p)
	browsersByID.Store(b.id, b)
	b.devtools = call(at[cefBrowserHost](b.bh).addDevToolsMessageObserver, b.bh, devtoolsObserver.ref(0))
	fixSignals() // in case starting the browser installed more
	if b.opts.Zoom > 0 && b.opts.Zoom != 1 {
		b.setZoom(b.opts.Zoom)
	}
	if b.opts.UserAgent != "" {
		b.setUserAgent(b.opts.UserAgent)
	}
	pending := b.pending
	b.pending = nil
	for _, fn := range pending {
		fn()
	}
}

// do runs fn once the browser exists.
func (b *Browser) do(fn func()) {
	switch {
	case b.closed:
	case b.browser != 0:
		fn()
	default:
		b.pending = append(b.pending, fn)
	}
}

// WindowHandle returns the X11 window of the browser, 0 before it exists.
func (b *Browser) WindowHandle() uintptr {
	if b.bh == 0 {
		return 0
	}
	return call(at[cefBrowserHost](b.bh).getWindowHandle, b.bh)
}

// Close closes the browser for good, without onbeforeunload. CEF closes
// its window (doClose), then the browser: the window must not be destroyed
// before, which would leave the browser running, since CEF only closes a
// browser whose window gets the close request it sends itself.
func (b *Browser) Close() {
	if b.closed {
		return
	}
	b.closed = true
	b.pending = nil
	if b.bh == 0 {
		return // a popup's, closed once created
	}
	call(at[cefBrowserHost](b.bh).closeDevTools, b.bh)
	if !b.closing {
		call(at[cefBrowserHost](b.bh).closeBrowser, b.bh, 1)
	}
}

// SetFocus gives the keyboard to the page, or takes it away.
func (b *Browser) SetFocus(focus bool) {
	b.do(func() { call(at[cefBrowserHost](b.bh).setFocus, b.bh, cbool(focus)) })
}

// Moved tells the page its window moved or started to resize, which
// closes its popups (select menus, for example).
func (b *Browser) Moved() {
	b.do(func() { call(at[cefBrowserHost](b.bh).notifyMoveOrResizeStarted, b.bh) })
}

// Resized tells the browser its window changed size.
func (b *Browser) Resized() {
	b.do(func() { call(at[cefBrowserHost](b.bh).wasResized, b.bh) })
}

// mainFrame calls fn with the main frame.
func (b *Browser) mainFrame(fn func(frame uintptr)) {
	b.do(func() {
		frame := call(at[cefBrowser](b.browser).getMainFrame, b.browser)
		if frame == 0 {
			return
		}
		defer release(frame)
		fn(frame)
	})
}

// webURL maps an app URL to the URL the browser loads.
func (b *Browser) webURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "localhost" || !b.hasScheme(u.Scheme) {
		return raw
	}
	u.Host = u.Scheme + ".localhost"
	u.Scheme = "http"
	return u.String()
}

// appURL maps a URL the browser loaded to its app URL.
func (b *Browser) appURL(raw string) string {
	if isBlank(raw) {
		return "about:blank"
	}
	return AppURL(raw, b.hasScheme)
}

// AppURL maps http://<scheme>.localhost/… to <scheme>://localhost/… for
// the schemes of the app.
func AppURL(raw string, isScheme func(string) bool) string {
	rest, ok := strings.CutPrefix(raw, "http://")
	if !ok {
		return raw
	}
	host, path, _ := strings.Cut(rest, "/")
	scheme, ok := strings.CutSuffix(host, ".localhost")
	if !ok || !isScheme(scheme) {
		return raw
	}
	return scheme + "://localhost/" + path
}

func (b *Browser) hasScheme(s string) bool {
	for _, x := range b.opts.Schemes {
		if x == s {
			return true
		}
	}
	return false
}

func (b *Browser) LoadURL(raw string) {
	b.programmatic = true
	target := newStr(b.webURL(raw))
	b.mainFrame(func(frame uintptr) {
		call(at[cefFrame](frame).loadUrl, frame, target.p())
		runtime.KeepAlive(target)
	})
}

// LoadHTML shows html as the document at baseURL, served once from there,
// as on Windows. Without an http(s) base URL it loads from a URL of its
// own that stands for about:blank.
func (b *Browser) LoadHTML(html, baseURL string) {
	target := b.webURL(baseURL)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		overrideURL(target, html)
	} else {
		target = blankURL(html)
	}
	b.programmatic = true
	t := newStr(target)
	b.mainFrame(func(frame uintptr) {
		call(at[cefFrame](frame).loadUrl, frame, t.p())
		runtime.KeepAlive(t)
	})
}

func (b *Browser) Reload(ignoreCache bool) {
	b.programmatic = true
	b.do(func() {
		if ignoreCache {
			call(at[cefBrowser](b.browser).reloadIgnoreCache, b.browser)
		} else {
			call(at[cefBrowser](b.browser).reload, b.browser)
		}
	})
}

func (b *Browser) StopLoading() { b.do(func() { call(at[cefBrowser](b.browser).stopLoad, b.browser) }) }
func (b *Browser) GoBack()      { b.do(func() { call(at[cefBrowser](b.browser).goBack, b.browser) }) }
func (b *Browser) GoForward()   { b.do(func() { call(at[cefBrowser](b.browser).goForward, b.browser) }) }

func (b *Browser) CanGoBack() bool {
	return b.browser != 0 && call(at[cefBrowser](b.browser).canGoBack, b.browser) != 0
}

func (b *Browser) CanGoForward() bool {
	return b.browser != 0 && call(at[cefBrowser](b.browser).canGoForward, b.browser) != 0
}

func (b *Browser) IsLoading() bool { return b.loading }

func (b *Browser) URL() string {
	var u string
	if b.browser != 0 && !b.closed {
		b.mainFrame(func(frame uintptr) { u = takeStr(call(at[cefFrame](frame).getUrl, frame)) })
	}
	return b.appURL(u)
}

// PostMessages passes msgs, messages of the bridge as a JSON array, to
// the receiver of the main frame's bridge (helper.go), which parses them:
// a script that calls __mygo.receive would be compiled.
func (b *Browser) PostMessages(msgs []byte) {
	b.mainFrame(func(frame uintptr) {
		// send_process_message takes the message.
		call(at[cefFrame](frame).sendProcessMessage, frame, pidRenderer, bridgeMessage(msgs))
	})
}

// Eval runs JavaScript in the main frame.
func (b *Browser) Eval(js string) {
	code, empty := newStr(js), newStr("")
	b.mainFrame(func(frame uintptr) {
		call(at[cefFrame](frame).executeJavaScript, frame, code.p(), empty.p(), 0)
		runtime.KeepAlive(code)
		runtime.KeepAlive(empty)
	})
}

// Edit runs an edit command in the focused frame: undo, redo, cut, copy,
// paste, pasteAndMatchStyle, delete or selectAll.
func (b *Browser) Edit(command string) {
	b.do(func() {
		frame := call(at[cefBrowser](b.browser).getFocusedFrame, b.browser)
		if frame == 0 {
			return
		}
		defer release(frame)
		f := at[cefFrame](frame)
		slot := map[string]uintptr{
			"undo": f.undo, "redo": f.redo, "cut": f.cut, "copy": f.copy,
			"paste": f.paste, "pasteAndMatchStyle": f.pasteAndMatchStyle,
			"delete": f.del, "selectAll": f.selectAll,
		}[command]
		if slot != 0 {
			call(slot, frame)
		}
	})
}

// zoomLevel converts a zoom factor to Chromium's zoom level: each level is
// 20% more.
func zoomLevel(factor float64) float64 { return math.Log(factor) / math.Log(1.2) }

// setZoomLevel calls set_zoom_level, which takes a double. CEF gives the
// slot of every browser host the same function.
var setZoomLevel func(host uintptr, level float64)

func (b *Browser) setZoom(factor float64) {
	if setZoomLevel == nil {
		purego.RegisterFunc(&setZoomLevel, at[cefBrowserHost](b.bh).setZoomLevel)
	}
	setZoomLevel(b.bh, zoomLevel(factor))
}

func (b *Browser) SetZoom(factor float64) {
	b.zoom = factor
	b.do(func() { b.setZoom(factor) })
}

func (b *Browser) Zoom() float64 { return b.zoom }

func (b *Browser) setUserAgent(ua string) {
	params, _ := json.Marshal(map[string]string{"userAgent": ua})
	b.devtoolsCall("Emulation.setUserAgentOverride", params, nil)
}

func (b *Browser) SetUserAgent(ua string) {
	b.userAgent = ua
	b.do(func() { b.setUserAgent(ua) })
}

func (b *Browser) UserAgent() string {
	if b.userAgent != "" {
		return b.userAgent
	}
	return ""
}

func (b *Browser) OpenDevTools() {
	if !b.opts.DevTools {
		return
	}
	b.do(func() {
		wi := &cefWindowInfo{size: unsafe.Sizeof(cefWindowInfo{})}
		bs := &cefBrowserSettings{size: unsafe.Sizeof(cefBrowserSettings{})}
		// Its own client counts the DevTools browser among those Quit
		// waits for.
		call(at[cefBrowserHost](b.bh).showDevTools, b.bh, addr(wi), devtoolsClient.ref(0), addr(bs), 0)
		runtime.KeepAlive(wi)
		runtime.KeepAlive(bs)
	})
}

func (b *Browser) CloseDevTools() {
	b.do(func() { call(at[cefBrowserHost](b.bh).closeDevTools, b.bh) })
}

func (b *Browser) IsDevToolsOpened() bool {
	return b.bh != 0 && call(at[cefBrowserHost](b.bh).hasDevTools, b.bh) != 0
}

func (b *Browser) Print() { b.do(func() { call(at[cefBrowserHost](b.bh).print, b.bh) }) }

// browserOf returns the browser of a structure of its object.
func browserOf(self uintptr) *Browser { return ownerOf[*Browser](self) }

// navigation decides a navigation of a frame.
func (b *Browser) willNavigate(frame, request uintptr, userGesture bool) bool {
	main := call(at[cefFrame](frame).isMain, frame) != 0
	req := at[cefRequest](request)
	nav := platform.Navigation{
		URL:           b.appURL(takeStr(call(req.getUrl, request))),
		IsMainFrame:   main,
		UserInitiated: userGesture,
	}
	tt := uint32(call(req.getTransitionType, request))
	if tt&ttSourceMask == ttReload || tt&ttForwardBackFlag != 0 {
		nav.IsReload = true
	}
	if main && b.programmatic {
		b.programmatic = false
		nav.IsReload = true
	}
	return b.host.WillNavigate(nav)
}

func initBrowserClasses() {
	handler := func(i int) func(self uintptr) uintptr {
		return func(self uintptr) uintptr {
			if b := browserOf(self); b != nil {
				return b.obj.ref(i)
			}
			return 0
		}
	}
	client := newClass[cefClient](map[string]any{
		"getLifeSpanHandler":   handler(lifeSpanIdx),
		"getLoadHandler":       handler(loadIdx),
		"getDisplayHandler":    handler(displayIdx),
		"getRequestHandler":    handler(requestIdx),
		"getDownloadHandler":   handler(downloadIdx),
		"getPermissionHandler": handler(permissionIdx),
		"getKeyboardHandler":   handler(keyboardIdx),
		"getDragHandler":       handler(dragIdx),
		"onProcessMessageReceived": func(self, browser, frame uintptr, source int32, message uintptr) int32 {
			defer release(browser)
			defer release(frame)
			defer release(message)
			b := browserOf(self)
			if b == nil || b.closed {
				return 0
			}
			if !isBridgeMessage(message) {
				return 0
			}
			// Only the main frame has the bridge's __mygoPost.
			if call(at[cefFrame](frame).isMain, frame) == 0 {
				return 1
			}
			messageBytes(message, func(data []byte) { b.host.Message(string(data)) })
			return 1
		},
	})
	lifeSpan := newClass[cefLifeSpanHandler](map[string]any{
		"onBeforePopup": onBeforePopup,
		"onAfterCreated": func(self, browser uintptr) {
			b := browserOf(self)
			if b == nil || b.browser != 0 {
				release(browser)
				return
			}
			b.attach(browser) // keeps the reference
			if b.closed {
				call(at[cefBrowserHost](b.bh).closeBrowser, b.bh, 1)
			}
		},
		// CEF asks before it closes a browser: one that Close closes, or
		// whose page called window.close(), which the app may refuse.
		// Answering 0 has CEF close its window, then the browser; 1 cancels.
		"doClose": func(self, browser uintptr) int32 {
			release(browser)
			b := browserOf(self)
			if b == nil {
				return 0
			}
			if !b.closed {
				b.closing = true
				b.host.ClosedByPage()
				b.closing = false
			}
			return int32(cbool(!b.closed))
		},
		"onBeforeClose": func(self, browser uintptr) {
			release(browser)
			b := browserOf(self)
			if b == nil {
				return
			}
			b.closed = true
			browsersByID.Delete(b.id)
			release(b.devtools)
			release(b.bh)
			release(b.browser)
			b.devtools, b.bh, b.browser = 0, 0, 0
			for id, cb := range b.calls {
				delete(b.calls, id)
				cb(nil, errClosed)
			}
			b.obj.release()
			browserClosed()
		},
	})
	load := newClass[cefLoadHandler](map[string]any{
		"onLoadingStateChange": func(self, browser uintptr, isLoading, canGoBack, canGoForward int32) {
			release(browser)
			if b := browserOf(self); b != nil && !b.closed {
				b.loading = isLoading != 0
			}
		},
		"onLoadStart": func(self, browser, frame uintptr, transition int32) {
			defer release(browser)
			defer release(frame)
			if b := browserOf(self); b != nil && !b.closed && call(at[cefFrame](frame).isMain, frame) != 0 {
				u := takeStr(call(at[cefFrame](frame).getUrl, frame))
				b.host.NavigationCommitted(b.appURL(u))
			}
		},
		"onLoadEnd": func(self, browser, frame uintptr, status int32) {
			defer release(browser)
			defer release(frame)
			if b := browserOf(self); b != nil && !b.closed && call(at[cefFrame](frame).isMain, frame) != 0 {
				b.host.LoadFinished()
			}
		},
		"onLoadError": func(self, browser, frame uintptr, code int32, text, failedURL uintptr) {
			defer release(browser)
			defer release(frame)
			b := browserOf(self)
			// Stopped and replaced loads are not failures.
			if b == nil || b.closed || code == errAborted || call(at[cefFrame](frame).isMain, frame) == 0 {
				return
			}
			b.host.LoadFailed(b.appURL(goStr(failedURL)), int(code), goStr(text))
		},
	})
	display := newClass[cefDisplayHandler](map[string]any{
		"onTitleChange": func(self, browser, title uintptr) {
			release(browser)
			if b := browserOf(self); b != nil && !b.closed {
				t := goStr(title)
				var u string
				b.mainFrame(func(frame uintptr) { u = takeStr(call(at[cefFrame](frame).getUrl, frame)) })
				if untitled(t, u) {
					t = "" // as WebKit reports a page without a title
				}
				b.host.TitleChanged(t)
			}
		},
	})
	request := newClass[cefRequestHandler](map[string]any{
		"onBeforeBrowse": func(self, browser, frame, request uintptr, userGesture, isRedirect int32) int32 {
			defer release(browser)
			defer release(frame)
			defer release(request)
			b := browserOf(self)
			if b == nil || b.closed {
				return 1
			}
			// The app's schemes load from http://<scheme>.localhost:
			// links, location and window.open() name them as on other
			// platforms.
			u := takeStr(call(at[cefRequest](request).getUrl, request))
			if web := b.webURL(u); web != u {
				t := newStr(web)
				call(at[cefFrame](frame).loadUrl, frame, t.p())
				runtime.KeepAlive(t)
				return 1
			}
			if b.willNavigate(frame, request, userGesture != 0) {
				if call(at[cefFrame](frame).isMain, frame) != 0 {
					b.host.NavigationStarted(b.appURL(takeStr(call(at[cefRequest](request).getUrl, request))))
				}
				return 0
			}
			return 1
		},
		"onRenderProcessTerminated": func(self, browser uintptr, status, code int32, text uintptr) {
			release(browser)
			b := browserOf(self)
			if b == nil || b.closed {
				return
			}
			reason := map[int32]string{
				tsAbnormalTermination: "crashed", tsProcessWasKilled: "killed",
				tsProcessCrashed: "crashed", tsProcessOom: "exceeded memory limit",
			}[status]
			if reason == "" {
				reason = "crashed"
			}
			b.host.RenderProcessGone(reason)
		},
	})
	download := newClass[cefDownloadHandler](downloadSlots())
	permission := newClass[cefPermissionHandler](permissionSlots())
	keyboard := newClass[cefKeyboardHandler](map[string]any{
		"onPreKeyEvent": func(self, browser, event, osEvent, isShortcut uintptr) int32 {
			release(browser)
			b := browserOf(self)
			e := at[cefKeyEvent](event)
			if b == nil || b.closed || e.type_ != keyeventRawkeydown && e.type_ != keyeventKeyup {
				return 0
			}
			ke := KeyEvent{
				Up:         e.type_ == keyeventKeyup,
				NativeCode: int(e.nativeKeyCode),
				Shift:      e.modifiers&eventflagShiftDown != 0,
				Control:    e.modifiers&eventflagControlDown != 0,
				Alt:        e.modifiers&eventflagAltDown != 0,
				Super:      e.modifiers&eventflagCommandDown != 0,
			}
			return int32(cbool(b.host.Key(ke)))
		},
	})
	drag := newClass[cefDragHandler](map[string]any{
		"onDragEnter": func(self, browser, data uintptr, mask uint32) int32 {
			defer release(browser)
			defer release(data)
			b := browserOf(self)
			d := at[cefDragData](data)
			if b == nil || b.closed || call(d.isFile, data) == 0 {
				return 0
			}
			list := call(lib.stringListAlloc)
			defer call(lib.stringListFree, list)
			call(d.getFileNames, data, list)
			b.host.DragEntered(stringList(list))
			return 0
		},
	})
	browserClasses = []*class{client, lifeSpan, load, display, request, download, permission, keyboard, drag}

	// The windows of DevTools, which CEF makes, count among the browsers.
	devtoolsLifeSpan := newClass[cefLifeSpanHandler](map[string]any{
		"onAfterCreated": func(self, browser uintptr) {
			release(browser)
			browsers++
		},
		"onBeforeClose": func(self, browser uintptr) {
			release(browser)
			browserClosed()
		},
	})
	devtoolsClientClass := newClass[cefClient](map[string]any{
		"getLifeSpanHandler": func(self uintptr) uintptr { return devtoolsClient.ref(1) },
	})
	devtoolsClient = newObject(nil, devtoolsClientClass, devtoolsLifeSpan)
}

// devtoolsClient is the client of the DevTools windows.
var devtoolsClient *object

// untitled reports a title Chromium made from the URL of a page without
// one: the URL, without its scheme for http(s) and file.
func untitled(title, url string) bool {
	if title == url {
		return true
	}
	for _, scheme := range []string{"http://", "https://", "file://"} {
		if rest, ok := strings.CutPrefix(url, scheme); ok && (title == rest || title == strings.TrimSuffix(rest, "/")) {
			return true
		}
	}
	return false
}

// errAborted is ERR_ABORTED of cef_errorcode_t.
const errAborted = -3

var errClosed = errors.New("mygo: the window has been closed")

// onBeforePopup asks the app for a window, through Host.NewWindow, and
// has CEF create the popup's browser in it.
func onBeforePopup(self, browser, frame uintptr, popupID int32, targetURL, targetFrame uintptr, disposition, userGesture int32,
	features, windowInfo, client, settings, extraInfo, noJavascriptAccess uintptr) int32 {
	release(browser)
	release(frame)
	b := browserOf(self)
	if b == nil || b.closed {
		return 1
	}
	f := at[cefPopupFeatures](features)
	req := platform.NewWindowRequest{
		URL:         b.appURL(goStr(targetURL)),
		FrameName:   goStr(targetFrame),
		Disposition: dispositionName(disposition),
		Native:      1, // a popup: its browser comes from CEF
	}
	if f.xSet != 0 {
		req.X = int(f.x)
	}
	if f.ySet != 0 {
		req.Y = int(f.y)
	}
	if f.widthSet != 0 {
		req.Width = int(f.width)
	}
	if f.heightSet != 0 {
		req.Height = int(f.height)
	}
	child, _ := b.host.NewWindow(req).(interface{ CEFBrowser() *Browser })
	var pb *Browser
	if child != nil {
		pb = child.CEFBrowser()
	}
	if pb == nil || !pb.opts.Popup || pb.browser != 0 {
		return 1
	}
	*at[cefWindowInfo](windowInfo) = *pb.windowInfo()
	// The out parameters hold references, which these replace.
	release(*at[uintptr](client))
	*at[uintptr](client) = pb.obj.ref(clientIdx)
	release(*at[uintptr](extraInfo))
	*at[uintptr](extraInfo) = pb.extraInfo()
	s := at[cefBrowserSettings](settings)
	s.backgroundColor = pb.settings().backgroundColor
	return 0
}

func dispositionName(d int32) string {
	switch d {
	case cefWodNewForegroundTab, cefWodNewBackgroundTab:
		return "new-tab"
	case cefWodNewPopup:
		return "new-popup"
	case cefWodNewWindow:
		return "new-window"
	}
	return "default"
}
