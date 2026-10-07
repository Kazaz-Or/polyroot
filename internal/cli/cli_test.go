package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	a := &app{stdout: &out, stderr: &out}
	root := a.root("test")
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func setupConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, r := range []string{"git/api", "git/web", "git/sdk"} {
		if err := os.MkdirAll(filepath.Join(dir, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := `version: 1
defaultAgent: claude
repos:
  api: git/api
  web: git/web
  sdk: git/sdk
groups:
  platform: {repos: [sdk]}
workspaces:
  payments: {primary: api, repos: [web], groups: [platform]}
  gaming: {primary: web, groups: [platform], defaultAgent: codex}
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYROOT_CONFIG_HOME", dir)
	t.Setenv("POLYROOT_CACHE_HOME", filepath.Join(dir, "cache"))
	return dir
}

func TestCommandForwardsArgs(t *testing.T) {
	setupConfig(t)
	out, err := run(t, "command", "payments", "--agent", "claude", "--", "--model", "opus", "two words")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--add-dir", "/git/web", "/git/sdk", "--model opus", "'two words'", "--append-system-prompt-file"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("POLYROOT_CACHE_HOME"), "run")); !os.IsNotExist(err) {
		t.Error("`command` must not write generated files")
	}
}

func TestWorkspaceDefaultAgent(t *testing.T) {
	setupConfig(t)
	out, err := run(t, "command", "gaming")
	if err != nil || !strings.Contains(out, "codex") {
		t.Fatalf("expected codex for gaming: %v\n%s", err, out)
	}
}

func TestOpenRejectsExtraPositionals(t *testing.T) {
	setupConfig(t)
	if _, err := run(t, "open", "payments", "--model"); err == nil {
		t.Error("unknown flags must not be silently forwarded")
	}
	if _, err := run(t, "open", "payments", "stray"); err == nil {
		t.Error("args without -- must be rejected")
	}
}

func TestListShowValidate(t *testing.T) {
	dir := setupConfig(t)
	out, err := run(t, "list")
	if err != nil || !strings.Contains(out, "payments   api       3") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = run(t, "show", "payments")
	if err != nil || !strings.Contains(out, "[platform]") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if out, err = run(t, "validate"); err != nil || !strings.Contains(out, "ok    payments") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if err := os.RemoveAll(filepath.Join(dir, "git/sdk")); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, "validate", "gaming"); err == nil || !strings.Contains(out, "sdk") {
		t.Fatalf("validate should fail on missing repo: %v\n%s", err, out)
	}
	if _, err = run(t, "show", "payments"); err == nil {
		t.Fatal("show should fail on missing repo")
	}
}

func TestUpdateCheck(t *testing.T) {
	setupConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/v9.0.0", http.StatusFound)
		}
	}))
	defer srv.Close()
	t.Setenv("POLYROOT_RELEASES_URL", srv.URL+"/releases")

	out, err := run(t, "update", "--check")
	if err != nil || !strings.Contains(out, "latest:  v9.0.0") {
		t.Fatalf("update --check: %v\n%s", err, out)
	}
	// The test binary reports version "test", which is not a release build.
	if _, err := run(t, "update"); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Fatalf("update of a dev build must require --force: %v", err)
	}
}
