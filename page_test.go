package mygo

import (
	"errors"
	"slices"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestPage(t *testing.T) {
	w := NewWindow(WindowOptions{Hidden: true, URL: "https://example.com/a", Page: PageOptions{
		PreloadScript: "window.preloaded = true", UserAgent: "agent", ZoomFactor: 1.5, DevTools: DevToolsEnabled,
	}})
	t.Cleanup(w.Destroy)
	fw := fb.Windows()[len(fb.Windows())-1]
	p := w.Page()
	if p == nil || p.Window() != w {
		t.Fatalf("Page %v", p)
	}
	o := fw.Opts
	preloaded := slices.ContainsFunc(o.UserScripts, func(s platform.UserScript) bool { return s.Source == "window.preloaded = true" })
	if o.UserAgent != "agent" || o.Zoom != 1.5 || !o.DevTools || !preloaded {
		t.Errorf("page options: user agent %q, zoom %v, devtools %v, preloaded %v", o.UserAgent, o.Zoom, o.DevTools, preloaded)
	}
	if got := p.URL(); got != "https://example.com/a" {
		t.Errorf("URL %q", got)
	}
	if err := p.LoadURL("https://example.com/b"); err != nil || p.URL() != "https://example.com/b" {
		t.Errorf("LoadURL: %v, URL %q", err, p.URL())
	}
	if _, err := EvalAs[int]((*Page)(nil), "1"); !errors.Is(err, errNoPage) {
		t.Errorf("EvalAs of no page: %v", err)
	}
}

// The page methods and options windows had before Page keep working
// until they go.
func TestDeprecatedPageMethods(t *testing.T) {
	w := NewWindow(WindowOptions{Hidden: true, UserAgent: "old", ZoomFactor: 2, TrustedOrigins: []string{"https://example.com"},
		Page: PageOptions{UserAgent: "new"}})
	t.Cleanup(w.Destroy)
	fw := fb.Windows()[len(fb.Windows())-1]
	if o := fw.Opts; o.UserAgent != "new" || o.Zoom != 2 {
		t.Errorf("user agent %q, zoom %v: Page should win, the old fields fill in", o.UserAgent, o.Zoom)
	}
	if !w.isTrusted("https://example.com/page") {
		t.Error("the deprecated TrustedOrigins were not trusted")
	}
	w.LoadURL("https://example.com/c")
	if w.URL() != "https://example.com/c" || w.Page().URL() != w.URL() {
		t.Errorf("URL %q", w.URL())
	}
	w.OpenDevTools()
	if !w.IsDevToolsOpened() || !w.Page().IsDevToolsOpened() {
		t.Error("OpenDevTools did not open them")
	}
}
