//go:build linux && (amd64 || arm64)

package cef

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// A class is a structure of the C API that Go implements: its size and the
// callbacks of its function slots, shared by every instance and created
// once (purego callbacks are never freed). Slots without a callback stay
// NULL, which CEF treats as the default behavior.
type class struct {
	size  uintptr
	slots []uintptr // after the base, in order
}

// newClass returns the class of the structure T. fns gives the callbacks
// of its slots by their Go field names; each receives the address of the
// structure as self, which ownerOf turns into the Go value implementing it.
func newClass[T any](fns map[string]any) *class {
	t := reflect.TypeFor[T]()
	c := &class{size: t.Size(), slots: make([]uintptr, t.NumField()-1)}
	for name, fn := range fns {
		f, ok := t.FieldByName(name)
		if !ok || f.Index[0] == 0 {
			panic(fmt.Sprintf("cef: %s has no slot %s", t.Name(), name))
		}
		c.slots[f.Index[0]-1] = purego.NewCallback(fn)
	}
	return c
}

// An object is one or more structures implemented in Go, in one block of
// C memory, that share a reference count: CEF counts its references from
// any thread, and the memory is freed when the count drops to zero.
type object struct {
	refs  atomic.Int64
	mem   uintptr
	ptrs  []uintptr // the structures, as CEF knows them
	owner any
	// freed runs once the memory is freed.
	freed func()
}

// objects maps the address of every live structure to its object.
var objects sync.Map

// newObject allocates structures of the classes for owner, holding one
// reference, which the caller releases (or keeps for good).
func newObject(owner any, classes ...*class) *object {
	o := &object{owner: owner}
	o.refs.Store(1)
	var size uintptr
	offsets := make([]uintptr, len(classes))
	for i, c := range classes {
		offsets[i] = size
		size += (c.size + 7) &^ 7
	}
	o.mem = cAlloc(size)
	for i, c := range classes {
		p := o.mem + offsets[i]
		base := at[cefBaseRefCounted](p)
		*base = cefBaseRefCounted{size: c.size, addRef: refCallbacks.addRef, release: refCallbacks.release,
			hasOneRef: refCallbacks.hasOneRef, hasAtLeastOneRef: refCallbacks.hasAtLeastOneRef}
		slots := unsafe.Slice(at[uintptr](p+unsafe.Sizeof(*base)), len(c.slots))
		copy(slots, c.slots)
		o.ptrs = append(o.ptrs, p)
		objects.Store(p, o)
	}
	return o
}

// ptr returns the structure of the i-th class.
func (o *object) ptr(i int) uintptr { return o.ptrs[i] }

// ref adds a reference to the structure of the i-th class and returns it,
// for passing it to CEF, which takes the reference.
func (o *object) ref(i int) uintptr {
	o.refs.Add(1)
	return o.ptrs[i]
}

// release drops a reference and reports whether that freed the object.
func (o *object) release() bool {
	if o.refs.Add(-1) != 0 {
		return false
	}
	for _, p := range o.ptrs {
		objects.Delete(p)
	}
	cFree(o.mem)
	if o.freed != nil {
		o.freed()
	}
	return true
}

func objectOf(self uintptr) *object {
	if v, ok := objects.Load(self); ok {
		return v.(*object)
	}
	return nil
}

// ownerOf returns the Go value implementing the structure at self.
func ownerOf[T any](self uintptr) T {
	var zero T
	o := objectOf(self)
	if o == nil {
		return zero
	}
	v, _ := o.owner.(T)
	return v
}

// refCallbacks implement cef_base_ref_counted_t for every object.
var refCallbacks struct {
	addRef, release, hasOneRef, hasAtLeastOneRef uintptr
}

var callbacksOnce sync.Once

// initCallbacks creates the callbacks of every class once.
func initCallbacks() {
	callbacksOnce.Do(func() {
		refCallbacks.addRef = purego.NewCallback(func(self uintptr) {
			if o := objectOf(self); o != nil {
				o.refs.Add(1)
			}
		})
		refCallbacks.release = purego.NewCallback(func(self uintptr) int32 {
			if o := objectOf(self); o != nil && !o.release() {
				return 0
			}
			return 1
		})
		refCallbacks.hasOneRef = purego.NewCallback(func(self uintptr) int32 {
			if o := objectOf(self); o != nil && o.refs.Load() == 1 {
				return 1
			}
			return 0
		})
		refCallbacks.hasAtLeastOneRef = purego.NewCallback(func(self uintptr) int32 {
			if o := objectOf(self); o != nil && o.refs.Load() >= 1 {
				return 1
			}
			return 0
		})
		initBrowserProcessClasses()
		initRenderProcessClasses()
	})
}
