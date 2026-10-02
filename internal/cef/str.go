//go:build linux && (amd64 || arm64)

package cef

import (
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// cefString is cef_string_t, UTF-16 in CEF's builds.
type cefString = cefStringUtf16

// str is a Go string as a cef_string_t, for the const cef_string_t*
// parameters of CEF functions, which copy it. Its UTF-16 buffer lives as
// long as the str: keep it alive (runtime.KeepAlive) until the call
// returns.
type str struct {
	cefString
	buf []uint16
}

func newStr(s string) *str {
	st := &str{}
	if s != "" {
		st.buf = encodeUTF16(s)
		st.cefString = cefString{str: addr(unsafe.SliceData(st.buf)), length: uintptr(len(st.buf))}
	}
	return st
}

// p returns the address of the cef_string_t.
func (s *str) p() uintptr { return addr(&s.cefString) }

func encodeUTF16(s string) []uint16 {
	buf := make([]uint16, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < utf8.RuneSelf {
			buf = append(buf, uint16(c))
			continue
		}
		for _, r := range s[i:] {
			buf = utf16.AppendRune(buf, r)
		}
		break
	}
	return buf
}

// decodeUTF16 copies n UTF-16 code units at p into a Go string.
func decodeUTF16(p, n uintptr) string {
	if p == 0 || n == 0 {
		return ""
	}
	b := appendUTF8(make([]byte, 0, n), p, n)
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// appendUTF8 appends n UTF-16 code units at p to b, as UTF-8.
func appendUTF8(b []byte, p, n uintptr) []byte {
	if p == 0 || n == 0 {
		return b
	}
	u := unsafe.Slice(at[uint16](p), n)
	for i, c := range u {
		if c >= utf8.RuneSelf {
			for _, r := range utf16.Decode(u[i:]) {
				b = utf8.AppendRune(b, r)
			}
			break
		}
		b = append(b, byte(c))
	}
	return b
}

// goStr copies the cef_string_t at p, which may be NULL.
func goStr(p uintptr) string {
	if p == 0 {
		return ""
	}
	s := at[cefString](p)
	return decodeUTF16(s.str, s.length)
}

// takeStr copies a cef_string_userfree_t, which CEF functions return, and
// frees it.
func takeStr(p uintptr) string {
	if p == 0 {
		return ""
	}
	s := goStr(p)
	call(lib.stringUserfreeFree, p)
	return s
}

// setStr sets the cef_string_t at p, an out parameter CEF frees, to s.
func setStr(p uintptr, s string) {
	if p == 0 {
		return
	}
	buf := encodeUTF16(s)
	call(lib.stringUTF16Set, addr(unsafe.SliceData(buf)), uintptr(len(buf)), p, 1)
}

// stringList copies a cef_string_list_t.
func stringList(list uintptr) []string {
	n := call(lib.stringListSize, list)
	out := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		var s cefString
		if call(lib.stringListValue, list, i, addr(&s)) != 0 {
			out = append(out, decodeUTF16(s.str, s.length))
			call(lib.stringUTF16Clear, addr(&s))
		}
	}
	return out
}
