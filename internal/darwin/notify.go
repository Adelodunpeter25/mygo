//go:build darwin

package darwin

import (
	"errors"
	"fmt"
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

// errNotificationsDenied is what ShowNotification returns once macOS has
// answered that the app may not show notifications. The system drops what
// it is given, so asking again says nothing and the app has to be told:
// otherwise a notification the user would not be allowed to see fails as
// though it had been shown.
var errNotificationsDenied = errors.New("mygo: the user does not allow notifications")

// notificationCenter returns the app's notification center.
func notificationCenter() id {
	return send(class("UNUserNotificationCenter"), "currentNotificationCenter")
}

// attachNotificationDelegate gives the notification center the delegate
// the registered classes hold. It is attached when the app launches rather
// than before a notification of its own is shown, because the system
// delivers the response to a notification the user clicked while the app
// was not running at launch: a center without a delegate then drops it,
// and the app comes up with nothing to show.
func attachNotificationDelegate() {
	b := theBackend
	if b == nil || b.notifyDelegate != 0 || !hasClass("UNUserNotificationCenter") {
		return
	}
	b.notifyDelegate = alloc("MyGoNotificationDelegate")
	withPool(func() {
		send(notificationCenter(), "setDelegate:", uintptr(b.notifyDelegate))
	})
}

// notifyOptions are the permissions asked for: badge, sound and alert.
const notifyOptions = 1<<0 | 1<<1 | 1<<2

// askNotificationAuthorization asks the user once, the first time the app
// shows a notification. macOS does not prompt again once the user has
// decided, so this one answer holds for the rest of the app's life, and
// comes back in a completion handler on a queue of its own: the
// notifications asked for before it arrives wait in notifyPending, as
// what the system is given before the user has answered is dropped.
func (b *Backend) askNotificationAuthorization() {
	if b.notifyAsked {
		return
	}
	b.notifyAsked = true
	blk := newBlock(func(_ objc.Block, granted bool, err id) {
		// A block made once, as callbacks are scarce and never freed:
		// asking happens once, so this runs once.
		if b := theBackend; b != nil {
			b.runOnMain(func() {
				withPool(func() {
					b.notifyResolved, b.notifyGranted, b.notifyErr = true, granted, nsError(err)
					b.addPendingNotifications()
				})
			})
		}
	})
	send(notificationCenter(), "requestAuthorizationWithOptions:completionHandler:",
		notifyOptions, uintptr(blk))
	blk.Release()
}

// addPendingNotifications gives the system the notifications the app asked
// for while the answer to the authorization request was still to come.
// They are given up when it says no: what the system is given then is
// dropped anyway, and the app is told by every Show from then on.
func (b *Backend) addPendingNotifications() {
	pending := b.notifyPending
	b.notifyPending = nil
	if !b.notifyGranted {
		return
	}
	for _, n := range pending {
		b.addNotification(n)
	}
}

// addNotification gives the system a request to show a notification. Its
// trigger is nil, which shows it at once.
func (b *Backend) addNotification(n *platform.Notification) {
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
}

func (b *Backend) ShowNotification(n *platform.Notification) error {
	if !b.NotificationsSupported() {
		return platform.ErrUnsupported
	}
	var err error
	withPool(func() {
		b.askNotificationAuthorization()
		switch {
		case !b.notifyResolved:
			b.notifyPending = append(b.notifyPending, n)
		case b.notifyGranted:
			b.addNotification(n)
		default:
			err = errNotificationsDenied
			if b.notifyErr != nil {
				err = fmt.Errorf("%w: %v", errNotificationsDenied, b.notifyErr)
			}
		}
	})
	return err
}

// RemoveNotification takes a notification away. One still waiting for the
// answer to the authorization request was never given to the system, so
// there is nothing to take back from it.
func (b *Backend) RemoveNotification(ident string) {
	if !b.NotificationsSupported() {
		return
	}
	for i, n := range b.notifyPending {
		if n.ID == ident {
			b.notifyPending = append(b.notifyPending[:i], b.notifyPending[i+1:]...)
			return
		}
	}
	withPool(func() {
		center := notificationCenter()
		ids := nsArray(nsString(ident))
		send(center, "removeDeliveredNotificationsWithIdentifiers:", uintptr(ids))
		send(center, "removePendingNotificationRequestsWithIdentifiers:", uintptr(ids))
	})
}
