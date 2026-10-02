//go:build linux && (amd64 || arm64)

package cef

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// lib holds the functions libcef exports, bound by Load.
var lib struct {
	apiHash, executeProcess, initialize, getExitCode, shutdown          uintptr
	runMessageLoop, quitMessageLoop, setNestableTasksAllowed            uintptr
	createBrowserSync, registerSchemeHandlerFactory                     uintptr
	stringUTF16Set, stringUTF16Clear, stringUserfreeFree                uintptr
	stringListAlloc, stringListSize, stringListValue, stringListFree    uintptr
	multimapAlloc, multimapSize, multimapKey, multimapValue             uintptr
	multimapAppend, multimapFree                                        uintptr
	processMessageCreate, sharedMessageBuilderCreate, binaryValueCreate uintptr
	dictionaryValueCreate, listValueCreate                              uintptr
	v8ContextGetCurrentContext, v8ValueCreateFunction                   uintptr
	v8ValueCreateArrayBufferWithCopy                                    uintptr
	cookieManagerGetGlobalManager                                       uintptr
	getXDisplay                                                         uintptr
}

var symbols = []struct {
	name string
	p    *uintptr
}{
	{"cef_api_hash", &lib.apiHash},
	{"cef_execute_process", &lib.executeProcess},
	{"cef_initialize", &lib.initialize},
	{"cef_get_exit_code", &lib.getExitCode},
	{"cef_shutdown", &lib.shutdown},
	{"cef_run_message_loop", &lib.runMessageLoop},
	{"cef_quit_message_loop", &lib.quitMessageLoop},
	{"cef_set_nestable_tasks_allowed", &lib.setNestableTasksAllowed},
	{"cef_browser_host_create_browser_sync", &lib.createBrowserSync},
	{"cef_register_scheme_handler_factory", &lib.registerSchemeHandlerFactory},
	{"cef_string_utf16_set", &lib.stringUTF16Set},
	{"cef_string_utf16_clear", &lib.stringUTF16Clear},
	{"cef_string_userfree_utf16_free", &lib.stringUserfreeFree},
	{"cef_string_list_alloc", &lib.stringListAlloc},
	{"cef_string_list_size", &lib.stringListSize},
	{"cef_string_list_value", &lib.stringListValue},
	{"cef_string_list_free", &lib.stringListFree},
	{"cef_string_multimap_alloc", &lib.multimapAlloc},
	{"cef_string_multimap_size", &lib.multimapSize},
	{"cef_string_multimap_key", &lib.multimapKey},
	{"cef_string_multimap_value", &lib.multimapValue},
	{"cef_string_multimap_append", &lib.multimapAppend},
	{"cef_string_multimap_free", &lib.multimapFree},
	{"cef_process_message_create", &lib.processMessageCreate},
	{"cef_shared_process_message_builder_create", &lib.sharedMessageBuilderCreate},
	{"cef_binary_value_create", &lib.binaryValueCreate},
	{"cef_dictionary_value_create", &lib.dictionaryValueCreate},
	{"cef_list_value_create", &lib.listValueCreate},
	{"cef_v8_context_get_current_context", &lib.v8ContextGetCurrentContext},
	{"cef_v8_value_create_function", &lib.v8ValueCreateFunction},
	{"cef_v8_value_create_array_buffer_with_copy", &lib.v8ValueCreateArrayBufferWithCopy},
	{"cef_cookie_manager_get_global_manager", &lib.cookieManagerGetGlobalManager},
}

// C memory, for the structures CEF keeps.
var calloc, free uintptr

var (
	loadOnce sync.Once
	loadErr  error
)

// Load loads libcef.so from dir and checks that it implements the CEF API
// version this package is written against. It must come before any other
// call, in every process.
func Load(dir string) error {
	loadOnce.Do(func() { loadErr = load(dir) })
	return loadErr
}

