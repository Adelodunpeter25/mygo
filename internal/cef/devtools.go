//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

// The DevTools protocol, in process: Eval with a result, PDFs, snapshots
// and the user agent use it, as the WebView2 backend does. One observer
// gets the results of every browser.

var (
	devtoolsClass    *class
	devtoolsObserver *object
	nextMessage      int32
)

func initDevToolsClass() {
	devtoolsClass = newClass[cefDevToolsMessageObserver](map[string]any{
		"onDevToolsMethodResult": func(self, browser uintptr, id, success int32, result, size uintptr) {
			defer release(browser)
			v, ok := browsersByID.Load(browserID(browser))
			if !ok {
				return
			}
			b := v.(*Browser)
			cb := b.calls[id]
			if cb == nil {
				return
			}
			delete(b.calls, id)
			data := append([]byte(nil), unsafe.Slice(at[byte](result), size)...)
			if success == 0 {
				var e struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(data, &e)
				cb(nil, fmt.Errorf("mygo: %s", e.Message))
				return
			}
			cb(data, nil)
		},
	})
	devtoolsObserver = newObject(nil, devtoolsClass)
}

// devtoolsCall calls a DevTools protocol method; done, which may be nil,
// gets the result on the main thread.
func (b *Browser) devtoolsCall(method string, params json.RawMessage, done func([]byte, error)) {
	if b.closed || b.bh == 0 {
		if done != nil {
			done(nil, errClosed)
		}
		return
	}
	nextMessage++
	id := nextMessage
	msg, _ := json.Marshal(struct {
		ID     int32           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params,omitempty"`
	}{id, method, params})
	if done != nil {
		b.calls[id] = done
	}
	if call(at[cefBrowserHost](b.bh).sendDevToolsMessage, b.bh, addr(unsafe.SliceData(msg)), uintptr(len(msg))) == 0 {
		delete(b.calls, id)
		if done != nil {
			done(nil, fmt.Errorf("mygo: %s failed", method))
		}
	}
}

// CallAsyncFunction runs body as the body of an async function in the main
// frame and reports what it returns, a string. The DevTools protocol awaits
// promises, reports syntax errors and ignores the page's CSP. The code runs
// as a user's action, as WebKit runs evaluated code: it may open windows
// and start downloads.
func (b *Browser) CallAsyncFunction(body string, cb func(string, error)) {
	params, _ := json.Marshal(map[string]any{
		"expression":    "(async () => {\n" + body + "\n})()",
		"awaitPromise":  true,
		"returnByValue": true,
		"userGesture":   true,
	})
	b.do(func() {
		b.devtoolsCall("Runtime.evaluate", params, func(res []byte, err error) {
			if err != nil {
				cb("", err)
				return
			}
			var out struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
				ExceptionDetails *struct {
					Text      string `json:"text"`
					Exception *struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			if err := json.Unmarshal(res, &out); err != nil {
				cb("", err)
				return
			}
			if d := out.ExceptionDetails; d != nil {
				msg := d.Text
				if d.Exception != nil && d.Exception.Description != "" {
					msg = d.Exception.Description
				}
				cb("", errors.New(msg))
				return
			}
			var s string
			_ = json.Unmarshal(out.Result.Value, &s)
			cb(s, nil)
		})
	})
	if b.closed {
		cb("", errClosed)
	}
}

// CapturePage takes a PNG of the visible page.
func (b *Browser) CapturePage(cb func([]byte, error)) {
	b.do(func() {
		b.devtoolsCall("Page.captureScreenshot", json.RawMessage(`{"format":"png"}`), func(res []byte, err error) {
			cb(decodeData(res, err))
		})
	})
	if b.closed {
		cb(nil, errClosed)
	}
}

// PrintToPDF renders the page as a PDF, as Chromium's headless mode does.
func (b *Browser) PrintToPDF(o platform.PDFOptions, cb func([]byte, error)) {
	params, _ := json.Marshal(map[string]any{
		"landscape":         o.Landscape,
		"printBackground":   o.Background,
		"paperWidth":        o.PageWidth,
		"paperHeight":       o.PageHeight,
		"marginTop":         o.MarginTop,
		"marginRight":       o.MarginRight,
		"marginBottom":      o.MarginBottom,
		"marginLeft":        o.MarginLeft,
		"preferCSSPageSize": false,
	})
	b.do(func() {
		b.devtoolsCall("Page.printToPDF", params, func(res []byte, err error) {
			pdf, err := decodeData(res, err)
			if err != nil {
				err = fmt.Errorf("mygo: printing to PDF: %w", err)
			}
			cb(pdf, err)
		})
	})
	if b.closed {
		cb(nil, errClosed)
	}
}

// decodeData returns the base64 data of a result.
func decodeData(res []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}
