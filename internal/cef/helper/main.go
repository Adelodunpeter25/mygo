//go:build linux && (amd64 || arm64)

// Command helper runs the child processes of Chromium (renderers, the GPU
// process, utilities) for MyGo apps that bundle CEF; see package cef. mygo
// build compiles it with the app's version of MyGo and puts it next to
// libcef.so. It holds no app code.
package main

import (
	"os"

	"github.com/egoist/mygo/internal/cef"
)

func main() { os.Exit(cef.RunHelper()) }
