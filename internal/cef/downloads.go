//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"errors"
	"net/url"
	"runtime"
)

// Downloads go where the app says (DownloadStarted): CEF cancels one whose
// callback is released without running it.

type download struct {
	url, path string
}

// downloads holds the downloads under way by id. Main thread only.
var downloads = map[uint32]*download{}

var errDownloadCanceled = errors.New("mygo: the download was canceled")

func downloadSlots() map[string]any {
	return map[string]any{
		// A slot left NULL answers false, which would cancel every
		// download: CEF's C++ default is true.
		"canDownload": func(self, browser, url, method uintptr) int32 {
			release(browser)
			return 1
		},
		"onBeforeDownload": func(self, browser, item, suggested, callback uintptr) int32 {
			release(browser)
			defer release(item)
			defer release(callback)
			b := browserOf(self)
			if b == nil || b.closed {
				return 1
			}
			it := at[cefDownloadItem](item)
			u := b.appURL(takeStr(call(it.getUrl, item)))
			path := b.host.DownloadStarted(u, goStr(suggested))
			if path == "" {
				return 1 // released without running: canceled
			}
			downloads[uint32(call(it.getId, item))] = &download{url: u, path: path}
			p := newStr(path)
			call(at[cefBeforeDownloadCallback](callback).cont, callback, p.p(), 0)
			runtime.KeepAlive(p)
			return 1
		},
		"onDownloadUpdated": func(self, browser, item, callback uintptr) {
			release(browser)
			defer release(item)
			defer release(callback)
			b := browserOf(self)
			it := at[cefDownloadItem](item)
			id := uint32(call(it.getId, item))
			d := downloads[id]
			if b == nil || d == nil {
				return
			}
			var err error
			switch {
			case call(it.isComplete, item) != 0:
			case call(it.isCanceled, item) != 0:
				err = errDownloadCanceled
			case call(it.isInterrupted, item) != 0:
				err = errors.New("mygo: the download failed")
			default:
				return // under way
			}
			delete(downloads, id)
			b.host.DownloadFinished(d.url, d.path, err)
		},
	}
}

// Camera, microphone, location and notifications go to the app; CEF gives
// other requests its default answer.

func permissionSlots() map[string]any {
	return map[string]any{
		"onRequestMediaAccessPermission": func(self, browser, frame, origin uintptr, requested uint32, callback uintptr) int32 {
			release(browser)
			release(frame)
			defer release(callback)
			b := browserOf(self)
			cb := at[cefMediaAccessCallback](callback)
			var kinds []string
			if requested&cefMediaPermissionDeviceVideoCapture != 0 {
				kinds = append(kinds, "camera")
			}
			if requested&cefMediaPermissionDeviceAudioCapture != 0 {
				kinds = append(kinds, "microphone")
			}
			if b == nil || b.closed || len(kinds) == 0 || requested&^(cefMediaPermissionDeviceVideoCapture|cefMediaPermissionDeviceAudioCapture) != 0 {
				call(cb.cancel, callback)
				return 1
			}
			if b.host.PermissionRequested(kinds, b.origin(goStr(origin))) {
				call(cb.cont, callback, uintptr(requested))
			} else {
				call(cb.cancel, callback)
			}
			return 1
		},
		"onShowPermissionPrompt": func(self, browser uintptr, promptID uint64, origin uintptr, requested uint32, callback uintptr) int32 {
			release(browser)
			defer release(callback)
			b := browserOf(self)
			kinds := map[uint32]string{
				cefPermissionTypeCameraStream:  "camera",
				cefPermissionTypeMicStream:     "microphone",
				cefPermissionTypeGeolocation:   "geolocation",
				cefPermissionTypeNotifications: "notifications",
			}
			var asked []string
			for bit, kind := range kinds {
				if requested&bit != 0 {
					asked = append(asked, kind)
					requested &^= bit
				}
			}
			if b == nil || b.closed || len(asked) == 0 || requested != 0 {
				return 0
			}
			result := cefPermissionResultDeny
			if b.host.PermissionRequested(asked, b.origin(goStr(origin))) {
				result = cefPermissionResultAccept
			}
			call(at[cefPermissionPromptCallback](callback).cont, callback, uintptr(result))
			return 1
		},
	}
}

// origin returns the app origin of a requesting origin.
func (b *Browser) origin(o string) string {
	u, err := url.Parse(b.appURL(o))
	if err != nil {
		return o
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}
