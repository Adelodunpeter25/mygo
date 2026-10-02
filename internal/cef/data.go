//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"encoding/json"
	"net/url"
	"runtime"
	"strings"

	"github.com/egoist/mygo/internal/platform"
)

var deleteCookiesClass *class

func initDataClasses() {
	deleteCookiesClass = newClass[cefDeleteCookiesCallback](map[string]any{
		"onComplete": func(self uintptr, deleted int32) {
			if fn := ownerOf[func()](self); fn != nil {
				fn()
			}
		},
	})
}

// ClearBrowsingData deletes the cookies, the HTTP cache and the storage of
// the app's origins and of the pages open: CEF has no call that clears
// every origin's. done runs on the main thread.
func ClearBrowsingData(done func(error)) {
	steps := 1
	var first error
	step := func(err error) {
		if err != nil && first == nil {
			first = err
		}
		if steps--; steps == 0 {
			done(first)
		}
	}
	var open []*Browser
	origins := map[string]bool{}
	factories.Range(func(k, _ any) bool {
		if s, ok := strings.CutPrefix(k.(string), "http://"); ok && strings.HasSuffix(s, ".localhost") {
			origins["http://"+s] = true
		}
		return true
	})
	browsersByID.Range(func(_, v any) bool {
		b := v.(*Browser)
		if b.closed {
			return true
		}
		open = append(open, b)
		b.mainFrame(func(frame uintptr) {
			if u, err := url.Parse(takeStr(call(at[cefFrame](frame).getUrl, frame))); err == nil && u.Host != "" {
				origins[u.Scheme+"://"+u.Host] = true
			}
		})
		return true
	})
	if len(open) > 0 {
		b := open[0]
		steps++
		b.devtoolsCall("Network.clearBrowserCache", nil, func(_ []byte, err error) { step(err) })
		for o := range origins {
			params, _ := json.Marshal(map[string]string{"origin": o, "storageTypes": "all"})
			steps++
			b.devtoolsCall("Storage.clearDataForOrigin", params, func(_ []byte, err error) { step(err) })
		}
	}
	manager := call(lib.cookieManagerGetGlobalManager, 0)
	if manager == 0 {
		step(nil)
		return
	}
	defer release(manager)
	cb := newObject(func() { opts.Post(func() { step(nil) }) }, deleteCookiesClass)
	empty := newStr("")
	ok := call(at[cefCookieManager](manager).deleteCookies, manager, empty.p(), empty.p(), cb.ptr(0))
	runtime.KeepAlive(empty)
	if ok == 0 {
		step(nil)
	}
}

// SetBackgroundColor sets the color the page shows where it paints none.
func (b *Browser) SetBackgroundColor(c platform.Color) {
	params, _ := json.Marshal(map[string]any{"color": map[string]any{"r": c.R, "g": c.G, "b": c.B, "a": 1}})
	b.do(func() { b.devtoolsCall("Emulation.setDefaultBackgroundColorOverride", params, nil) })
}
