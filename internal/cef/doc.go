//go:build linux && (amd64 || arm64)

// Package cef drives the Chromium Embedded Framework through its C API,
// loaded at run time with purego: no cgo and no C++ wrapper. It is the web
// engine of the Linux backend when an app bundles CEF.
//
// The browser process is the app itself. Every other Chromium process runs
// the helper (./helper), a small Go program that `mygo build` compiles with
// the app's version of this package and puts next to libcef.so; it holds
// no app code. A Go process is always multithreaded, which Chromium's
// zygote and Linux sandbox refuse, so child processes are started without
// a zygote and without the sandbox (--no-zygote, --no-sandbox), as
// WebKitGTK's are in MyGo.
//
// Structures of the C API are mirrored by capi_gen.go, which ./gen writes
// from the headers of the CEF version MyGo pins (CEF API version
// apiVersion). Objects CEF calls back (handlers) live in C memory and
// share one purego callback per function slot, created once: each finds
// its Go object by the address CEF passes as self.
package cef
