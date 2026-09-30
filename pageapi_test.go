package mygo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/fake"
)

// testEditor is what the pages of the tests do for Go.
type testEditor struct {
	// Text returns the text being edited.
	Text func(ctx context.Context) (string, error)
	Open func(name string, size int) error
	Sum  func(ctx context.Context, nums ...int) (int, error)
	User func() (*testUser, error)
	// Unexported fields are not functions of the page.
	notes []string
}

// pageReady simulates the bridge of fw reporting that its DOM is ready,
// with the page's token.
func pageReady(fw *fake.Window, token string) {
	page(fw, `{"t":"dom-ready","k":"`+token+`"}`)
}

// invoked waits until the page of fw received invocation id.
func invoked(t *testing.T, fw *fake.Window, id int) map[string]any {
	t.Helper()
	return received(t, fw, func(m map[string]any) bool { return m["t"] == "invoke" && m["id"] == float64(id) })
}

// answer simulates the page answering invocation id.
func answer(fw *fake.Window, id int, token, fields string) {
	page(fw, fmt.Sprintf(`{"t":"result","id":%d,"k":%q,%s}`, id, token, fields))
}

type outcome[T any] struct {
	v   T
	err error
}

// start runs fn on a goroutine of its own.
func start[T any](fn func() (T, error)) chan outcome[T] {
	ch := make(chan outcome[T], 1)
	go func() {
		v, err := fn()
		ch <- outcome[T]{v, err}
	}()
	return ch
}

// result waits for what fn of start returned.
func result[T any](t *testing.T, ch chan outcome[T]) (T, error) {
	t.Helper()
	select {
	case o := <-ch:
		return o.v, o.err
	case <-time.After(3 * time.Second):
		t.Fatal("the call did not return")
	}
	panic("unreachable")
}

