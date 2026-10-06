//go:build darwin

package darwin

import (
	"fmt"
	"slices"
	"sync"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// Notifications use UNUserNotificationCenter, which only works for apps
// running from a bundle with an identifier.

var (
	mainQueueMu sync.Mutex
	mainQueue   []func()
)

// runOnMain runs fn on the main thread; used by callbacks the system
// delivers on background queues.
func (b *Backend) runOnMain(fn func()) {
	if b.IsMainThread() {
		fn()
		return
	}
	b.post(fn)
}

// post runs fn on the main thread after the current event, even when
// called there.
func (b *Backend) post(fn func()) {
	mainQueueMu.Lock()
	mainQueue = append(mainQueue, fn)
	mainQueueMu.Unlock()
	b.Signal()
}

func drainMainQueue() {
	mainQueueMu.Lock()
	q := mainQueue
	mainQueue = nil
	mainQueueMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func registerNotificationDelegate() {
	if !hasClass("UNUserNotificationCenter") {
		return
	}
	classDef("MyGoNotificationDelegate", "NSObject", []string{"UNUserNotificationCenterDelegate"}, []objc.MethodDef{
		method("userNotificationCenter:willPresentNotification:withCompletionHandler:", func(self id, _ objc.SEL, center, n id, handler uintptr) {
			// Show banners even while the app is in the foreground.
			callBlock(handler, 1<<1|1<<3|1<<4) // sound | list | banner
		}),
		method("userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:", func(self id, _ objc.SEL, center, resp id, handler uintptr) {
			ident := goString(send(send(send(send(resp, "notification"), "request"), "identifier"), "self"))
			theBackend.runOnMain(func() { theBackend.h.NotificationClicked(ident) })
			callBlock(handler)
		}),
	})
}

func (b *Backend) NotificationsSupported() bool {
	_, packaged := appController{b}.Package()
	return packaged && hasClass("UNUserNotificationCenter")
}

// notificationCenter returns the app's notification center. It throws
// when the app does not run from a bundle: check NotificationsSupported
// first.
func notificationCenter() id {
	return send(class("UNUserNotificationCenter"), "currentNotificationCenter")
}

// setUpNotifications attaches the notification center's delegate and asks
// for the app's notification settings, which does not prompt the user, so
// that the first notification knows whether it can be shown. The delegate
// has to be there before the app finishes launching, as the system then
// delivers the response to a notification the user clicked while the app
// was not running.
func (b *Backend) setUpNotifications() {
	b.notify = notifier{
		status: notifyUnknown,
		add:    b.addNotification,
		check:  b.checkNotificationSettings,
		ask:    b.askNotificationAuthorization,
	}
	if !b.NotificationsSupported() {
		return
	}
	b.notifyDelegate = alloc("MyGoNotificationDelegate")
	withPool(func() {
		send(notificationCenter(), "setDelegate:", uintptr(b.notifyDelegate))
	})
	b.notify.refresh()
}

// notificationsMayHaveChanged asks for the settings again when the app
// becomes active, as the user may come back from System Settings having
// allowed or turned off its notifications.
func (b *Backend) notificationsMayHaveChanged() {
	if b.notifyDelegate != 0 {
		b.notify.refresh()
	}
}

// checkNotificationSettings asks for the app's authorization status, which
// comes back on a queue of the system's.
func (b *Backend) checkNotificationSettings() {
	withPool(func() {
		blk := newBlock(func(_ objc.Block, settings id) {
			// The settings are the caller's only while the block runs.
			status := sendInt(settings, "authorizationStatus")
			b.runOnMain(func() { b.notify.checked(status) })
		})
		send(notificationCenter(), "getNotificationSettingsWithCompletionHandler:", uintptr(blk))
		blk.Release()
	})
}

// notifyOptions are the permissions asked for: badge, sound and alert.
const notifyOptions = 1<<0 | 1<<1 | 1<<2

// askNotificationAuthorization prompts the user, whose answer comes back
// on a queue of the system's.
func (b *Backend) askNotificationAuthorization() {
	withPool(func() {
		blk := newBlock(func(_ objc.Block, granted bool, nsErr id) {
			// The error is the caller's only while the block runs.
			var err error
			withPool(func() { err = nsError(nsErr) })
			b.runOnMain(func() { b.notify.answered(granted, err) })
		})
		send(notificationCenter(), "requestAuthorizationWithOptions:completionHandler:",
			notifyOptions, uintptr(blk))
		blk.Release()
	})
}

// addNotification gives the system a request to show a notification. Its
// trigger is nil, which shows it at once.
func (b *Backend) addNotification(n *platform.Notification) {
	withPool(func() {
		content := autorelease(alloc("UNMutableNotificationContent"))
		send(content, "setTitle:", uintptr(nsString(n.Title)))
		if n.Subtitle != "" {
			send(content, "setSubtitle:", uintptr(nsString(n.Subtitle)))
		}
		send(content, "setBody:", uintptr(nsString(n.Body)))
		if !n.Silent {
			send(content, "setSound:", uintptr(send(class("UNNotificationSound"), "defaultSound")))
		}
		req := send(class("UNNotificationRequest"), "requestWithIdentifier:content:trigger:",
			uintptr(nsString(n.ID)), uintptr(content), 0)
		send(notificationCenter(), "addNotificationRequest:withCompletionHandler:", uintptr(req), 0)
	})
}

func (b *Backend) ShowNotification(n *platform.Notification) error {
	if !b.NotificationsSupported() {
		return platform.ErrUnsupported
	}
	return b.notify.show(n)
}

func (b *Backend) RemoveNotification(ident string) {
	if !b.NotificationsSupported() {
		return
	}
	b.notify.remove(ident)
	withPool(func() {
		center := notificationCenter()
		ids := nsArray(nsString(ident))
		send(center, "removeDeliveredNotificationsWithIdentifiers:", uintptr(ids))
		send(center, "removePendingNotificationRequestsWithIdentifiers:", uintptr(ids))
	})
}

// The authorization statuses of UNAuthorizationStatus that matter here.
// Provisional (3) and ephemeral (4) authorizations count as authorized.
const (
	notifyUnknown       = -1 // not known yet
	notifyNotDetermined = 0  // the user has not been asked
	notifyDenied        = 1
	notifyAuthorized    = 2
)

// maxPendingNotifications is how many notifications wait for the user's
// answer, the latest ones: the prompt can sit in Notification Center for
// hours, and what was shown before is stale by the time the user allows.
const maxPendingNotifications = 5

// notifier keeps what the app knows of its permission to show
// notifications, and the notifications that wait for it. Main thread only.
// macOS answers on a queue of its own, and drops a notification it is given
// before the user has allowed it: one shown before the answer waits here.
// The native side is a set of functions, which tests replace.
type notifier struct {
	status   int   // a UNAuthorizationStatus, or notifyUnknown
	checking bool  // the settings were asked for
	asking   bool  // the user was prompted
	err      error // what prompting the user failed with
	pending  []*platform.Notification

	add   func(*platform.Notification) // gives the system a notification
	check func()                       // asks for the settings, answered by checked
	ask   func()                       // prompts the user, answered by answered
}

// show shows n, or keeps it until the answer comes. Once the user has not
// allowed notifications it returns ErrNotificationsDenied.
func (s *notifier) show(n *platform.Notification) error {
	switch s.status {
	case notifyAuthorized:
		s.add(n)
		return nil
	case notifyDenied:
		if s.err != nil {
			return fmt.Errorf("%w: %w", platform.ErrNotificationsDenied, s.err)
		}
		return platform.ErrNotificationsDenied
	}
	s.pending = append(s.pending, n)
	if len(s.pending) > maxPendingNotifications {
		s.pending = slices.Delete(s.pending, 0, 1)
	}
	if s.status == notifyNotDetermined {
		s.prompt()
	} else {
		s.refresh()
	}
	return nil
}

// remove forgets the waiting copies of a notification.
func (s *notifier) remove(ident string) {
	s.pending = slices.DeleteFunc(s.pending, func(n *platform.Notification) bool { return n.ID == ident })
}

// refresh asks for the settings, unless the answer to a question is still
// to come.
func (s *notifier) refresh() {
	if !s.checking && !s.asking {
		s.checking = true
		s.check()
	}
}

func (s *notifier) prompt() {
	if !s.asking {
		s.asking = true
		s.ask()
	}
}

// checked receives the authorization status from the settings.
func (s *notifier) checked(status int) {
	s.checking = false
	if s.asking {
		// The user's answer settles it.
		return
	}
	if status > notifyAuthorized {
		status = notifyAuthorized
	}
	if status != s.status {
		s.err = nil
	}
	s.status = status
	if status == notifyNotDetermined {
		if len(s.pending) > 0 {
			s.prompt()
		}
		return
	}
	s.flush()
}

// answered receives the user's answer to the prompt.
func (s *notifier) answered(granted bool, err error) {
	s.asking = false
	s.status, s.err = notifyDenied, err
	if granted {
		s.status, s.err = notifyAuthorized, nil
	}
	s.flush()
}

// flush gives the system the waiting notifications once the app may show
// them, and gives them up otherwise.
func (s *notifier) flush() {
	pending := s.pending
	s.pending = nil
	if s.status == notifyAuthorized {
		for _, n := range pending {
			s.add(n)
		}
	}
}
