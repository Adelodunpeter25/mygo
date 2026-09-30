package mygo

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/internal/tsgen"
)

// PageAPI is a set of functions that pages implement for Go, which calls
// them like methods: the reverse of Bind. T is a struct whose exported
// fields are the functions:
//
//	// Editor is what the editor's page does for Go.
//	type Editor struct {
//		// Text returns the text being edited.
//		Text func(ctx context.Context) (string, error)
//		// Open shows a document.
//		Open func(ctx context.Context, name, text string) error
//	}
//
//	var editor = mygo.NewPageAPI[Editor]()
//
// In returns them for the page of a window:
//
//	text, err := editor.In(win).Text(ctx)
//
// and the page implements them with the function `mygo generate` writes
// for T, exposeEditor here:
//
//	exposeEditor({
//	  text: () => textarea.value,
//	  open(name, text) { ... },
//	});
//
// A function may take a context.Context first, then any number of
// JSON-encodable arguments, including a variadic one, and returns an
// error, or a value and an error. In the page it is named in lower camel
// case (Text is text), and the page may answer with a value or a promise.
//
// A call goes to the page the window shows, and waits for its DOM to be
// ready, like events: calls made right after creating a window, or during
// a navigation, reach the next page. It fails with ErrNotExposed when the
// page does not expose the function, with a *PageError when the function
// throws or its promise rejects, and when the page navigates away or the
// window closes before it answered. Only trusted pages are called (see
// WindowOptions.TrustedOrigins). The context stops the wait, not the
// page's function.
//
// Calls are safe from any goroutine. On the main thread, in an event
// listener for example, they keep processing native events while they
// wait, like Window.Eval.
type PageAPI[T any] struct {
	api *pageAPI
}

// ErrNotExposed is returned by the functions of a PageAPI when the page does
// not expose the function called.
var ErrNotExposed = errors.New("mygo: not exposed by the page")

// PageError is returned by the functions of a PageAPI when the page's
// function throws or its promise rejects.
type PageError struct {
	// Function is the function that failed, as the page names it, e.g.
	// "Editor.open".
	Function string
	// Message is the message of what the page threw.
	Message string
}

func (e *PageError) Error() string { return "mygo: " + e.Function + ": " + e.Message }

// pageAPI is a page API of any type.
type pageAPI struct {
	// name is that of the type, which the page exposes its functions under.
	name  string
	typ   reflect.Type
	funcs []*pageFunc
	pc    uintptr // of the declaration
}

// pageFunc is a function of a page API, a field of its type.
type pageFunc struct {
	// name is what the page calls it, e.g. "Editor.text".
	name     string
	goName   string
	field    int
	typ      reflect.Type
	ctx      bool
	params   []reflect.Type
	variadic bool
	// result is nil for functions that only return an error.
	result reflect.Type
}

// NewPageAPI declares the page API T, a struct of functions (see
// PageAPI). Declare page APIs at package level so `mygo generate` includes
// them in the TypeScript client:
//
//	var editor = mygo.NewPageAPI[Editor]()
//
// The page exposes the functions under the name of T. NewPageAPI panics if
// the name is taken or a function has parameters or results that cannot be
// encoded as JSON, so mistakes show at startup.
func NewPageAPI[T any]() *PageAPI[T] {
	api, err := newPageAPI(reflect.TypeFor[T]())
	if err != nil {
		panic(err)
	}
	api.pc, _, _, _ = runtime.Caller(1)
	ipc.Lock()
	defer ipc.Unlock()
	for _, a := range ipc.pages {
		if a.name == api.name {
			panic(fmt.Sprintf("mygo: a page API named %s is already declared", api.name))
		}
	}
	ipc.pages = append(ipc.pages, api)
	return &PageAPI[T]{api: api}
}

func newPageAPI(t reflect.Type) (*pageAPI, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("mygo: NewPageAPI[%s]: the type must be a struct of functions", t)
	}
	name := tsgen.TypeName(t.Name())
	if t.Name() == "" || !tsgen.IsIdentifier(name) {
		return nil, fmt.Errorf("mygo: NewPageAPI[%s]: the type must have a name", t)
	}
	api := &pageAPI{name: name, typ: t}
	fields := map[string]string{}
	for i := range t.NumField() {
		f := t.Field(i)
		where := name + "." + f.Name
		if f.Anonymous {
			return nil, fmt.Errorf("mygo: %s: embedded fields are not supported in page APIs", where)
		}
		if !f.IsExported() {
			continue
		}
		if f.Type.Kind() != reflect.Func {
			return nil, fmt.Errorf("mygo: %s is not a function", where)
		}
		fn, err := newPageFunc(name, i, f)
		if err != nil {
			return nil, err
		}
		if other, ok := fields[fn.name]; ok {
			return nil, fmt.Errorf("mygo: %s.%s and %s are both %s in the page", name, other, where, fn.name)
		}
		fields[fn.name] = f.Name
		api.funcs = append(api.funcs, fn)
	}
	if len(api.funcs) == 0 {
		return nil, fmt.Errorf("mygo: page API %s has no functions", name)
	}
	return api, nil
}

