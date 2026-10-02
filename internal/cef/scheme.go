//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

// App schemes load from http://<scheme>.localhost/, whose requests a
// scheme handler factory gets on CEF's IO thread. The app answers them on
// the main thread (platform.WindowHandler.SchemeRequest); the body streams
// from the goroutine serving the request straight into the buffers CEF
// reads into (platform.SchemeBodyWriter), holding at most bodyLimit bytes
// while CEF reads slower than the app writes.

const bodyLimit = 1 << 20

var (
	factoryClass, resourceClass *class
	// schemeFactory lives as long as the process.
	schemeFactory *object
	// factories holds the "scheme://domain" factories are registered for.
	factories sync.Map
	// htmlOverrides holds documents of LoadHTML by the URL they load at,
	// served once.
	htmlOverrides sync.Map
	// blankURLs holds the URLs LoadHTML without a base URL loads, which
	// stand for about:blank.
	blankURLs sync.Map
	nextBlank atomic.Int64
)

// blankDomain serves the documents of LoadHTML without a base URL.
const blankDomain = "mygo-blank.localhost"

// RegisterScheme routes the requests of an app scheme to the hosts of the
// browsers making them. Call it before loading its URLs.
func RegisterScheme(scheme string) { registerFactory("http", scheme+".localhost") }

func registerFactory(scheme, domain string) {
	if _, loaded := factories.LoadOrStore(scheme+"://"+domain, true); loaded {
		return
	}
	s, d := newStr(scheme), newStr(domain)
	call(lib.registerSchemeHandlerFactory, s.p(), d.p(), schemeFactory.ref(0))
	runtime.KeepAlive(s)
	runtime.KeepAlive(d)
}

// blankURL returns a URL that serves html once and stands for about:blank.
func blankURL(html string) string {
	registerFactory("http", blankDomain)
	u := "http://" + blankDomain + "/" + strconv.FormatInt(nextBlank.Add(1), 10)
	htmlOverrides.Store(u, html)
	blankURLs.Store(u, true)
	return u
}

// overrideURL serves html once at target, an http(s) URL.
func overrideURL(target, html string) {
	scheme, rest, _ := strings.Cut(target, "://")
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, ":")
	registerFactory(scheme, host)
	htmlOverrides.Store(target, html)
}

func isBlank(u string) bool {
	_, ok := blankURLs.Load(u)
	return ok
}

func initSchemeClasses() {
	factoryClass = newClass[cefSchemeHandlerFactory](map[string]any{
		"create": func(self, browser, frame, schemeName, request uintptr) uintptr {
			defer release(browser)
			defer release(frame)
			defer release(request)
			u := takeStr(call(at[cefRequest](request).getUrl, request))
			if html, ok := htmlOverrides.LoadAndDelete(u); ok {
				return newStaticResource(http.StatusOK, "text/html; charset=utf-8", []byte(html.(string)))
			}
			rest, ok := strings.CutPrefix(u, "http://")
			host, _, _ := strings.Cut(rest, "/")
			scheme, isApp := strings.CutSuffix(host, ".localhost")
			if !ok || !isApp {
				return 0 // a domain LoadHTML used: the network answers
			}
			if host == blankDomain {
				return newStaticResource(http.StatusNotFound, "text/plain", nil)
			}
			var b *Browser
			if browser != 0 {
				if v, ok := browsersByID.Load(browserID(browser)); ok {
					b = v.(*Browser)
				}
			}
			if b == nil {
				b = anyBrowser(scheme)
			}
			if b == nil {
				return newStaticResource(http.StatusNotFound, "text/plain", nil)
			}
			return newResource(b)
		},
	})
	resourceClass = newClass[cefResourceHandler](map[string]any{
		"open": func(self, request, handleRequest, callback uintptr) int32 {
			r := ownerOf[*resource](self)
			if r == nil {
				release(request)
				release(callback)
				return 0
			}
			return r.open(request, handleRequest, callback)
		},
		"getResponseHeaders": func(self, response, length, redirect uintptr) {
			defer release(response)
			if r := ownerOf[*resource](self); r != nil {
				r.headers(response, at[int64](length))
			}
		},
		"read": func(self, data uintptr, size int32, read, callback uintptr) int32 {
			r := ownerOf[*resource](self)
			if r == nil {
				release(callback)
				*at[int32](read) = 0
				return 0
			}
			return r.read(data, int(size), at[int32](read), callback)
		},
		"cancel": func(self uintptr) {
			if r := ownerOf[*resource](self); r != nil {
				r.canceled()
			}
		},
	})
	schemeFactory = newObject(nil, factoryClass)
}

// anyBrowser returns a browser of the scheme's app for requests no browser
// makes, such as a service worker's.
func anyBrowser(scheme string) *Browser {
	var found *Browser
	browsersByID.Range(func(_, v any) bool {
		b := v.(*Browser)
		if b.hasScheme(scheme) {
			found = b
			return false
		}
		return true
	})
	return found
}

