package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// fakeReleases serves /latest -> /tag/v0.2.0 and one release's assets.
func fakeReleases(t *testing.T, archive []byte, sumOverride string) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("polyroot_0.2.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n" + strings.Repeat("0", 64) + "  polyroot_0.2.0_plan9_mips.tar.gz\n"
	if sumOverride != "" {
		sums = sumOverride + "  " + name + "\n"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/v0.2.0", http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/v0.2.0", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/releases/download/v0.2.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/releases/download/v0.2.0/"+name, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func target(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "polyroot")
	if err := os.WriteFile(p, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLatestAndInstall(t *testing.T) {
	srv := fakeReleases(t, tarball(t, map[string]string{"LICENSE": "MIT", "polyroot": "new binary"}), "")
	o := Options{Releases: srv.URL + "/releases", Target: target(t)}
	tag, err := Latest(context.Background(), o)
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("Latest: %q %v", tag, err)
	}
	if err := Install(context.Background(), o, tag); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(o.Target)
	st, _ := os.Stat(o.Target)
	if string(data) != "new binary" || st.Mode().Perm() != 0o755 {
		t.Fatalf("target: %q %v", data, st.Mode())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(o.Target), ".polyroot-update-*")); len(leftovers) > 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestInstallRefusesBadDownloads(t *testing.T) {
	good := tarball(t, map[string]string{"polyroot": "new binary"})
	for name, srv := range map[string]*httptest.Server{
		"checksum mismatch": fakeReleases(t, good, strings.Repeat("a", 64)),
		"no binary inside":  fakeReleases(t, tarball(t, map[string]string{"README.md": "x"}), ""),
	} {
		o := Options{Releases: srv.URL + "/releases", Target: target(t)}
		if err := Install(context.Background(), o, "v0.2.0"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if data, _ := os.ReadFile(o.Target); string(data) != "old binary" {
			t.Errorf("%s: target must be untouched, got %q", name, data)
		}
	}
	srv := fakeReleases(t, good, "")
	if err := Install(context.Background(), Options{Releases: srv.URL + "/releases", Target: target(t)}, "v9.9.9"); err == nil {
		t.Error("missing release must fail")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"0.1.0", "v0.1.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.1", "v1.0.0", false},
		{"v0.1.0", "v0.2.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
	if IsRelease("dev") || !IsRelease("0.1.0") || IsRelease("0.0.0-SNAPSHOT") {
		t.Error("IsRelease")
	}
}
