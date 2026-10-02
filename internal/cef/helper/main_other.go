//go:build !(linux && (amd64 || arm64))

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "the CEF helper runs on Linux only")
	os.Exit(1)
}