// pending reports that the call of start has not returned.
func pending[T any](t *testing.T, ch chan outcome[T]) {
	t.Helper()
	select {
	case o := <-ch:
		t.Fatalf("the call returned %v, %v", o.v, o.err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPageAPI(t *testing.T) {
	editor := newPageAPIForTest[testEditor](t)
	w, fw := testWindow(t, WindowOptions{})
	pageReady(fw, "tok")
	ed := editor.In(w)
	ctx := context.Background()

	text := start(func() (string, error) { return ed.Text(ctx) })
	if m := invoked(t, fw, 1); m["m"] != "TestEditor.text" || m["k"] != "tok" || fmt.Sprint(m["a"]) != "[]" {
		t.Errorf("invocation = %v", m)
	}
	answer(fw, 1, "tok", `"ok":true,"v":"hello"`)
	if v, err := result(t, text); v != "hello" || err != nil {
		t.Errorf("Text = %q, %v", v, err)
	}

	// Variadic arguments are spread.
	sum := start(func() (int, error) { return ed.Sum(ctx, 1, 2, 3) })
	if m := invoked(t, fw, 2); fmt.Sprint(m["a"]) != "[1 2 3]" {
		t.Errorf("Sum arguments = %v", m["a"])
	}
	answer(fw, 2, "tok", `"ok":true,"v":6`)
	if v, err := result(t, sum); v != 6 || err != nil {
		t.Errorf("Sum = %d, %v", v, err)
	}

	open := start(func() (bool, error) { return true, ed.Open("a.txt", 3) })
	if m := invoked(t, fw, 3); fmt.Sprint(m["a"]) != "[a.txt 3]" {
		t.Errorf("Open arguments = %v", m["a"])
	}
	answer(fw, 3, "tok", `"ok":true`)
	if _, err := result(t, open); err != nil {
		t.Errorf("Open = %v", err)
	}

	user := start(func() (*testUser, error) { return ed.User() })
	invoked(t, fw, 4)
	answer(fw, 4, "tok", `"ok":true,"v":{"name":"ada","tags":["x"]}`)
	if u, err := result(t, user); err != nil || u == nil || u.Name != "ada" || len(u.Tags) != 1 {
		t.Errorf("User = %+v, %v", u, err)
	}
	// No value (undefined) is the zero value.
	user = start(func() (*testUser, error) { return ed.User() })
	invoked(t, fw, 5)
	answer(fw, 5, "tok", `"ok":true`)
	if u, err := result(t, user); u != nil || err != nil {
		t.Errorf("User without a value = %+v, %v", u, err)
	}
}

func TestPageAPIErrors(t *testing.T) {
	editor := newPageAPIForTest[testEditor](t)
	w, fw := testWindow(t, WindowOptions{})
	pageReady(fw, "tok")
	ed := editor.In(w)
	ctx := context.Background()

	text := start(func() (string, error) { return ed.Text(ctx) })
	invoked(t, fw, 1)
	answer(fw, 1, "tok", `"ok":false,"missing":true`)
	if _, err := result(t, text); !errors.Is(err, ErrNotExposed) || !strings.Contains(err.Error(), "TestEditor.text") {
		t.Errorf("Text not exposed = %v", err)
	}

	text = start(func() (string, error) { return ed.Text(ctx) })
	invoked(t, fw, 2)
	answer(fw, 2, "tok", `"ok":false,"e":"boom"`)
	var pageErr *PageError
	if _, err := result(t, text); !errors.As(err, &pageErr) || pageErr.Function != "TestEditor.text" || pageErr.Message != "boom" {
		t.Errorf("Text throwing = %v", err)
	} else if err.Error() != "mygo: TestEditor.text: boom" {
		t.Errorf("PageError = %q", err)
	}

	// A value of the wrong type.
	sum := start(func() (int, error) { return ed.Sum(ctx) })
	invoked(t, fw, 3)
	answer(fw, 3, "tok", `"ok":true,"v":"six"`)
	if v, err := result(t, sum); v != 0 || err == nil || !strings.Contains(err.Error(), "cannot decode the result") {
		t.Errorf("Sum with a string = %v, %v", v, err)
	}

	// Answers from another page, or for no call, are ignored.
	text = start(func() (string, error) { return ed.Text(ctx) })
	invoked(t, fw, 4)
	answer(fw, 4, "stale", `"ok":true,"v":"wrong"`)
	answer(fw, 99, "tok", `"ok":true,"v":"wrong"`)
	page(fw, `{"t":"result","id":`)
	pending(t, text)
	answer(fw, 4, "tok", `"ok":true,"v":"right"`)
	if v, err := result(t, text); v != "right" || err != nil {
		t.Errorf("Text = %q, %v", v, err)
	}

	// The context stops the wait; the late answer changes nothing.
	cctx, cancel := context.WithCancel(ctx)
	text = start(func() (string, error) { return ed.Text(cctx) })
	invoked(t, fw, 5)
	cancel()
	if _, err := result(t, text); !errors.Is(err, context.Canceled) {
		t.Errorf("Text canceled = %v", err)
	}
	answer(fw, 5, "tok", `"ok":true,"v":"late"`)
	if _, err := ed.Text(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Text with a canceled context = %v", err)
	}
	var none *Window
	if err := editor.In(none).Open("x", 1); err == nil || !strings.Contains(err.Error(), "no window") {
		t.Errorf("Open without a window = %v", err)
	}

	// Closing the window fails the calls waiting for it, and later ones.
	text = start(func() (string, error) { return ed.Text(ctx) })
	invoked(t, fw, 6)
	w.Destroy()
	if _, err := result(t, text); !errors.Is(err, errDestroyed) {
		t.Errorf("Text in a closed window = %v", err)
	}
	if _, err := ed.Text(ctx); !errors.Is(err, errDestroyed) {
		t.Errorf("Text after closing = %v", err)
	}
}

// TestPageAPIWaitsForTheDOM: calls made before the page is ready, or during
// a navigation, reach the next page, in order with events, and those sent
// to a page that goes away fail.
func TestPageAPIWaitsForTheDOM(t *testing.T) {
	editor := newPageAPIForTest[testEditor](t)
	ev := newEventForTest[string](t, "test:order")
	w, fw := testWindow(t, WindowOptions{})
	ed := editor.In(w)
	ctx := context.Background()

	_ = ev.Emit(w, "before")
	text := start(func() (string, error) { return ed.Text(ctx) })
	time.Sleep(20 * time.Millisecond)
	_ = ev.Emit(w, "after")
	time.Sleep(20 * time.Millisecond)
	if n := len(pageMessages(t, fw)); n != 0 {
		t.Fatalf("%d messages sent before the page was ready", n)
	}
	pageReady(fw, "first")
	invoked(t, fw, 1)
	var order []string
	for _, m := range pageMessages(t, fw) {
		order = append(order, fmt.Sprint(m["t"], ":", m["k"], m["p"]))
	}
	if got := strings.Join(order, " "); got != "event:<nil>before invoke:first<nil> event:<nil>after" {
		t.Errorf("messages = %s", got)
	}

	// The page navigates before answering: the call fails.
	onMain(func() { fw.H.NavigationCommitted("about:blank") })
	if _, err := result(t, text); err == nil || !strings.Contains(err.Error(), "navigated away") {
		t.Errorf("Text after a navigation = %v", err)
	}
	// A call during the navigation waits for the next page.
	text = start(func() (string, error) { return ed.Text(ctx) })
	pending(t, text)
	pageReady(fw, "second")
	if m := invoked(t, fw, 2); m["k"] != "second" {
		t.Errorf("invocation of the next page = %v", m)
	}
	answer(fw, 2, "first", `"ok":true,"v":"from the old page"`)
	answer(fw, 2, "second", `"ok":true,"v":"new"`)
	if v, err := result(t, text); v != "new" || err != nil {
		t.Errorf("Text = %q, %v", v, err)
	}

	// A call that was canceled while it waited is never sent.
	onMain(func() { fw.H.NavigationCommitted("about:blank") })
	cctx, cancel := context.WithCancel(ctx)
	text = start(func() (string, error) { return ed.Text(cctx) })
	time.Sleep(20 * time.Millisecond)
	cancel()
	if _, err := result(t, text); !errors.Is(err, context.Canceled) {
		t.Errorf("Text canceled = %v", err)
	}
	// Many events do not push calls out.
	sum := start(func() (int, error) { return ed.Sum(ctx, 1) })
	time.Sleep(20 * time.Millisecond)
	for i := range maxHeldEvents + 10 {
		_ = ev.Emit(w, fmt.Sprint(i))
	}
	n := len(fw.Scripts())
	pageReady(fw, "third")
	received(t, fw, func(m map[string]any) bool { return m["t"] == "invoke" && m["k"] == "third" })
	for _, s := range fw.Scripts()[n:] {
		if strings.Contains(s, `"id":3,`) {
			t.Error("the canceled call was sent")
		}
	}
	answer(fw, 4, "third", `"ok":true,"v":1`)
	if v, err := result(t, sum); v != 1 || err != nil {
		t.Errorf("Sum = %d, %v", v, err)
	}
}

func TestPageAPIOnlyCallsTrustedPages(t *testing.T) {
	editor := newPageAPIForTest[testEditor](t)
	w, fw := testWindow(t, WindowOptions{})
	ed := editor.In(w)
	onMain(func() { fw.H.NavigationCommitted("https://evil.example/") })
	// Held until the page is ready, then refused.
	text := start(func() (string, error) { return ed.Text(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	pageReady(fw, "tok")
	if _, err := result(t, text); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("Text in an untrusted page = %v", err)
	}
	if _, err := ed.Text(context.Background()); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("Text in a ready untrusted page = %v", err)
	}
	for _, m := range pageMessages(t, fw) {
		if m["t"] == "invoke" {
			t.Errorf("sent to an untrusted page: %v", m)
		}
	}
}

// TestPageAPIOnTheMainThread: an event listener may call the page and
// wait, like Eval.
func TestPageAPIOnTheMainThread(t *testing.T) {
	editor := newPageAPIForTest[testEditor](t)
	w, fw := testWindow(t, WindowOptions{})
	pageReady(fw, "tok")
	fw.OnEval = func(js string) {
		if strings.Contains(js, `"t":"invoke"`) {
			// The page answers later, as the main thread waits.
			go answer(fw, 1, "tok", `"ok":true,"v":"from the page"`)
		}
	}
	text := start(func() (string, error) {
		var v string
		var err error
		onMain(func() { v, err = editor.In(w).Text(context.Background()) })
		return v, err
	})
	if v, err := result(t, text); v != "from the page" || err != nil {
		t.Errorf("Text on the main thread = %q, %v", v, err)
	}
}

type (
	emptyPage     struct{ notes []string }
	valuePage     struct{ Title string }
	noErrorPage   struct{ Text func() string }
	threeResults  struct{ Text func() (string, int, error) }
	channelPage   struct{ Watch func(ch *Channel[int]) error }
	funcParamPage struct{ Run func(fn func()) error }
	embeddedPage  struct{ testEditor }
	collidingPage struct {
		URL func() error
		Url func() error
	}
	pagePair[A, B any] struct {
		First func() (A, error)
		Last  func() (B, error)
	}
)

func TestPageAPIDeclarations(t *testing.T) {
	mustPanic := func(what, want string, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), want) {
				t.Errorf("%s: panic %v, want %q", what, r, want)
			}
		}()
		fn()
	}
	mustPanic("not a struct", "must be a struct", func() { NewPageAPI[func() error]() })
	mustPanic("anonymous", "must have a name", func() { NewPageAPI[struct{ F func() error }]() })
	mustPanic("empty", "has no functions", func() { NewPageAPI[emptyPage]() })
	mustPanic("value", "ValuePage.Title is not a function", func() { NewPageAPI[valuePage]() })
	mustPanic("no error", "must return an error", func() { NewPageAPI[noErrorPage]() })
	mustPanic("three results", "must return an error", func() { NewPageAPI[threeResults]() })
	mustPanic("channel", "channels are parameters of bound methods only", func() { NewPageAPI[channelPage]() })
	mustPanic("func parameter", "cannot be encoded as JSON", func() { NewPageAPI[funcParamPage]() })
	mustPanic("embedded", "embedded fields", func() { NewPageAPI[embeddedPage]() })
	mustPanic("collision", "are both CollidingPage.url", func() { NewPageAPI[collidingPage]() })
	newPageAPIForTest[testEditor](t)
	mustPanic("duplicate", "already declared", func() { NewPageAPI[testEditor]() })
	mustPanic("zero PageAPI", "NewPageAPI", func() { new(PageAPI[testEditor]).In(nil) })

	// Generic types are named like their TypeScript interface.
	pair := newPageAPIForTest[pagePair[string, int]](t)
	if pair.api.name != "PagePairStringInt" || pair.api.funcs[1].name != "PagePairStringInt.last" {
		t.Errorf("generic page API named %q, %q", pair.api.name, pair.api.funcs[1].name)
	}
}

// TestPageAPIInTypeScript: page APIs reach the generator (which tests the
// output with sources, which it does not read from test files).
func TestPageAPIInTypeScript(t *testing.T) {
	newPageAPIForTest[testEditor](t)
	src, err := GenerateTypeScript()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"export interface TestEditor {",
		"  text(): string | Promise<string>;",
		"  open(arg0: string, arg1: number): void | Promise<void>;",
		"  sum(...arg0: number[]): number | Promise<number>;",
		"  user(): TestUser | null | Promise<TestUser | null>;",
		"export function exposeTestEditor(functions: Partial<TestEditor>): () => void {\n  return expose(\"TestEditor\", functions);\n}",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated code is missing:\n%s\n\nin:\n%s", want, src)
		}
	}
	if strings.Contains(string(src), "notes") {
		t.Error("unexported fields are not functions of the page")
	}
}

func TestResultHead(t *testing.T) {
	for msg, want := range map[string]string{
		`{"t":"result","id":12,"k":"abc","ok":true}`: "12 abc true",
		`{"t":"result","id":12,"k":"","ok":true}`:    "12  true",
		`{"t":"result","id":x,"k":"abc"}`:            "0  false",
		`{"t":"result","id":12}`:                     "0  false",
		`{"t":"result","id":12,"k":"abc`:             "0  false",
		`{"t":"reply","id":12,"k":"abc"}`:            "0  false",
	} {
		id, token, ok := resultHead(msg)
		if got := fmt.Sprint(id, " ", token, " ", ok); got != want {
			t.Errorf("resultHead(%s) = %s, want %s", msg, got, want)
		}
	}
}
