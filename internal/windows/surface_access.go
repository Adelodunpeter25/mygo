//go:build windows && (amd64 || arm64)

package windows

import "github.com/egoist/mygo/internal/platform"

// UpdateAccessibility does nothing yet: assistive technology on Windows
// does not see native UI.
func (s *surface) UpdateAccessibility(*platform.AccessTree) {}
