package main

import (
	"archive/tar"
	"compress/bzip2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Linux apps may bundle Chromium (linux.cef): the Chromium Embedded
// Framework, which package internal/cef drives in place of WebKitGTK. mygo
// build downloads the minimal build of the CEF version MyGo pins once per
// architecture, checks its SHA-256 and unpacks what an app runs into the
// user cache, with the debug information stripped from its libraries (2.2
// of libcef.so's 2.5 GB on arm64). Apps get those files in a cef directory
// next to the executable, with the helper, the executable of Chromium's
// child processes, which mygo build compiles with the app's version of
// MyGo.

// CEF configures the Chromium of a Linux app; `cef: true` takes the
// defaults.
type CEF struct {
	// Locales lists the languages of Chromium's own texts to ship (form
	// validation messages, its context menu), such as ["en-US", "de"]:
	// Chromium shows en-US for others. Default: all 55, 50 MB.
	Locales []string `json:"locales"`

	enabled bool
}

// UnmarshalJSON takes true (the defaults), false or an object.
func (c *CEF) UnmarshalJSON(data []byte) error {
	switch strings.TrimSpace(string(data)) {
	case "true":
		*c = CEF{enabled: true}
		return nil
	case "false":
		*c = CEF{}
		return nil
	}
	type plain CEF
	if err := json.Unmarshal(data, (*plain)(c)); err != nil {
		return err
	}
	c.enabled = true
	return nil
}

// cef returns the CEF configuration of a Linux app that bundles it, else
// nil.
func (l *Linux) cef() *CEF {
	if l.CEF == nil || !l.CEF.enabled {
		return nil
	}
	return l.CEF
}

// wantsLocale reports whether the app ships a .pak file of Chromium's
// locales: those of its languages, with their grammatical genders
// (fr_FEMININE.pak, empty in CEF's builds).
func (c *CEF) wantsLocale(file string) bool {
	lang, _, _ := strings.Cut(strings.TrimSuffix(file, ".pak"), "_")
	return len(c.Locales) == 0 || slices.ContainsFunc(c.Locales, func(l string) bool { return strings.EqualFold(l, lang) })
}

// cefRelease is the CEF that mygo build bundles: the one package
// internal/cef is written against, whose API version it checks.
var cefRelease = struct {
	version string
	builds  map[string]cefBuild // by GOARCH
}{
	version: "154.0.32+g682c378+chromium-154.0.8037.58",
	builds: map[string]cefBuild{
		"amd64": {"linux64", "9b6a82e04506d5e1af560e031e718c89af5f96760413fd16380358784545d153", 326397696},
		"arm64": {"linuxarm64", "65829646cad7223c68bbcc659257e41ec282741adf2570d000722d201c3ccf1e", 424005444},
	},
}

type cefBuild struct {
	platform, sha256 string
	size             int64
}

func (b cefBuild) url() string {
	name := "cef_binary_" + cefRelease.version + "_" + b.platform + "_minimal.tar.bz2"
	return "https://cef-builds.spotifycdn.com/" + strings.ReplaceAll(name, "+", "%2B")
}

// cefHelper is the executable of Chromium's child processes in the cef
// directory; internal/linux names it too.
const cefHelper = "mygo-helper"

// cefFiles are the files of a distribution that apps run, by their path in
// it, and the licenses. Resources/locales holds a .pak file per language.
var cefFiles = []string{
	"Release/libcef.so",
	"Release/libvk_swiftshader.so",
	"Release/libvulkan.so.1",
	"Release/vk_swiftshader_icd.json",
	"Release/v8_context_snapshot.bin",
	"Resources/icudtl.dat",
	"Resources/resources.pak",
	"Resources/chrome_100_percent.pak",
	"Resources/chrome_200_percent.pak",
	"LICENSE.txt",
	"CREDITS.html",
}

func cefCacheDir(goarch string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "mygo", "cef-"+cefRelease.version, "linux-"+goarch)
}

