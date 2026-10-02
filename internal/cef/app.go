//go:build linux && (amd64 || arm64) && !mygo_cef_helper

package cef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Options configure the browser process.
type Options struct {
	// Dir holds libcef.so, its resources and locales, and the helper.
	Dir string
	// Helper is the executable of the child processes.
	Helper string
	// Cache is the profile directory: storage, cookies, caches.
	Cache string
	// Development keeps Chromium's command line switches working and logs
	// warnings; production builds ignore switches given to the app.
	Development bool
	// Post runs fn on the main thread soon; it is called from any thread.
	Post func(fn func())
	// Ready is called on the main thread once browsers can be created.
	Ready func()
}

var (
	opts     Options
	running  bool
	quitting bool
	// browsers counts the browsers not closed yet; Quit waits for them.
	browsers int
)

var appClass, browserProcessClass *class

// app and browserProcess live as long as the process.
var app, browserProcess *object

// Initialize starts CEF in the browser process, on the main thread. Load
// comes first.
func Initialize(o Options) error {
	opts = o
	if err := os.MkdirAll(o.Cache, 0o700); err != nil {
		return err
	}
	settings := &cefSettings{
		size:                    unsafe.Sizeof(cefSettings{}),
		noSandbox:               1,
		commandLineArgsDisabled: int32(cbool(!o.Development)),
		disableSignalHandlers:   1,
		logSeverity:             logseverityWarning,
		persistSessionCookies:   0,
	}
	if !o.Development {
		settings.logSeverity = logseverityError
	}
	strs := []*str{
		setField(&settings.browserSubprocessPath, o.Helper),
		setField(&settings.resourcesDirPath, o.Dir),
		setField(&settings.localesDirPath, filepath.Join(o.Dir, "locales")),
		setField(&settings.rootCachePath, o.Cache),
		setField(&settings.cachePath, o.Cache),
		setField(&settings.logFile, filepath.Join(o.Cache, "cef.log")),
	}
	app = newObject(nil, appClass)
	browserProcess = newObject(nil, browserProcessClass)
	saveSignals()
	ok := call(lib.initialize, mainArgs(), addr(settings), app.ref(0), 0)
	runtime.KeepAlive(settings)
	runtime.KeepAlive(strs)
	if ok == 0 {
		code := int32(call(lib.getExitCode))
		if code == cefResultCodeNormalExitProcessNotified {
			return ErrProfileInUse
		}
		return fmt.Errorf("mygo: CEF failed to start (exit code %d); see %s", code, filepath.Join(o.Cache, "cef.log"))
	}
	return nil
}

// ErrProfileInUse reports that another process uses the profile directory:
// Chromium allows one process per profile.
var ErrProfileInUse = errors.New("mygo: another instance of the app uses its CEF profile")

// setField sets a cef_string_t field of a structure CEF copies, returning
// what holds the buffer.
func setField(f *cefString, s string) *str {
	st := newStr(s)
	*f = st.cefString
	return st
}

// Run runs the message loop until Quit, then shuts CEF down.
func Run() {
	running = true
	call(lib.runMessageLoop)
	running = false
	call(lib.shutdown)
}

// Quit ends Run once every browser has closed: CEF must not shut down with
// browsers open. Browsers that do not close within a few seconds are not
// waited for.
func Quit() {
	if quitting {
		return
	}
	quitting = true
	if browsers == 0 {
		call(lib.quitMessageLoop)
		return
	}
	go func() {
		time.Sleep(3 * time.Second)
		opts.Post(func() {
			if running {
				call(lib.quitMessageLoop)
			}
		})
	}()
}

// browserClosed counts down the open browsers; the last one ends a Quit.
func browserClosed() {
	browsers--
	if quitting && browsers == 0 && running {
		call(lib.quitMessageLoop)
	}
}

// NestedLoop lets CEF run its tasks in a native message loop that fn runs
// on the main thread, such as a modal dialog or a wait for a result CEF
// delivers: Chromium otherwise defers them until the loop ends.
func NestedLoop(fn func()) {
	call(lib.setNestableTasksAllowed, 1)
	defer call(lib.setNestableTasksAllowed, 0)
	fn()
}

// ProfileInUse reports whether a running process holds the profile in dir.
// Chromium's lock is a link named SingletonLock to "<host>-<pid>".
func ProfileInUse(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(target, '-')
	if i < 0 {
		return false
	}
	host, _ := os.Hostname()
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || target[:i] != host {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func initBrowserProcessClasses() {
	initDevToolsClass()
	initDataClasses()
	initSchemeClasses()
	initBrowserClasses()
	appClass = newClass[cefApp](map[string]any{
		"onBeforeCommandLineProcessing": func(self, processType, commandLine uintptr) {
			defer release(commandLine)
			if goStr(processType) != "" {
				return
			}
			cl := at[cefCommandLine](commandLine)
			for _, s := range []string{
				// Go cannot be Chromium's zygote, which forks, nor run in its
				// sandbox, which needs a single thread.
				"no-zygote",
				"no-sandbox",
				// Child windows of X11 windows: CEF does not embed in
				// Wayland surfaces.
				"ozone-platform=x11",
				// No first run dialogs or EULA, which would end the process.
				"no-first-run",
				// No keyring, whose unlock dialog would block startup.
				"password-store=basic",
				// Nothing downloaded in the background (Widevine and other
				// components).
				"disable-component-update",
				// No extensions, for which Chromium would make a directory
				// next to the executable. The PDF viewer, a component, stays.
				"disable-extensions",
			} {
				name, value, hasValue := strings.Cut(s, "=")
				n := newStr(name)
				if hasValue {
					v := newStr(value)
					call(cl.appendSwitchWithValue, commandLine, n.p(), v.p())
					runtime.KeepAlive(v)
				} else {
					call(cl.appendSwitch, commandLine, n.p())
				}
				runtime.KeepAlive(n)
			}
		},
		"getBrowserProcessHandler": func(self uintptr) uintptr {
			return browserProcess.ref(0)
		},
	})
	browserProcessClass = newClass[cefBrowserProcessHandler](map[string]any{
		"onContextInitialized": func(self uintptr) {
			fixSignals()
			if opts.Ready != nil {
				opts.Ready()
			}
		},
		"onBeforeChildProcessLaunch": func(self, commandLine uintptr) {
			defer release(commandLine)
			// Chromium's signal handlers in the helper would turn the Go
			// runtime's signals into crashes.
			n := newStr("disable-in-process-stack-traces")
			call(at[cefCommandLine](commandLine).appendSwitch, commandLine, n.p())
			runtime.KeepAlive(n)
		},
		// Another process started with this profile, which MyGo prevents
		// (ProfileInUse): there is nothing to open for it.
		"onAlreadyRunningAppRelaunch": func(self, commandLine, dir uintptr) int32 {
			release(commandLine)
			return 1
		},
	})
}
