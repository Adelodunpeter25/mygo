//go:build darwin

package darwin

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// testNotifier records what a notifier asks of the system.
type testNotifier struct {
	notifier
	added           []string
	checks, prompts int
}

func newTestNotifier() *testNotifier {
	t := &testNotifier{}
	t.notifier = notifier{
		status: notifyUnknown,
		add:    func(n *platform.Notification) { t.added = append(t.added, n.ID) },
		check:  func() { t.checks++ },
		ask:    func() { t.prompts++ },
	}
	return t
}

func (t *testNotifier) mustShow(tb testing.TB, ids ...string) {
	tb.Helper()
	for _, id := range ids {
		if err := t.show(&platform.Notification{ID: id}); err != nil {
			tb.Fatalf("show %s: %v", id, err)
		}
	}
}

func TestNotifierWaitsForSettings(t *testing.T) {
	n := newTestNotifier()
	n.refresh() // at launch
	n.mustShow(t, "a", "b")
	if n.checks != 1 || len(n.added) != 0 {
		t.Fatalf("before the settings: %d checks, added %v", n.checks, n.added)
	}
	n.checked(3) // provisional
	if !slices.Equal(n.added, []string{"a", "b"}) {
		t.Fatalf("added %v", n.added)
	}
	n.mustShow(t, "c")
	if !slices.Equal(n.added, []string{"a", "b", "c"}) || n.prompts != 0 {
		t.Fatalf("added %v, %d prompts", n.added, n.prompts)
	}
}

func TestNotifierDenied(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	n.mustShow(t, "a")
	n.checked(notifyDenied)
	if len(n.added) != 0 || len(n.pending) != 0 {
		t.Fatalf("added %v, pending %d", n.added, len(n.pending))
	}
	if err := n.show(&platform.Notification{ID: "b"}); !errors.Is(err, platform.ErrNotificationsDenied) {
		t.Fatalf("show = %v", err)
	}
	// The user allows the app in System Settings, then comes back to it.
	n.refresh()
	n.checked(notifyAuthorized)
	n.mustShow(t, "c")
	if !slices.Equal(n.added, []string{"c"}) {
		t.Fatalf("added %v", n.added)
	}
}

func TestNotifierPrompts(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	n.checked(notifyNotDetermined)
	if n.prompts != 0 {
		t.Fatal("prompted before the first notification")
	}
	n.mustShow(t, "a", "b")
	n.refresh() // the app becomes active while the prompt is up
	if n.prompts != 1 || n.checks != 1 {
		t.Fatalf("%d prompts, %d checks", n.prompts, n.checks)
	}
	n.answered(true, nil)
	if !slices.Equal(n.added, []string{"a", "b"}) {
		t.Fatalf("added %v", n.added)
	}
}

func TestNotifierPromptRefused(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	n.mustShow(t, "a")
	n.checked(notifyNotDetermined)
	if n.prompts != 1 {
		t.Fatalf("%d prompts", n.prompts)
	}
	cause := errors.New("Notifications are not allowed for this application")
	n.answered(false, cause)
	err := n.show(&platform.Notification{ID: "b"})
	if !errors.Is(err, platform.ErrNotificationsDenied) || !errors.Is(err, cause) {
		t.Fatalf("show = %v", err)
	}
	if len(n.added) != 0 {
		t.Fatalf("added %v", n.added)
	}
}

func TestNotifierCheckedWhilePrompting(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	n.checked(notifyNotDetermined)
	n.checking = true // a check went out before the prompt
	n.mustShow(t, "a")
	n.checked(notifyNotDetermined)
	if n.prompts != 1 || len(n.pending) != 1 {
		t.Fatalf("%d prompts, pending %d", n.prompts, len(n.pending))
	}
	n.answered(true, nil)
	if !slices.Equal(n.added, []string{"a"}) {
		t.Fatalf("added %v", n.added)
	}
}

func TestNotifierRemoveWaiting(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	n.mustShow(t, "a", "b", "a")
	n.remove("a")
	n.checked(notifyAuthorized)
	if !slices.Equal(n.added, []string{"b"}) {
		t.Fatalf("added %v", n.added)
	}
}

func TestNotifierKeepsLatest(t *testing.T) {
	n := newTestNotifier()
	n.refresh()
	var ids []string
	for i := range maxPendingNotifications + 3 {
		ids = append(ids, fmt.Sprint(i))
	}
	n.mustShow(t, ids...)
	n.checked(notifyAuthorized)
	if want := ids[3:]; !slices.Equal(n.added, want) {
		t.Fatalf("added %v, want %v", n.added, want)
	}
}