// resource answers one request of an app scheme. open, headers, read and
// canceled run on CEF's IO thread; Respond, Write, Finish and Fail on the
// main thread; WriteBody on the goroutine serving the request.
type resource struct {
	obj    *object
	b      *Browser
	url    string // the app URL
	ctx    context.Context
	cancel context.CancelFunc

	// navigation reports a frame's document, which a download replaces.
	navigation bool

	mu        sync.Mutex
	cond      *sync.Cond
	opened    uintptr // the cef_callback_t that continues open, until Respond
	status    int
	header    http.Header
	responded bool
	body      []byte // received, not read yet
	done      bool   // Finish
	err       error  // Fail after Respond
	gone      bool   // canceled by CEF
	// A read waiting for data: CEF keeps data valid until callback runs.
	wait struct {
		data     uintptr
		size     int
		callback uintptr
	}
}

// newResource returns a resource handler, with a reference for CEF.
func newResource(b *Browser) uintptr {
	r := &resource{b: b}
	r.cond = sync.NewCond(&r.mu)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.obj = newObject(r, resourceClass)
	return r.obj.ptr(0) // newObject's reference goes to CEF
}

// newStaticResource returns a resource handler answering with body.
func newStaticResource(status int, contentType string, body []byte) uintptr {
	r := &resource{status: status, header: http.Header{"Content-Type": {contentType}}, responded: true, body: body, done: true}
	r.cond = sync.NewCond(&r.mu)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.header.Set("Content-Length", strconv.Itoa(len(body)))
	r.obj = newObject(r, resourceClass)
	return r.obj.ptr(0)
}

func (r *resource) open(request, handleRequest, callback uintptr) int32 {
	if r.b == nil { // static
		release(request)
		release(callback)
		*at[int32](handleRequest) = 1
		return 1
	}
	req := r.request(request)
	switch call(at[cefRequest](request).getResourceType, request) {
	case rtMainFrame, rtSubFrame:
		r.navigation = true
	}
	release(request)
	r.mu.Lock()
	r.opened = callback // kept until Respond
	r.mu.Unlock()
	*at[int32](handleRequest) = 0
	b := r.b
	r.obj.refs.Add(1) // for the main thread
	opts.Post(func() {
		defer r.obj.release()
		if b.closed {
			r.Fail(errClosed)
			return
		}
		b.host.SchemeRequest(req)
	})
	return 1
}

// request reads a cef_request_t.
func (r *resource) request(request uintptr) *platform.SchemeRequest {
	req := at[cefRequest](request)
	r.url = r.b.appURL(takeStr(call(req.getUrl, request)))
	sr := &platform.SchemeRequest{
		Context:   r.ctx,
		Method:    takeStr(call(req.getMethod, request)),
		URL:       r.url,
		Header:    http.Header{},
		Responder: r,
	}
	headers := call(lib.multimapAlloc)
	call(req.getHeaderMap, request, headers)
	for i, n := uintptr(0), call(lib.multimapSize, headers); i < n; i++ {
		var k, v cefString
		call(lib.multimapKey, headers, i, addr(&k))
		call(lib.multimapValue, headers, i, addr(&v))
		sr.Header.Add(decodeUTF16(k.str, k.length), decodeUTF16(v.str, v.length))
		call(lib.stringUTF16Clear, addr(&k))
		call(lib.stringUTF16Clear, addr(&v))
	}
	call(lib.multimapFree, headers)
	if post := call(req.getPostData, request); post != 0 {
		sr.Body = io.NopCloser(bytes.NewReader(postData(post)))
		release(post)
	}
	return sr
}

// postData reads the elements of a cef_post_data_t.
func postData(post uintptr) []byte {
	pd := at[cefPostData](post)
	n := call(pd.getElementCount, post)
	if n == 0 {
		return nil
	}
	elems := make([]uintptr, n)
	count := n
	call(pd.getElements, post, addr(&count), addr(unsafe.SliceData(elems)))
	var out []byte
	for _, e := range elems[:count] {
		el := at[cefPostDataElement](e)
		switch call(el.getType, e) {
		case pdeTypeBytes:
			size := call(el.getBytesCount, e)
			start := len(out)
			out = append(out, make([]byte, size)...)
			if size > 0 {
				call(el.getBytes, e, size, addr(&out[start]))
			}
		case pdeTypeFile:
			if data, err := os.ReadFile(takeStr(call(el.getFile, e))); err == nil {
				out = append(out, data...)
			}
		}
		release(e)
	}
	return out
}

func (r *resource) headers(response uintptr, length *int64) {
	r.mu.Lock()
	status, header := r.status, r.header
	r.mu.Unlock()
	resp := at[cefResponse](response)
	call(resp.setStatus, response, uintptr(status))
	ct := header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if mt, params, err := mime.ParseMediaType(ct); err == nil {
		m := newStr(mt)
		call(resp.setMimeType, response, m.p())
		runtime.KeepAlive(m)
		if cs := params["charset"]; cs != "" {
			c := newStr(cs)
			call(resp.setCharset, response, c.p())
			runtime.KeepAlive(c)
		}
	}
	for k, vs := range header {
		for _, v := range vs {
			ks, vs := newStr(k), newStr(v)
			call(resp.setHeaderByName, response, ks.p(), vs.p(), 0)
			runtime.KeepAlive(ks)
			runtime.KeepAlive(vs)
		}
	}
	*length = -1
	if n, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64); err == nil {
		*length = n
	}
}

