// Package text shapes, wraps and measures text with go-text/typesetting,
// finds fonts among the system's and the app's own, and rasterizes glyphs
// into the atlases scenes draw from. All methods of System are safe from
// any goroutine; the ui package uses them from the main thread.
package text

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
)

// systemFamilies are the user interface fonts of each system, best first.
// fontscan substitutes the generic family at the end with the system's
// own choice when none is installed.
var systemFamilies = map[string][]string{
	"windows": {"Segoe UI", "Tahoma", "sans-serif"},
	"darwin":  {".SF NS", "SF Pro Text", "SF Pro", "Helvetica Neue", "Helvetica", "sans-serif"},
	"linux":   {"Cantarell", "Ubuntu", "Noto Sans", "DejaVu Sans", "Liberation Sans", "sans-serif"},
}

var monospaceFamilies = map[string][]string{
	"windows": {"Cascadia Mono", "Consolas", "Courier New", "monospace"},
	"darwin":  {"SF Mono", "Menlo", "Monaco", "monospace"},
	"linux":   {"DejaVu Sans Mono", "Noto Sans Mono", "Ubuntu Mono", "Liberation Mono", "monospace"},
}

func familiesFor(goos string, m map[string][]string) []string {
	if f, ok := m[goos]; ok {
		return f
	}
	return m["linux"]
}

// families returns the families to try for a Style.Family: a
// comma-separated list where "system-ui", "monospace", "sans-serif" and
// "serif" name generic families, followed by the system UI fonts.
func families(family string) []string {
	var out []string
	add := func(f ...string) {
		for _, name := range f {
			for _, have := range out {
				if strings.EqualFold(have, name) {
					name = ""
					break
				}
			}
			if name != "" {
				out = append(out, name)
			}
		}
	}
	for _, f := range strings.Split(family, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		switch strings.ToLower(f) {
		case "":
		case "system-ui", "ui-sans-serif":
			add(familiesFor(runtime.GOOS, systemFamilies)...)
		case "monospace", "ui-monospace":
			add(familiesFor(runtime.GOOS, monospaceFamilies)...)
		default:
			add(f)
		}
	}
	add(familiesFor(runtime.GOOS, systemFamilies)...)
	return out
}

type quietLogger struct{}

func (quietLogger) Printf(string, ...any) {}

// fonts holds the font map: the system fonts, indexed once and cached on
// disk by fontscan, and the fonts the app registers.
type fonts struct {
	once sync.Once
	fm   *fontscan.FontMap
	err  error
	// registered counts the fonts added with RegisterFont, which may be
	// added before the system fonts are loaded.
	pending []registered
	query   fontscan.Query
	queried bool
}

type registered struct {
	data   []byte
	family string
}

func (f *fonts) load() {
	f.once.Do(func() {
		f.fm = fontscan.NewFontMap(quietLogger{})
		f.fm.SetRuneCacheSize(4096)
		dir, err := os.UserCacheDir()
		if err != nil {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "mygo", "fonts")
		if err := f.fm.UseSystemFonts(dir); err != nil {
			// Without system fonts only the app's own fonts render.
			log.Printf("mygo: cannot load the system fonts: %v", err)
		}
		for _, r := range f.pending {
			if err := addFont(f.fm, r.data, r.family); err != nil {
				log.Print(err)
			}
		}
		f.pending = nil
	})
}

func addFont(fm *fontscan.FontMap, data []byte, family string) error {
	if err := fm.AddFont(bytes.NewReader(data), fmt.Sprintf("mygo-font-%p", &data[0]), family); err != nil {
		return fmt.Errorf("mygo: cannot add font: %w", err)
	}
	return nil
}

// setQuery makes the font map resolve faces of style.
func (f *fonts) setQuery(style Style) {
	q := fontscan.Query{
		Families: families(style.Family),
		Aspect:   font.Aspect{Style: font.StyleNormal, Weight: font.Weight(style.weight()), Stretch: font.StretchNormal},
	}
	if style.Italic {
		q.Aspect.Style = font.StyleItalic
	}
	if f.queried && sameQuery(f.query, q) {
		return
	}
	f.query, f.queried = q, true
	f.fm.SetQuery(q)
}

func sameQuery(a, b fontscan.Query) bool {
	if a.Aspect != b.Aspect || len(a.Families) != len(b.Families) {
		return false
	}
	for i := range a.Families {
		if a.Families[i] != b.Families[i] {
			return false
		}
	}
	return true
}
