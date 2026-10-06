package mygo

import "testing"

// showNotification shows a notification for a test. The fake backend
// records it by its id, which is how a test sees what the platform would
// still be showing.
func showNotification(t *testing.T, opts NotificationOptions) *Notification {
	t.Helper()
	n := NewNotification(opts)
	if err := n.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	t.Cleanup(n.Close)
	return n
}

// clickNotification reports a click on a notification as a backend does,
// on the main thread, where the platform's delegate reports it.
func clickNotification(t *testing.T, n *Notification) {
	t.Helper()
	onMain(func() { fb.ClickNotification(n.id) })
}

// TestNotificationDismissOnClick checks that a notification asked to
// dismiss itself is removed once the user has clicked it, and that one
// that was not stays where macOS leaves it, in the notification centre.
func TestNotificationDismissOnClick(t *testing.T) {
	stay := showNotification(t, NotificationOptions{Title: "stay"})
	dismiss := showNotification(t, NotificationOptions{Title: "dismiss", DismissOnClick: true})

	var clicked []string
	stay.OnClick(func() { clicked = append(clicked, "stay") })
	dismiss.OnClick(func() { clicked = append(clicked, "dismiss") })

	clickNotification(t, dismiss)
	if fb.Notification(dismiss.id) != nil {
		t.Error("a notification with DismissOnClick is still there after a click")
	}
	if fb.Notification(stay.id) == nil {
		t.Error("a notification without DismissOnClick was removed by a click")
	}

	// Its own listener still runs, whether or not it dismisses itself.
	clickNotification(t, stay)
	if len(clicked) != 2 || clicked[0] != "dismiss" || clicked[1] != "stay" {
		t.Errorf("clicked = %q, want [dismiss stay]", clicked)
	}
	if fb.Notification(stay.id) == nil {
		t.Error("a click removed a notification that did not ask to be")
	}

	// Clicking one that was removed already reaches nobody.
	clickNotification(t, dismiss)
	if len(clicked) != 2 {
		t.Errorf("a removed notification's listener ran again: %q", clicked)
	}
}

// TestNotificationDismissOnClickListener checks that a listener which
// shows another notification, as answering a message may, does not have
// that one removed along with the notification it came from.
func TestNotificationDismissOnClickListener(t *testing.T) {
	first := showNotification(t, NotificationOptions{Title: "first", DismissOnClick: true})
	var second *Notification
	first.OnClick(func() {
		second = NewNotification(NotificationOptions{Title: "second"})
		if err := second.Show(); err != nil {
			t.Errorf("Show: %v", err)
		}
	})
	clickNotification(t, first)
	if second == nil {
		t.Fatal("the listener did not run")
	}
	t.Cleanup(second.Close)
	if fb.Notification(second.id) == nil {
		t.Error("the notification the listener showed was removed with the first")
	}
	if fb.Notification(first.id) != nil {
		t.Error("the notification clicked was not removed")
	}
}

// TestClearNotifications checks that every notification the app still has
// shown is removed, whether or not it was asked to dismiss itself, and
// that a click reaches none of them afterwards.
func TestClearNotifications(t *testing.T) {
	one := showNotification(t, NotificationOptions{Title: "one"})
	two := showNotification(t, NotificationOptions{Title: "two"})
	three := showNotification(t, NotificationOptions{Title: "three", DismissOnClick: true})
	// One the app closed itself, which nothing is left to remove.
	closed := showNotification(t, NotificationOptions{Title: "closed"})
	closed.Close()

	clicked := 0
	for _, n := range []*Notification{one, two, three, closed} {
		n.OnClick(func() { clicked++ })
	}

	ClearNotifications()

	for _, n := range []*Notification{one, two, three} {
		if fb.Notification(n.id) != nil {
			t.Errorf("%q was not removed by ClearNotifications", n.opts.Title)
		}
	}
	clickNotification(t, one)
	clickNotification(t, two)
	clickNotification(t, three)
	if clicked != 0 {
		t.Errorf("%d listeners ran after ClearNotifications, want 0", clicked)
	}

	// Clearing again, with nothing shown, is not an error.
	ClearNotifications()
}

// TestNotificationOptionsReachTheBackend checks that Show passes the
// options on, which the backends read for the sound and the core reads
// for the dismissal.
func TestNotificationOptionsReachTheBackend(t *testing.T) {
	n := showNotification(t, NotificationOptions{
		Title: "title", Subtitle: "subtitle", Body: "body",
		Silent: true, DismissOnClick: true,
	})
	got := fb.Notification(n.id)
	if got == nil {
		t.Fatal("the notification did not reach the backend")
	}
	if got.Title != "title" || got.Subtitle != "subtitle" || got.Body != "body" {
		t.Errorf("got %q/%q/%q, want title/subtitle/body", got.Title, got.Subtitle, got.Body)
	}
	if !got.Silent || !got.DismissOnClick {
		t.Errorf("Silent = %v, DismissOnClick = %v, want both true", got.Silent, got.DismissOnClick)
	}
}