func newPageFunc(api string, index int, f reflect.StructField) (*pageFunc, error) {
	ft := f.Type
	where := api + "." + f.Name
	fn := &pageFunc{name: api + "." + tsgen.Camel(f.Name), goName: f.Name, field: index, typ: ft, variadic: ft.IsVariadic()}
	start := 0
	if ft.NumIn() > 0 && ft.In(0) == contextType {
		fn.ctx, start = true, 1
	}
	for i := start; i < ft.NumIn(); i++ {
		p := ft.In(i)
		if hasChannel(p, map[reflect.Type]bool{}) {
			return nil, fmt.Errorf("mygo: %s: parameter %d: channels are parameters of bound methods only", where, i-start+1)
		}
		if err := validateType(p); err != nil {
			return nil, fmt.Errorf("mygo: %s: parameter %d: %w", where, i-start+1, err)
		}
		fn.params = append(fn.params, p)
	}
	switch {
	case ft.NumOut() == 1 && ft.Out(0) == errorType:
	case ft.NumOut() == 2 && ft.Out(1) == errorType:
		fn.result = ft.Out(0)
		if err := validateType(fn.result); err != nil {
			return nil, fmt.Errorf("mygo: %s: result: %w", where, err)
		}
	default:
		return nil, fmt.Errorf("mygo: %s must return an error, or a value and an error", where)
	}
	return fn, nil
}

// In returns the functions of the page that w shows: each call goes to the
// page shown at that time (see PageAPI). A nil window, for example the
// FocusedWindow when none is, makes them fail.
func (p *PageAPI[T]) In(w *Window) *T {
	if p == nil || p.api == nil {
		panic("mygo: PageAPI.In on a page API not declared with NewPageAPI")
	}
	v := reflect.New(p.api.typ)
	s := v.Elem()
	for _, fn := range p.api.funcs {
		s.Field(fn.field).Set(reflect.MakeFunc(fn.typ, func(in []reflect.Value) []reflect.Value {
			return fn.call(w, in)
		}))
	}
	return v.Interface().(*T)
}

// call calls the function in the page of w with the arguments of in.
func (fn *pageFunc) call(w *Window, in []reflect.Value) []reflect.Value {
	ctx := context.Background()
	if fn.ctx {
		if c, ok := in[0].Interface().(context.Context); ok && c != nil {
			ctx = c
		}
		in = in[1:]
	}
	raw, err := fn.invoke(ctx, w, in)
	errv := reflect.Zero(errorType)
	if fn.result == nil {
		if err != nil {
			errv = reflect.ValueOf(&err).Elem()
		}
		return []reflect.Value{errv}
	}
	result := reflect.New(fn.result)
	if err == nil && len(raw) > 0 {
		if uerr := json.Unmarshal(raw, result.Interface(), jsonOptions); uerr != nil {
			err = fmt.Errorf("mygo: %s: cannot decode the result: %w", fn.name, uerr)
			result = reflect.New(fn.result)
		}
	}
	if err != nil {
		errv = reflect.ValueOf(&err).Elem()
	}
	return []reflect.Value{result.Elem(), errv}
}

func (fn *pageFunc) invoke(ctx context.Context, w *Window, in []reflect.Value) (jsontext.Value, error) {
	if w == nil {
		return nil, fmt.Errorf("mygo: %s: no window", fn.name)
	}
	args, err := fn.encodeArgs(in)
	if err != nil {
		return nil, err
	}
	return w.invoke(ctx, fn.name, args)
}

// encodeArgs encodes the arguments of a call as a JSON array.
func (fn *pageFunc) encodeArgs(in []reflect.Value) ([]byte, error) {
	b := []byte{'['}
	n := 0
	add := func(v reflect.Value) error {
		if n > 0 {
			b = append(b, ',')
		}
		n++
		p, err := json.Marshal(v.Interface(), jsonOptions)
		if err != nil {
			return fmt.Errorf("mygo: %s: cannot encode argument %d: %w", fn.name, n, err)
		}
		b = append(b, p...)
		return nil
	}
	for i, v := range in {
		if fn.variadic && i == len(in)-1 {
			for j := range v.Len() {
				if err := add(v.Index(j)); err != nil {
					return nil, err
				}
			}
			break
		}
		if err := add(v); err != nil {
			return nil, err
		}
	}
	return append(b, ']'), nil
}

// invocation is a call of a page function waiting for the page's answer.
type invocation struct {
	id   int64
	name string // e.g. "Editor.text"
	args []byte // a JSON array
	// sent reports whether it was sent to a page, the one whose token is
	// token. Guarded by the window's outMu.
	sent  bool
	token string
	done  atomic.Bool
	ch    chan invokeResult // gets one result
}

type invokeResult struct {
	msg string // the page's result message
	err error
}

