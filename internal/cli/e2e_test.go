package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// When the test binary is invoked as a fake agent (through a symlink named
// claude, codex, ...), it records how it was launched and exits.
const fakeOutEnv = "POLYROOT_FAKE_AGENT_OUT"

type launchRecord struct {
	Name  string            `json:"name"`
	Args  []string          `json:"args"`
	Cwd   string            `json:"cwd"`
	Env   map[string]string `json:"env"`
	Files map[string]string `json:"files"` // generated files visible at launch
}

func TestMain(m *testing.M) {
	if out := os.Getenv(fakeOutEnv); out != "" {
		os.Exit(fakeAgent(out))
	}
	os.Exit(m.Run())
}

func fakeAgent(out string) int {
	rec := launchRecord{Name: filepath.Base(os.Args[0]), Args: os.Args[1:], Env: map[string]string{}, Files: map[string]string{}}
	rec.Cwd, _ = os.Getwd()
	for _, k := range []string{"CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD", "OPENCODE_CONFIG_CONTENT", "E2E_MARK"} {
		if v, ok := os.LookupEnv(k); ok {
			rec.Env[k] = v
		}
	}
	_ = filepath.WalkDir(filepath.Join(os.Getenv("POLYROOT_CACHE_HOME"), "run"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			rec.Files[p] = string(data)
		}
		return nil
	})
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return 99
	}
	code, _ := strconv.Atoi(os.Getenv("POLYROOT_FAKE_AGENT_EXIT"))
	return code
}

type e2eEnv struct {
	root, cache, record string
	api, web, sdk       string // canonical repo paths
}

