// Package surface connects the content of a window that MyGo draws itself
// (package ui) to the window (package mygo), without either importing the
// other's internals.
package surface

import "github.com/egoist/mygo/internal/platform"

// Conn is a window's side of the connection. Package mygo fills it before
// calling Content.AttachContent; the content sets the hooks it handles.
// Everything runs on the main thread, except Invalidate.
type Conn struct {
	Surface platform.Surface
	// Window is the *mygo.Window.
	Window any
	// Clipboard is the system clipboard.
	Clipboard platform.Clipboard
	// StartDrag moves the window with the pointer, for drag regions;
	// TitleBarDoubleClicked does what a double click on a title bar does.
	StartDrag             func()
	TitleBarDoubleClicked func()
	// IsDark reports the system's dark appearance.
	IsDark func() bool
	// TitleBar returns the room the window controls take in a window with
	// a hidden title bar, zero in other windows.
	TitleBar func() platform.TitleBar
	// OpenURL opens a link in the default browser.
	OpenURL func(url string)
	// Invalidate asks for a frame; it is safe from any goroutine.
	Invalidate func()

	// Event receives the surface's events, and Focus and Blur of the
	// window.
	Event func(ev platform.SurfaceEvent)
	// ThemeChanged is called when the system appearance changes, and
	// TitleBarChanged when TitleBar does.
	ThemeChanged    func()
	TitleBarChanged func()
	// Capture renders the content as it is now into premultiplied RGBA.
	Capture func() (width, height int, rgba []byte)
	// Detach is called once the window is closed.
	Detach func()
}

// Content is implemented by package ui.
type Content interface {
	AttachContent(conn *Conn)
}
