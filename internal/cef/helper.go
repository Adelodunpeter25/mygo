//go:build linux && (amd64 || arm64)

package cef

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

// The helper runs every child process of Chromium: renderers, the GPU
// process, utilities. Renderers run the page side of MyGo's bridge, the
// only code of MyGo that runs there: the scripts of each window, which the
// browser process passes with the browser (Script), run when a frame's
// JavaScript context is created, before the page's own scripts, and the
// main frame gets __mygoPost, which the bridge takes for its transport.
//
// The bridge's messages cross as UTF-8 JSON in ArrayBuffers both ways,
// which are copied as they are: strings would be converted to UTF-16 and
// back, and the browser's messages would be compiled as a script.
// __mygoPost(buffer) posts a message to the browser process;
// __mygoPost(fn) makes fn the receiver of the browser's messages, which it
// calls with a buffer of each batch of them.

// Script is JavaScript a browser runs in its pages.
type Script struct {
	Source string `json:"source"`
	// AtDocumentEnd runs it once the document is parsed.
	AtDocumentEnd bool `json:"atDocumentEnd,omitzero"`
	// AllFrames runs it in every frame, not just the main one.
	AllFrames bool `json:"allFrames,omitzero"`
}

const (
	// messageName names the process messages of the bridge.
	messageName = "mygo"
	// postName is the global function the bridge takes and deletes.
	postName = "__mygoPost"
	// extraKey holds a browser's scripts, as JSON, in the extra info of
	// its creation.
	extraKey = "mygo"
	// sharedMessageSize is the size from which messages go through shared
	// memory rather than being copied through the IPC channel.
	sharedMessageSize = 64 << 10
)

