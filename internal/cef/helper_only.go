//go:build linux && (amd64 || arm64) && mygo_cef_helper

package cef

// The helper (built with the mygo_cef_helper tag) runs no browser: its
// classes, and what they need, stay out of it.
func initBrowserProcessClasses() {}
