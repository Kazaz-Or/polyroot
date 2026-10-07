// Package update replaces the running polyroot binary with a GitHub release.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultReleases is the GitHub releases URL of the project.
const DefaultReleases = "https://github.com/Kazaz-Or/polyroot/releases"

// Options configures an update.
type Options struct {
	Releases string // releases base URL (DefaultReleases unless testing)
	Current  string // running version, e.g. "0.1.0" or "dev"
	Want     string // exact tag to install; "" means latest
	Target   string // binary to replace
	Client   *http.Client
}

// Latest returns the newest published release tag, e.g. "v0.2.0".
// GitHub's /releases/latest redirects to /releases/tag/<tag>.
func Latest(ctx context.Context, o Options) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, o.Releases+"/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for the latest release: %w", err)
	}
	_ = resp.Body.Close()
	tag := resp.Request.URL.Path[strings.LastIndex(resp.Request.URL.Path, "/")+1:]
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("no published release found at %s", o.Releases)
	}
	return tag, nil
}

// Install downloads tag for this OS/CPU, verifies its SHA-256 against the
// release's checksums.txt and atomically replaces o.Target.
func Install(ctx context.Context, o Options, tag string) error {
	version := strings.TrimPrefix(tag, "v")
	archive := fmt.Sprintf("polyroot_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("%s/download/%s/", o.Releases, tag)

	sums, err := o.get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == archive {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("release %s has no build for %s/%s", tag, runtime.GOOS, runtime.GOARCH)
	}
	data, err := o.get(ctx, base+archive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s); nothing was changed", archive, want, got)
	}
	bin, err := extract(data, "polyroot")
	if err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	return replace(o.Target, bin)
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (o Options) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// extract returns the contents of the regular file named name in a .tar.gz.
func extract(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s inside", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Clean(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// replace swaps target for content: write a temp file next to it, then
// rename over it. A running binary can be replaced this way on macOS/Linux.
func replace(target string, content []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".polyroot-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (%w); reinstall with the install script, or use sudo if it lives in a system directory", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// Newer reports whether version a is newer than b ("v" prefixes optional).
// A release (1.2.0) is newer than its pre-releases (1.2.0-rc.1).
func Newer(a, b string) bool {
	pa, ra := parse(a)
	pb, rb := parse(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	switch {
	case ra == rb:
		return false
	case ra == "":
		return true
	case rb == "":
		return false
	}
	return ra > rb // ponytail: lexical pre-release order; fine for rc.1..rc.9
}

func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(v, "v")
	v, pre, _ := strings.Cut(v, "-")
	var n [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		n[i], _ = strconv.Atoi(part)
	}
	return n, pre
}

// IsRelease reports whether version looks like a release build ("dev" and
// `go install` builds are not).
func IsRelease(version string) bool {
	n, _ := parse(version)
	return n != [3]int{} && version != "dev"
}
