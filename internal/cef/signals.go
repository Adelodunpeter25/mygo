//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"syscall"
	"unsafe"
)

// Chromium installs handlers of SIGHUP, SIGINT and SIGTERM (its shutdown)
// and SIGCHLD as its browser starts, after cef_initialize, without
// SA_ONSTACK. Go needs it: a signal that arrives on a thread running a
// goroutine would run the handler on the goroutine's small stack and
// overwrite it. The shutdown signals go back to Go, so the app quits as it
// does without CEF (mygo's quit sequence); others keep Chromium's handler,
// on the signal stack.

// kernelSigaction is the kernel's struct sigaction on amd64 and arm64.
type kernelSigaction struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     uint64
}

const saOnStack = 0x08000000

// goSignals holds Go's handlers of the signals Chromium takes for its
// shutdown, from before CEF started.
var goSignals = map[syscall.Signal]kernelSigaction{}

func getSigaction(sig syscall.Signal) (kernelSigaction, bool) {
	var a kernelSigaction
	_, _, e := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), 0, uintptr(unsafe.Pointer(&a)), 8, 0, 0)
	return a, e == 0
}

func setSigaction(sig syscall.Signal, a kernelSigaction) {
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&a)), 0, 8, 0, 0)
}

// saveSignals records Go's handlers, before CEF starts.
func saveSignals() {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
		if a, ok := getSigaction(sig); ok {
			goSignals[sig] = a
		}
	}
}

// fixSignals gives the shutdown signals back to Go and the handlers of the
// others SA_ONSTACK. It runs once Chromium started, and again later in
// case it installed more.
func fixSignals() {
	for sig := syscall.Signal(1); sig < 65; sig++ {
		if sig == syscall.SIGKILL || sig == syscall.SIGSTOP {
			continue
		}
		a, ok := getSigaction(sig)
		if !ok {
			continue
		}
		if g, ok := goSignals[sig]; ok {
			if a.handler != g.handler {
				setSigaction(sig, g)
			}
			continue
		}
		// SIG_DFL (0) and SIG_IGN (1) have no handler to run.
		if a.handler > 1 && a.flags&saOnStack == 0 {
			a.flags |= saOnStack
			setSigaction(sig, a)
		}
	}
}