func load(dir string) error {
	libc, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW)
	if err != nil {
		return err
	}
	if calloc, err = purego.Dlsym(libc, "calloc"); err != nil {
		return err
	}
	if free, err = purego.Dlsym(libc, "free"); err != nil {
		return err
	}
	path := filepath.Join(dir, "libcef.so")
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return fmt.Errorf("mygo: loading CEF: %w", err)
	}
	for _, s := range symbols {
		if *s.p, err = purego.Dlsym(h, s.name); err != nil {
			return fmt.Errorf("mygo: %s is not the CEF MyGo needs (%s): %w", path, cefVersion, err)
		}
	}
	// The API version comes first: it selects the structures libcef
	// expects, those of capi_gen.go.
	if got := goCString(call(lib.apiHash, apiVersion, 0)); got != apiHash {
		return fmt.Errorf("mygo: %s does not implement CEF API %d, which MyGo uses (CEF %s); its hash for it is %q, not %q", path, apiVersion, cefVersion, got, apiHash)
	}
	lib.getXDisplay, _ = purego.Dlsym(h, "cef_get_xdisplay")
	releaseRelocations(path)
	initCallbacks()
	return nil
}

// releaseRelocations unmaps the pages of libcef.so's relocation table
// from this process: the dynamic loader reads it once, as it loads the
// library, and its 28 MB would stay resident in every process of the app.
// The pages stay in the page cache for the loaders of the next processes.
func releaseRelocations(path string) {
	f, err := elf.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	rela := f.Section(".rela.dyn")
	base := loadBase(f)
	if rela == nil || base == 0 {
		return
	}
	page := uintptr(os.Getpagesize())
	start := (base + uintptr(rela.Addr) + page - 1) &^ (page - 1)
	end := (base + uintptr(rela.Addr+rela.Size)) &^ (page - 1)
	if end > start {
		syscall.Syscall(syscall.SYS_MADVISE, start, end-start, syscall.MADV_DONTNEED)
	}
}

// loadBase returns the address libcef.so, f, is loaded at: where the
// mapping of its first loaded segment starts, less that segment's address.
func loadBase(f *elf.File) uintptr {
	var first *elf.Prog
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Off == 0 {
			first = p
			break
		}
	}
	maps, err := os.ReadFile("/proc/self/maps")
	if first == nil || err != nil {
		return 0
	}
	// start-end perms offset dev inode path
	for _, line := range strings.Split(string(maps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[2] != "00000000" || !strings.HasSuffix(fields[5], "/libcef.so") {
			continue
		}
		start, err := strconv.ParseUint(strings.Split(fields[0], "-")[0], 16, 64)
		if err != nil {
			return 0
		}
		return uintptr(start) - uintptr(first.Vaddr)&^uintptr(os.Getpagesize()-1)
	}
	return 0
}

// XDisplay returns the Xlib display of CEF's own X11 connection, or 0.
func XDisplay() uintptr {
	if lib.getXDisplay == 0 {
		return 0
	}
	return call(lib.getXDisplay)
}

// call calls a C function with integer and pointer arguments. Pointers to Go
// memory converted to uintptr in the call stay valid during it.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn, args...)
	return r
}

// at returns the structure at a C address.
func at[T any](p uintptr) *T {
	return (*T)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
}

// addr returns the address of a value, for C.
func addr[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

func cbool(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}

// goCString copies a NUL terminated C string.
func goCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *at[byte](p + uintptr(n)) != 0 {
		n++
	}
	return string(unsafe.Slice(at[byte](p), n))
}

// cAlloc returns zeroed C memory, which cFree releases.
func cAlloc(size uintptr) uintptr { return call(calloc, 1, size) }

func cFree(p uintptr) {
	if p != 0 {
		call(free, p)
	}
}

// cString copies s into C memory, NUL terminated, for the life of the
// process.
func cString(s string) uintptr {
	p := cAlloc(uintptr(len(s)) + 1)
	copy(unsafe.Slice(at[byte](p), len(s)), s)
	return p
}

// Reference counting of the structures CEF implements: their base comes
// first.

func addRef(p uintptr) {
	if p != 0 {
		call(at[cefBaseRefCounted](p).addRef, p)
	}
}

func release(p uintptr) {
	if p != 0 {
		call(at[cefBaseRefCounted](p).release, p)
	}
}
