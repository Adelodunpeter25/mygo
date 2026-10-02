package main

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCEFConfig(t *testing.T) {
	for _, tc := range []struct {
		json    string
		enabled bool
		locales []string
	}{
		{`{}`, false, nil},
		{`{"cef": false}`, false, nil},
		{`{"cef": null}`, false, nil},
		{`{"cef": true}`, true, nil},
		{`{"cef": {}}`, true, nil},
		{`{"cef": {"locales": ["en-US", "de"]}}`, true, []string{"en-US", "de"}},
	} {
		var l Linux
		if err := json.Unmarshal([]byte(tc.json), &l); err != nil {
			t.Fatalf("%s: %v", tc.json, err)
		}
		cef := l.cef()
		if (cef != nil) != tc.enabled {
			t.Errorf("%s: CEF enabled %v, want %v", tc.json, cef != nil, tc.enabled)
		}
		if cef != nil && !slices.Equal(cef.Locales, tc.locales) {
			t.Errorf("%s: locales %v, want %v", tc.json, cef.Locales, tc.locales)
		}
	}
	if err := json.Unmarshal([]byte(`{"cef": 1}`), new(Linux)); err == nil {
		t.Error("cef: 1 was accepted")
	}
}

func TestCEFLocales(t *testing.T) {
	all, some := &CEF{enabled: true}, &CEF{enabled: true, Locales: []string{"en-US", "fr"}}
	for _, tc := range []struct {
		file      string
		all, some bool
	}{
		{"en-US.pak", true, true},
		{"en-us.pak", true, true},
		{"fr.pak", true, true},
		{"fr_FEMININE.pak", true, true},
		{"en-GB.pak", true, false},
		{"en-US_NEUTER.pak", true, true},
		{"fil.pak", true, false},
	} {
		if got := all.wantsLocale(tc.file); got != tc.all {
			t.Errorf("all locales: %s %v, want %v", tc.file, got, tc.all)
		}
		if got := some.wantsLocale(tc.file); got != tc.some {
			t.Errorf("en-US and fr: %s %v, want %v", tc.file, got, tc.some)
		}
	}
}

func TestCEFLinuxPackaging(t *testing.T) {
	c := &Config{Name: "App", Linux: Linux{CEF: &CEF{enabled: true}}}
	if deps := strings.Join(debDepends(c), ", "); strings.Contains(deps, "webkit") || !strings.Contains(deps, "libnss3") {
		t.Errorf("CEF app depends on %s", deps)
	}
	if !slices.Contains(reservedNames(c, "linux"), cefDirName) {
		t.Error("the cef directory is not reserved")
	}
	if s := installScript(c); strings.Contains(s, "needs WebKitGTK") {
		t.Error("install.sh of a CEF app checks for WebKitGTK")
	}
	c.Linux.CEF = nil
	if deps := strings.Join(debDepends(c), ", "); !strings.Contains(deps, "libwebkit2gtk-4.1-0") {
		t.Errorf("app depends on %s", deps)
	}
	if s := installScript(c); !strings.Contains(s, "needs WebKitGTK") {
		t.Error("install.sh does not check for WebKitGTK")
	}
}

// TestCEFBundle puts CEF and the helper for this machine's architecture
// in $MYGO_CEF_OUT/cef, as mygo build does, downloading CEF into the cache
// the first time: CI runs the GUI tests with them (MYGO_CEF_DIR).
func TestCEFBundle(t *testing.T) {
	out := os.Getenv("MYGO_CEF_OUT")
	if out == "" {
		t.Skip("MYGO_CEF_OUT is not set")
	}
	c := &Config{root: ".", Linux: Linux{CEF: &CEF{enabled: true}}}
	if err := bundleCEF(c, out, runtime.GOARCH, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"libcef.so", "icudtl.dat", "resources.pak", "locales/en-US.pak", cefHelper} {
		if _, err := os.Stat(filepath.Join(out, cefDirName, name)); err != nil {
			t.Error(err)
		}
	}
}

// TestStripELF strips a Linux program built with debug information: what
// its segments load stays the same, the rest goes.
func TestStripELF(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module strip\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(dir, "prog"), filepath.Join(dir, "prog.stripped")
	for _, arch := range []string{"amd64", "arm64"} {
		cmd := goCommand(dir, []string{"GOOS=linux", "GOARCH=" + arch}, "build", "-ldflags=-compressdwarf=false", "-o", src, ".")
		cmd.Stdout, cmd.Stderr = nil, nil
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", arch, err, out)
		}
		if err := stripELF(src, dst); err != nil {
			t.Fatal(err)
		}
		before, after := mustReadELF(t, src), mustReadELF(t, dst)
		in, _ := os.ReadFile(src)
		out, _ := os.ReadFile(dst)
		if len(out) >= len(in) {
			t.Errorf("%s: %d bytes after stripping, %d before", arch, len(out), len(in))
		}
		var end uint64
		for i, p := range before.Progs {
			if p.ProgHeader != after.Progs[i].ProgHeader {
				t.Errorf("%s: segment %d changed: %+v, was %+v", arch, i, after.Progs[i].ProgHeader, p.ProgHeader)
			}
			end = max(end, p.Off+p.Filesz)
		}
		// Only the ELF header's e_shoff, e_shnum and e_shstrndx change.
		for _, b := range [][]byte{in, out} {
			clear(b[40:48])
			clear(b[60:64])
		}
		if !bytes.Equal(in[:end], out[:end]) {
			t.Errorf("%s: the bytes the segments load changed", arch)
		}
		var kept []string
		for _, s := range before.Sections {
			if s.Flags&elf.SHF_ALLOC != 0 {
				kept = append(kept, s.Name)
				if a := after.Section(s.Name); a == nil || a.Addr != s.Addr || a.Size != s.Size || a.Offset != s.Offset {
					t.Errorf("%s: section %s changed", arch, s.Name)
				}
			}
		}
		for _, s := range after.Sections[1:] {
			if s.Name != ".shstrtab" && !slices.Contains(kept, s.Name) {
				t.Errorf("%s: %s was kept", arch, s.Name)
			}
		}
		if after.Section(".debug_info") != nil || after.Section(".symtab") != nil {
			t.Errorf("%s: debug information or symbols left", arch)
		}
	}
}

func mustReadELF(t *testing.T, path string) *elf.File {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
