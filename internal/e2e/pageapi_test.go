package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// E2EPage is what the page of TestPageAPI does for Go.
type E2EPage struct {
	Echo    func(ctx context.Context, s string, n int) (string, error)
	Later   func(ctx context.Context, ms int) (int, error)
	Big     func(ctx context.Context, size int) (string, error)
	Fail    func(ctx context.Context) error
	Hang    func(ctx context.Context) error
	Missing func(ctx context.Context) error
}

var e2ePage = mygo.NewPageAPI[E2EPage]()

const exposingPage = `<!doctype html><html><body><script>
mygo.expose("E2EPage", {
  echo: (s, n) => s + ":" + n + ":" + document.readyState,
  later: (ms) => new Promise((resolve) => setTimeout(() => resolve(ms), ms)),
  big: (size) => "x".repeat(size),
  fail() { throw new Error("boom") },
  hang: () => new Promise(() => {}),
});
</script></body></html>`

func TestPageAPI(t *testing.T) {
	w := newWindow(t, mygo.WindowOptions{Hidden: true})
	p := e2ePage.In(w)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A call made before the page loads waits for its DOM.
	early := make(chan string, 1)
	go func() {
		v, err := p.Echo(ctx, "early", 1)
		if err != nil {
			v = err.Error()
		}
		early <- v
	}()
	time.Sleep(100 * time.Millisecond)
	w.LoadHTML(exposingPage, "")
	if v := <-early; !strings.HasPrefix(v, "early:1:") {
		t.Errorf("Echo before the page loaded = %q", v)
	}

	if v, err := p.Later(ctx, 50); v != 50 || err != nil {
		t.Errorf("Later = %d, %v", v, err)
	}
	start := time.Now()
	if v, err := p.Big(ctx, 8<<20); len(v) != 8<<20 || err != nil {
		t.Errorf("Big = %d bytes, %v", len(v), err)
	}
	t.Logf("8 MiB result in %v", time.Since(start))
	var pageErr *mygo.PageError
	if err := p.Fail(ctx); !errors.As(err, &pageErr) || pageErr.Message != "boom" {
		t.Errorf("Fail = %v", err)
	}
	if err := p.Missing(ctx); !errors.Is(err, mygo.ErrNotExposed) {
		t.Errorf("Missing = %v", err)
	}

	// On the main thread, the call keeps the loop running while it waits.
	var v string
	var err error
	mygo.RunOnMain(func() { v, err = p.Echo(ctx, "main", 2) })
	if !strings.HasPrefix(v, "main:2:") || err != nil {
		t.Errorf("Echo on the main thread = %q, %v", v, err)
	}

	// A navigation fails the calls the page did not answer.
	hung := make(chan error, 1)
	go func() { hung <- p.Hang(ctx) }()
	time.Sleep(100 * time.Millisecond)
	w.LoadHTML("<p>another page</p>", "")
	select {
	case err := <-hung:
		if err == nil || !strings.Contains(err.Error(), "navigated away") {
			t.Errorf("Hang after a navigation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pending call did not fail after the navigation")
	}
	// The new page exposes nothing.
	if err := p.Fail(ctx); !errors.Is(err, mygo.ErrNotExposed) {
		t.Errorf("Fail in a page that exposes nothing = %v", err)
	}
}