// cefDist returns the directory of the CEF files for goarch, downloading
// and unpacking them the first time. Like NSIS, they are unpacked next to
// the directory and renamed into place, so it is complete whenever it
// exists.
func cefDist(goarch string) (string, error) {
	build, ok := cefRelease.builds[goarch]
	if !ok {
		return "", fmt.Errorf("CEF does not run on linux/%s (only amd64 and arm64)", goarch)
	}
	dir := cefCacheDir(goarch)
	if dir == "" {
		return "", errors.New("no cache directory for CEF")
	}
	if fileExists(filepath.Join(dir, "libcef.so")) {
		return dir, nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(parent, ".cef-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	logf("downloading CEF %s for linux/%s (%s, once)", cefRelease.version, goarch, formatSize(build.size))
	archive := filepath.Join(work, "cef.tar.bz2")
	if err := downloadFile(build.url(), build.sha256, archive, build.size, time.Hour); err != nil {
		return "", fmt.Errorf("downloading CEF: %w", err)
	}
	logf("unpacking CEF")
	unpacked := filepath.Join(work, "cef")
	if err := unpackCEF(archive, unpacked); err != nil {
		return "", fmt.Errorf("unpacking CEF: %w", err)
	}
	os.Remove(archive)
	if err := os.Rename(unpacked, dir); err != nil {
		if fileExists(filepath.Join(dir, "libcef.so")) {
			return dir, nil // another build unpacked it meanwhile
		}
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		if err := os.Rename(unpacked, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// unpackCEF unpacks the files apps run from a minimal distribution into
// dir, flat, stripping the libraries.
func unpackCEF(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(bzip2.NewReader(f))
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		_, name, _ := strings.Cut(hdr.Name, "/") // under cef_binary_…/
		var target string
		switch {
		case slices.Contains(cefFiles, name):
			target = filepath.Join(dir, path.Base(name))
		case strings.HasPrefix(name, "Resources/locales/") && strings.HasSuffix(name, ".pak"):
			target = filepath.Join(dir, "locales", path.Base(name))
		default:
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a file", name)
		}
		found[name] = true
		mode := os.FileMode(0o644)
		if hdr.FileInfo().Mode()&0o111 != 0 {
			mode = 0o755
		}
		if strings.HasSuffix(name, ".so") || strings.Contains(name, ".so.") {
			// Unpacked whole, then stripped: the section headers come last.
			raw := target + ".unstripped"
			if err := writeFile(raw, tr, mode); err != nil {
				return err
			}
			if err := stripELF(raw, target); err != nil {
				return err
			}
			os.Remove(raw)
			continue
		}
		if err := writeFile(target, tr, mode); err != nil {
			return err
		}
	}
	for _, name := range cefFiles {
		if !found[name] {
			return fmt.Errorf("the archive holds no %s", name)
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// downloadFile fetches url into the file path and checks its size and
// SHA-256.
func downloadFile(url, sum, path string, size int64, timeout time.Duration) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mygo/"+version)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = downloadHeaderTimeout
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("%s has %d bytes, not %d", url, n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s has the SHA-256 %s, not %s", url, got, sum)
	}
	return nil
}

// cefDirName is the directory of CEF next to a Linux app's executable.
const cefDirName = "cef"

// cefFlag links the CEF directory into a Linux app (-X), which makes it
// use CEF.
func cefFlag() string {
	return " -X github.com/egoist/mygo/internal/linux.cefDir=" + cefDirName
}

// bundleCEF puts CEF and the helper into dir/cef: copies of the cached
// files, or links to them for development builds, which keep running from
// the cache.
func bundleCEF(c *Config, dir, goarch string, link bool) error {
	dist, err := cefDist(goarch)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, cefDirName)
	if err := os.MkdirAll(filepath.Join(target, "locales"), 0o755); err != nil {
		return err
	}
	place := func(name string) error {
		src, dst := filepath.Join(dist, name), filepath.Join(target, name)
		if link {
			os.Remove(dst)
			return os.Symlink(src, dst)
		}
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		return copyFile(src, dst, info.Mode())
	}
	entries, err := os.ReadDir(dist)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			if err := place(e.Name()); err != nil {
				return err
			}
		}
	}
	locales, err := os.ReadDir(filepath.Join(dist, "locales"))
	if err != nil {
		return err
	}
	for _, e := range locales {
		if !c.Linux.cef().wantsLocale(e.Name()) {
			continue
		}
		if err := place(filepath.Join("locales", e.Name())); err != nil {
			return err
		}
	}
	return buildCEFHelper(c, filepath.Join(target, cefHelper), goarch)
}

// buildCEFHelper compiles the helper with the app's version of MyGo: it
// must speak the app's protocol.
func buildCEFHelper(c *Config, out, goarch string) error {
	cmd := goCommand(c.root, []string{"GOOS=linux", "GOARCH=" + goarch},
		"build", "-trimpath", "-ldflags", "-s -w", "-tags", "mygo_cef_helper", "-o", out, "github.com/egoist/mygo/internal/cef/helper")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building the CEF helper: %w", err)
	}
	return nil
}