func (r *resource) read(data uintptr, size int, read *int32, callback uintptr) int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.body) > 0 {
		n := copy(unsafe.Slice(at[byte](data), size), r.body)
		r.body = r.body[n:]
		r.cond.Broadcast()
		*read = int32(n)
		release(callback)
		return 1
	}
	if r.err != nil || r.gone {
		*read = -2 // ERR_FAILED
		release(callback)
		return 0
	}
	if r.done {
		*read = 0
		release(callback)
		return 0
	}
	r.wait.data, r.wait.size, r.wait.callback = data, size, callback
	*read = 0
	return 1
}

// fill copies p into a waiting read, if there is one, and returns the
// rest. r.mu is held; the read's callback runs after unlocking it.
func (r *resource) fill(p []byte) (rest []byte, cont func()) {
	w := &r.wait
	if w.callback == 0 || len(p) == 0 {
		return p, nil
	}
	n := copy(unsafe.Slice(at[byte](w.data), w.size), p)
	cb := w.callback
	w.callback = 0
	return p[n:], func() {
		call(at[cefResourceReadCallback](cb).cont, cb, uintptr(n))
		release(cb)
	}
}

// Respond continues open with the head of the response. A page that is a
// download is served again into the file (SchemeDownload): CEF cannot turn
// what a scheme handler serves into one.
func (r *resource) Respond(status int, header http.Header) {
	if r.navigation && isDownload(header) {
		r.Fail(errDownload)
		if !r.b.closed {
			r.b.host.SchemeDownload(r.url)
		}
		return
	}
	r.mu.Lock()
	if r.responded || r.gone {
		r.mu.Unlock()
		return
	}
	r.responded, r.status, r.header = true, status, header.Clone()
	cb := r.opened
	r.opened = 0
	r.mu.Unlock()
	if cb != 0 {
		call(at[cefCallback](cb).cont, cb)
		release(cb)
	}
}

func (r *resource) Write(p []byte) {
	r.mu.Lock()
	rest, cont := r.fill(p)
	if len(rest) > 0 && !r.gone {
		r.body = append(r.body, rest...)
	}
	r.mu.Unlock()
	if cont != nil {
		cont()
	}
}

var (
	errGone     = errors.New("mygo: the page no longer reads the response")
	errDownload = errors.New("mygo: the response is a download")
)

// isDownload reports whether a page response is a file to save: an
// attachment, or plain bytes.
func isDownload(h http.Header) bool {
	d := strings.ToLower(strings.TrimSpace(h.Get("Content-Disposition")))
	return strings.HasPrefix(d, "attachment") || strings.HasPrefix(h.Get("Content-Type"), "application/octet-stream")
}

// WriteBody writes p, waiting while CEF has not read enough of what came
// before.
func (r *resource) WriteBody(p []byte) error {
	for len(p) > 0 {
		r.mu.Lock()
		for !r.gone && r.wait.callback == 0 && len(r.body) >= bodyLimit {
			r.cond.Wait()
		}
		if r.gone {
			r.mu.Unlock()
			return errGone
		}
		rest, cont := r.fill(p)
		if cont == nil {
			r.body = append(r.body, rest...)
			rest = nil
		}
		r.mu.Unlock()
		if cont != nil {
			cont()
		}
		p = rest
	}
	return nil
}

func (r *resource) Finish() {
	r.mu.Lock()
	if !r.responded {
		r.mu.Unlock()
		r.Respond(http.StatusOK, http.Header{})
		r.mu.Lock()
	}
	r.done = true
	var cb uintptr
	if r.wait.callback != 0 && len(r.body) == 0 {
		cb = r.wait.callback
		r.wait.callback = 0
	}
	r.mu.Unlock()
	if cb != 0 {
		call(at[cefResourceReadCallback](cb).cont, cb, 0) // the end
		release(cb)
	}
}

func (r *resource) Fail(err error) {
	r.mu.Lock()
	if !r.responded {
		cb := r.opened
		r.opened, r.responded, r.done = 0, true, true
		r.mu.Unlock()
		if cb != 0 {
			call(at[cefCallback](cb).cancel, cb)
			release(cb)
		}
		return
	}
	r.err = err
	cb := r.wait.callback
	r.wait.callback = 0
	r.mu.Unlock()
	if cb != 0 {
		call(at[cefResourceReadCallback](cb).cont, cb, uintptr(^uint32(1))) // -2, ERR_FAILED
		release(cb)
	}
}

// canceled runs when CEF no longer wants the response.
func (r *resource) canceled() {
	r.mu.Lock()
	r.gone = true
	opened, waiting := r.opened, r.wait.callback
	r.opened, r.wait.callback = 0, 0
	r.cond.Broadcast()
	r.mu.Unlock()
	release(opened)
	release(waiting)
	r.cancel()
}