// finish settles the call with r, unless it is settled already.
func (inv *invocation) finish(r invokeResult) {
	if inv.done.CompareAndSwap(false, true) {
		deliver(inv.ch, r)
	}
}

// message addresses the call to the page with token. Guarded by the
// window's outMu.
func (inv *invocation) message(token string) message {
	inv.sent, inv.token = true, token
	b := make([]byte, 0, 48+len(token)+len(inv.name))
	b = append(b, `{"t":"invoke","id":`...)
	b = strconv.AppendInt(b, inv.id, 10)
	b = append(b, `,"k":`...)
	b, _ = jsontext.AppendQuote(b, token)
	b = append(b, `,"m":`...)
	b, _ = jsontext.AppendQuote(b, inv.name)
	b = append(b, `,"a":`...)
	return message{head: b, value: inv.args, tail: closeBrace}
}

// invoke calls the page function name with args, a JSON array, and
// returns its result.
func (w *Window) invoke(ctx context.Context, name string, args []byte) (jsontext.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inv := &invocation{name: name, args: args, ch: make(chan invokeResult, 1)}
	if err := w.sendInvocation(inv); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() {
		w.outMu.Lock()
		if w.invokes[inv.id] == inv {
			delete(w.invokes, inv.id)
		}
		w.outMu.Unlock()
		inv.finish(invokeResult{err: ctx.Err()})
	})
	defer stop()
	r := await(inv.ch)
	if r.err != nil {
		return nil, r.err
	}
	var out struct {
		OK      bool     `json:"ok"`
		V       rawValue `json:"v"` // shares r.msg
		E       string   `json:"e"`
		Missing bool     `json:"missing"`
	}
	if err := json.Unmarshal(stringBytes(r.msg), &out); err != nil {
		return nil, fmt.Errorf("mygo: %s: malformed result: %w", name, err)
	}
	switch {
	case out.OK:
		return jsontext.Value(out.V), nil
	case out.Missing:
		return nil, fmt.Errorf("%w: %s", ErrNotExposed, name)
	}
	return nil, &PageError{Function: name, Message: out.E}
}

// sendInvocation sends inv to the page, or holds it until the DOM of the
// next page is ready.
func (w *Window) sendInvocation(inv *invocation) error {
	w.outMu.Lock()
	// Closed fails the calls it finds under the lock, after the window is
	// marked destroyed: none is left waiting.
	if w.destroyed.Load() {
		w.outMu.Unlock()
		return errDestroyed
	}
	if w.domReady && !w.pageTrusted {
		w.outMu.Unlock()
		return untrustedPage(inv.name)
	}
	w.invokeSeq++
	inv.id = w.invokeSeq
	if w.invokes == nil {
		w.invokes = map[int64]*invocation{}
	}
	w.invokes[inv.id] = inv
	if !w.domReady {
		w.hold(heldMessage{inv: inv})
		w.outMu.Unlock()
		return nil
	}
	schedule := w.queue(inv.message(w.pageToken))
	w.outMu.Unlock()
	if schedule {
		postMain(w.flush)
	}
	return nil
}

func untrustedPage(name string) error {
	return fmt.Errorf("mygo: %s: the page is not trusted (see WindowOptions.TrustedOrigins)", name)
}

// takeInvocations removes and returns the calls waiting for the page: all
// of them, or only those sent to it. Guarded by outMu.
func (w *Window) takeInvocations(sentOnly bool) []*invocation {
	var out []*invocation
	for id, inv := range w.invokes {
		if inv.sent || !sentOnly {
			delete(w.invokes, id)
			out = append(out, inv)
		}
	}
	return out
}

// pageAnswered hands a result message of the page to the call it answers.
// Main thread only.
func (w *Window) pageAnswered(msg string) {
	id, token, ok := resultHead(msg)
	if !ok {
		return
	}
	w.outMu.Lock()
	inv := w.invokes[id]
	if inv == nil || !inv.sent || inv.token != token {
		w.outMu.Unlock()
		return
	}
	delete(w.invokes, id)
	w.outMu.Unlock()
	// The caller decodes it: a result can be large.
	inv.finish(invokeResult{msg: msg})
}

// resultHead reads the id and the page's token at the start of a result
// message, which the bridge writes first: {"t":"result","id":N,"k":"…",
// Tokens are base 36.
func resultHead(msg string) (id int64, token string, ok bool) {
	rest, ok := strings.CutPrefix(msg, `{"t":"result","id":`)
	if !ok {
		return 0, "", false
	}
	i := strings.IndexByte(rest, ',')
	if i < 0 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(rest[:i], 10, 64)
	if err != nil {
		return 0, "", false
	}
	rest, ok = strings.CutPrefix(rest[i:], `,"k":"`)
	if !ok {
		return 0, "", false
	}
	if token, _, ok = strings.Cut(rest, `"`); !ok {
		return 0, "", false
	}
	return id, token, true
}
