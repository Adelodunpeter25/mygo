//go:build linux && (amd64 || arm64)

package linux

import "github.com/egoist/mygo/internal/platform"

// UpdateAccessibility does nothing yet: assistive technology on Linux
// does not see native UI.
func (s *surface) UpdateAccessibility(*platform.AccessTree) {}