// RunHelper runs the child process its command line names and returns its
// exit code. libcef.so is next to the executable.
func RunHelper() int {
	runtime.LockOSThread() // Chromium's main thread is this one
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil {
		err = Load(filepath.Dir(exe))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	app := newObject(nil, helperAppClass)
	return int(int32(call(lib.executeProcess, mainArgs(), app.ref(0), 0)))
}

// mainArgs returns a cef_main_args_t of the process' arguments, in C
// memory, which Chromium may keep.
func mainArgs() uintptr {
	argv := cAlloc(uintptr(len(os.Args)+1) * unsafe.Sizeof(uintptr(0)))
	for i, a := range os.Args {
		*at[uintptr](argv + uintptr(i)*unsafe.Sizeof(uintptr(0))) = cString(a)
	}
	args := cAlloc(unsafe.Sizeof(cefMainArgs{}))
	*at[cefMainArgs](args) = cefMainArgs{argc: int32(len(os.Args)), argv: argv}
	return args
}

var (
	helperAppClass, renderHandlerClass, postHandlerClass *class
	// renderHandler and postHandler live as long as the process.
	renderHandler, postHandler *object
)

// rendererScripts holds the scripts of each browser of this renderer, by
// browser identifier. Renderer main thread only.
var rendererScripts = map[int32][]Script{}

// A receiver is the function of a main frame's bridge that takes the
// browser's messages, with the context it belongs to.
type receiver struct{ context, fn uintptr }

// receivers holds the receiver of each browser's main frame, by browser
// identifier. Renderer main thread only.
var receivers = map[int32]receiver{}

func dropReceiver(id int32) {
	if r, ok := receivers[id]; ok {
		release(r.fn)
		release(r.context)
		delete(receivers, id)
	}
}

func initRenderProcessClasses() {
	helperAppClass = newClass[cefApp](map[string]any{
		"getRenderProcessHandler": func(self uintptr) uintptr {
			return renderHandler.ref(0)
		},
	})
	renderHandlerClass = newClass[cefRenderProcessHandler](map[string]any{
		"onBrowserCreated": func(self, browser, extra uintptr) {
			defer release(browser)
			defer release(extra)
			if extra == 0 {
				return
			}
			k := newStr(extraKey)
			raw := takeStr(call(at[cefDictionaryValue](extra).getString, extra, k.p()))
			runtime.KeepAlive(k)
			var scripts []Script
			if json.Unmarshal([]byte(raw), &scripts) == nil {
				rendererScripts[browserID(browser)] = scripts
			}
		},
		"onBrowserDestroyed": func(self, browser uintptr) {
			defer release(browser)
			id := browserID(browser)
			delete(rendererScripts, id)
			dropReceiver(id)
		},
		"onContextCreated": func(self, browser, frame, context uintptr) {
			defer release(browser)
			defer release(frame)
			defer release(context)
			scripts, ok := rendererScripts[browserID(browser)]
			if !ok {
				return
			}
			main := call(at[cefFrame](frame).isMain, frame) != 0
			if main {
				installPost(context)
			}
			for _, s := range scripts {
				if !main && !s.AllFrames {
					continue
				}
				src := s.Source
				if s.AtDocumentEnd {
					src = "document.readyState===\"loading\"?document.addEventListener(\"DOMContentLoaded\",()=>{\n" + src + "\n},{once:true}):(()=>{\n" + src + "\n})();"
				}
				eval(context, src)
			}
		},
		"onContextReleased": func(self, browser, frame, context uintptr) {
			defer release(browser)
			defer release(frame)
			defer release(context)
			id := browserID(browser)
			if r, ok := receivers[id]; ok {
				addRef(r.context) // is_same takes a reference to its argument
				if call(at[cefV8Context](context).isSame, context, r.context) != 0 {
					dropReceiver(id)
				}
			}
		},
		"onProcessMessageReceived": func(self, browser, frame uintptr, source int32, message uintptr) int32 {
			defer release(browser)
			defer release(frame)
			defer release(message)
			if !isBridgeMessage(message) {
				return 0
			}
			// The receiver is the bridge's in the main frame's document, if
			// it has one.
			r, ok := receivers[browserID(browser)]
			if !ok || call(at[cefV8Context](r.context).enter, r.context) == 0 {
				return 1
			}
			messageBytes(message, func(data []byte) {
				buffer := call(lib.v8ValueCreateArrayBufferWithCopy, addr(unsafe.SliceData(data)), uintptr(len(data)))
				if buffer == 0 {
					return
				}
				// execute_function takes the references of its arguments.
				args := [1]uintptr{buffer}
				release(call(at[cefV8Value](r.fn).executeFunction, r.fn, 0, 1, addr(&args[0])))
			})
			call(at[cefV8Context](r.context).exit, r.context)
			return 1
		},
	})
	postHandlerClass = newClass[cefV8Handler](map[string]any{
		"execute": func(self, name, object uintptr, count uintptr, args, retval, exception uintptr) int32 {
			release(object)
			argv := unsafe.Slice(at[uintptr](args), count)
			defer func() {
				for _, a := range argv {
					release(a)
				}
			}()
			var v *cefV8Value
			if count == 1 {
				v = at[cefV8Value](argv[0])
			}
			switch {
			case v != nil && call(v.isArrayBuffer, argv[0]) != 0:
				// The buffer's memory, which is copied once, into the message.
				n := int(call(v.getArrayBufferByteLength, argv[0]))
				var data []byte
				if p := call(v.getArrayBufferData, argv[0]); p != 0 && n > 0 {
					data = unsafe.Slice(at[byte](p), n)
				}
				post(data)
			case v != nil && call(v.isFunction, argv[0]) != 0:
				setReceiver(argv[0])
			default:
				setStr(exception, "__mygoPost takes an ArrayBuffer or a function")
			}
			return 1
		},
	})
	renderHandler = newObject(nil, renderHandlerClass)
	postHandler = newObject(nil, postHandlerClass)
}

func browserID(browser uintptr) int32 {
	return int32(call(at[cefBrowser](browser).getIdentifier, browser))
}

// installPost defines postMessage on the global object of a main frame's
// context, not enumerable, for the bridge, which takes it and deletes it.
func installPost(context uintptr) {
	global := call(at[cefV8Context](context).getGlobal, context)
	if global == 0 {
		return
	}
	defer release(global)
	name := newStr(postName)
	fn := call(lib.v8ValueCreateFunction, name.p(), postHandler.ref(0))
	// set_value_bykey takes the reference to fn.
	call(at[cefV8Value](global).setValueBykey, global, name.p(), fn, v8PropertyAttributeDontenum)
	runtime.KeepAlive(name)
}

// eval runs a script in a context.
func eval(context uintptr, src string) {
	code, url := newStr(src), newStr("")
	var retval, exception uintptr
	call(at[cefV8Context](context).eval, context, code.p(), url.p(), 0, addr(&retval), addr(&exception))
	runtime.KeepAlive(code)
	runtime.KeepAlive(url)
	release(retval)
	release(exception)
}

// setReceiver makes fn, a function of the current context, the receiver
// of the browser's messages for the context's browser.
func setReceiver(fn uintptr) {
	context := call(lib.v8ContextGetCurrentContext) // kept by the receiver
	if context == 0 {
		return
	}
	browser := call(at[cefV8Context](context).getBrowser, context)
	if browser == 0 {
		release(context)
		return
	}
	id := browserID(browser)
	release(browser)
	dropReceiver(id)
	addRef(fn)
	receivers[id] = receiver{context, fn}
}

// post sends a message of the bridge to the browser process.
func post(msg []byte) {
	ctx := call(lib.v8ContextGetCurrentContext)
	if ctx == 0 {
		return
	}
	frame := call(at[cefV8Context](ctx).getFrame, ctx)
	release(ctx)
	if frame == 0 {
		return
	}
	defer release(frame)
	// send_process_message takes the message.
	call(at[cefFrame](frame).sendProcessMessage, frame, pidBrowser, bridgeMessage(msg))
}

// bridgeMessage returns a process message of the bridge holding data:
// small ones as binary values, large ones in shared memory, which saves
// copying them through the IPC channel.
func bridgeMessage(data []byte) uintptr {
	name := messageNameStr
	if len(data) >= sharedMessageSize {
		if b := call(lib.sharedMessageBuilderCreate, name.p(), uintptr(len(data))); b != 0 {
			builder := at[cefSharedProcessMessageBuilder](b)
			var m uintptr
			if call(builder.isValid, b) != 0 {
				copy(unsafe.Slice(at[byte](call(builder.memory, b)), len(data)), data)
				m = call(builder.build, b)
			}
			release(b)
			if m != 0 {
				return m
			}
		}
	}
	m := call(lib.processMessageCreate, name.p())
	list := call(at[cefProcessMessage](m).getArgumentList, m)
	bin := call(lib.binaryValueCreate, addr(unsafe.SliceData(data)), uintptr(len(data))) // copies data
	call(at[cefListValue](list).setBinary, list, 0, bin)                                 // takes bin
	release(list)
	return m
}

// isBridgeMessage reports whether a process message is one of the
// bridge's.
func isBridgeMessage(message uintptr) bool {
	return takeStr(call(at[cefProcessMessage](message).getName, message)) == messageName
}

// messageBytes calls fn with the data of a message of the bridge, which
// fn must not keep.
func messageBytes(message uintptr, fn func(data []byte)) {
	m := at[cefProcessMessage](message)
	if region := call(m.getSharedMemoryRegion, message); region != 0 {
		defer release(region)
		r := at[cefSharedMemoryRegion](region)
		if call(r.isValid, region) != 0 {
			fn(bytesAt(call(r.memory, region), call(r.size, region)))
		}
		return
	}
	list := call(m.getArgumentList, message)
	if list == 0 {
		return
	}
	defer release(list)
	if bin := call(at[cefListValue](list).getBinary, list, 0); bin != 0 {
		defer release(bin)
		v := at[cefBinaryValue](bin)
		fn(bytesAt(call(v.getRawData, bin), call(v.getSize, bin)))
	}
}

// bytesAt returns the n bytes of C memory at p.
func bytesAt(p, n uintptr) []byte {
	if p == 0 || n == 0 {
		return nil
	}
	return unsafe.Slice(at[byte](p), n)
}

// messageNameStr is messageName as a cef_string_t, made once.
var messageNameStr = newStr(messageName)
