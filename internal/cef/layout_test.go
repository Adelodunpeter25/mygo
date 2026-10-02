//go:build linux && (amd64 || arm64)

package cef

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// layout is a generated structure and the C names of its fields.
type layout struct {
	c      string
	v      any
	fields []string
}

// TestLayout writes, to $CEF_LAYOUT_OUT, a C file that checks the size of
// every generated structure and the offset of every field against the CEF
// headers. Compile it for the target, as of apiVersion, with the
// distribution's root on the include path:
//
//	zig cc -target aarch64-linux-gnu -c -DCEF_API_VERSION=15400 -I <cef_binary_…> layout.c
func TestLayout(t *testing.T) {
	out := os.Getenv("CEF_LAYOUT_OUT")
	if out == "" {
		t.Skip("CEF_LAYOUT_OUT is not set")
	}
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n")
	for _, h := range []string{"cef_app", "cef_browser", "cef_client", "cef_render_process_handler", "cef_scheme", "cef_resource_handler", "cef_v8", "cef_values", "cef_process_message", "cef_shared_process_message_builder", "cef_shared_memory_region", "cef_command_line", "cef_task", "cef_request_context", "cef_cookie", "cef_devtools_message_observer", "cef_registration"} {
		fmt.Fprintf(&b, "#include \"include/capi/%s_capi.h\"\n", h)
	}
	for _, l := range generatedLayouts {
		typ := reflect.TypeOf(l.v)
		if typ.NumField() != len(l.fields) {
			t.Fatalf("%s: %d Go fields, %d C fields", l.c, typ.NumField(), len(l.fields))
		}
		fmt.Fprintf(&b, "_Static_assert(sizeof(%s) == %d, \"size of %s\");\n", l.c, typ.Size(), l.c)
		for i, f := range l.fields {
			fmt.Fprintf(&b, "_Static_assert(offsetof(%s, %s) == %d, \"%s.%s\");\n", l.c, f, typ.Field(i).Offset, l.c, f)
		}
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