func setupE2E(t *testing.T) e2eEnv {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real // macOS /var -> /private/var
	}
	e := e2eEnv{
		root: root, cache: filepath.Join(root, "cache"), record: filepath.Join(root, "launch.json"),
		api: filepath.Join(root, "src", "payments api"), web: filepath.Join(root, "src", "web"), sdk: filepath.Join(root, "src", "shared sdk"),
	}
	for _, d := range []string{e.api, e.web, e.sdk, filepath.Join(root, "cfg", "contexts"), filepath.Join(root, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(e.web, "AGENTS.md"), "# web rules\n")
	write(filepath.Join(root, "cfg", "contexts", "payments.md"), "Checkout flows web -> api.\n")
	write(filepath.Join(root, "cfg", "config.yaml"), `version: 1
defaultAgent: claude
repos:
  payments-api: ${E2E_ROOT}/src/payments api
  web: $E2E_ROOT/src/web
  sdk:
    path: ../src/shared sdk
groups:
  platform: {repos: [sdk]}
workspaces:
  payments:
    primary: payments-api
    repos: [web]
    groups: [platform]
    context: contexts/payments.md
`)

	// Fake agents: symlinks to this test binary, alone on PATH.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "pi"} {
		if err := os.Symlink(self, filepath.Join(root, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(root, "bin"))
	t.Setenv("E2E_ROOT", root)
	t.Setenv("POLYROOT_CONFIG_HOME", filepath.Join(root, "cfg"))
	t.Setenv("POLYROOT_CACHE_HOME", e.cache)
	t.Setenv(fakeOutEnv, e.record)
	t.Setenv("POLYROOT_FAKE_AGENT_EXIT", "0")
	t.Setenv("CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD", "")
	_ = os.Unsetenv("CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	_ = os.Unsetenv("OPENCODE_CONFIG_CONTENT")
	return e
}

func (e e2eEnv) open(t *testing.T, args ...string) (launchRecord, error) {
	t.Helper()
	_ = os.Remove(e.record)
	_, err := run(t, append([]string{"-q", "open"}, args...)...)
	var rec launchRecord
	data, readErr := os.ReadFile(e.record)
	if readErr != nil {
		t.Fatalf("agent was not launched (open error: %v)", err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	// Generated files must be gone once the agent exits.
	if entries, _ := os.ReadDir(filepath.Join(e.cache, "run")); len(entries) > 0 {
		t.Errorf("run directory not cleaned up: %v", entries)
	}
	return rec, err
}

func generated(t *testing.T, rec launchRecord, suffix string) string {
	t.Helper()
	for p, content := range rec.Files {
		if strings.HasSuffix(p, suffix) {
			if !strings.Contains(content, "# Polyroot Workspace: payments") || !strings.Contains(content, "Checkout flows web -> api.") {
				t.Errorf("%s lacks workspace map or context:\n%s", p, content)
			}
			return p
		}
	}
	t.Fatalf("no generated %s; files: %v", suffix, rec.Files)
	return ""
}

func TestE2EOpenEveryAgent(t *testing.T) {
	e := setupE2E(t)
	forwarded := []string{"--", "--flag", "a prompt with spaces"}

	t.Run("claude", func(t *testing.T) {
		rec, err := e.open(t, append([]string{"payments"}, forwarded...)...) // default agent
		if err != nil {
			t.Fatal(err)
		}
		ctx := generated(t, rec, "workspace.md")
		want := []string{"--add-dir", e.web, "--add-dir", e.sdk, "--append-system-prompt-file", ctx, "--flag", "a prompt with spaces"}
		if rec.Name != "claude" || !slices.Equal(rec.Args, want) {
			t.Errorf("got %s %q\nwant %q", rec.Name, rec.Args, want)
		}
		if rec.Cwd != e.api {
			t.Errorf("cwd %q, want primary %q", rec.Cwd, e.api)
		}
		if rec.Env["CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"] != "1" {
			t.Errorf("env: %v", rec.Env)
		}
	})

	t.Run("codex", func(t *testing.T) {
		rec, err := e.open(t, append([]string{"payments", "--agent", "codex"}, forwarded...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rec.Args[:4], []string{"--add-dir", e.web, "--add-dir", e.sdk}) || rec.Args[4] != "-c" ||
			!strings.HasPrefix(rec.Args[5], `developer_instructions="# Polyroot Workspace: payments`) ||
			!slices.Equal(rec.Args[6:], forwarded[1:]) {
			t.Errorf("args %q", rec.Args)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		rec, err := e.open(t, append([]string{"payments", "--agent", "gemini"}, forwarded...)...)
		if err != nil {
			t.Fatal(err)
		}
		ctx := generated(t, rec, filepath.Join("gemini", "GEMINI.md"))
		want := []string{"--include-directories=" + e.web, "--include-directories=" + e.sdk, "--include-directories=" + filepath.Dir(ctx), "--flag", "a prompt with spaces"}
		if !slices.Equal(rec.Args, want) {
			t.Errorf("got %q\nwant %q", rec.Args, want)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		rec, err := e.open(t, append([]string{"payments", "--agent", "opencode"}, forwarded...)...)
		if err != nil {
			t.Fatal(err)
		}
		ctx := generated(t, rec, "workspace.md")
		var cfg struct {
			Instructions []string
			Permission   struct {
				ExternalDirectory map[string]string `json:"external_directory"`
			}
		}
		if err := json.Unmarshal([]byte(rec.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cfg.Instructions, []string{ctx, filepath.Join(e.web, "AGENTS.md")}) {
			t.Errorf("instructions %q", cfg.Instructions)
		}
		if cfg.Permission.ExternalDirectory[e.web+"/*"] != "allow" || cfg.Permission.ExternalDirectory[e.sdk+"/*"] != "allow" {
			t.Errorf("permissions %v", cfg.Permission.ExternalDirectory)
		}
		if !slices.Equal(rec.Args, forwarded[1:]) {
			t.Errorf("args %q", rec.Args)
		}
	})

	t.Run("pi", func(t *testing.T) {
		rec, err := e.open(t, append([]string{"payments", "--agent", "pi"}, forwarded...)...)
		if err != nil {
			t.Fatal(err)
		}
		ctx := generated(t, rec, "workspace.md")
		want := []string{"--append-system-prompt", ctx, "--append-system-prompt", filepath.Join(e.web, "AGENTS.md"), "--flag", "a prompt with spaces"}
		if !slices.Equal(rec.Args, want) {
			t.Errorf("got %q\nwant %q", rec.Args, want)
		}
	})
}

func TestE2EExitCodeAndMissingAgent(t *testing.T) {
	e := setupE2E(t)
	t.Setenv("POLYROOT_FAKE_AGENT_EXIT", "3")
	_, err := e.open(t, "payments")
	var exit ExitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("want agent exit code 3, got %v", err)
	}

	if err := os.Remove(filepath.Join(e.root, "bin", "pi")); err != nil {
		t.Fatal(err)
	}
	_, err = run(t, "open", "payments", "--agent", "pi")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("want not-installed error, got %v", err)
	}
	// An unrelated missing agent never affects others.
	t.Setenv("POLYROOT_FAKE_AGENT_EXIT", "0")
	if _, err := e.open(t, "payments", "--agent", "claude"); err != nil {
		t.Fatal(err)
	}
}

func TestE2EAgentsAndDoctor(t *testing.T) {
	setupE2E(t)
	out, err := run(t, "agents")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "pi"} {
		if !strings.Contains(out, name) {
			t.Errorf("agents output lacks %s:\n%s", name, out)
		}
	}
	// Fake agents print no real --help, so doctor flags them; config and
	// workspace checks must still pass and be reported.
	out, _ = run(t, "doctor")
	if !strings.Contains(out, "✓ payments") || !strings.Contains(out, "parses and all references resolve") {
		t.Errorf("doctor:\n%s", out)
	}
}

func TestE2EBuiltinOverride(t *testing.T) {
	e := setupE2E(t)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(e.root, "bin", "claude-wrapper")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(e.root, "cfg", "config.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`agents:
  claude:
    command: claude-wrapper
    args: [--dangerously-skip-permissions]
    env: {E2E_MARK: overridden}
  claude-opus:
    extends: claude
    args: [--model, opus]
`)
	_ = f.Close()

	rec, err := e.open(t, "payments", "--", "hi")
	if err != nil {
		t.Fatal(err)
	}
	tail := rec.Args[len(rec.Args)-2:]
	if rec.Name != "claude-wrapper" || !slices.Equal(tail, []string{"--dangerously-skip-permissions", "hi"}) || rec.Env["E2E_MARK"] != "overridden" {
		t.Errorf("override not applied: %s %q %v", rec.Name, rec.Args, rec.Env)
	}
	if !slices.Contains(rec.Args, "--add-dir") {
		t.Error("override must keep the built-in adapter's workspace flags")
	}

	rec, err = e.open(t, "payments", "--agent", "claude-opus")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.Args[len(rec.Args)-3:], []string{"--dangerously-skip-permissions", "--model", "opus"}) {
		t.Errorf("extends should inherit the claude override: %q", rec.Args)
	}
}
