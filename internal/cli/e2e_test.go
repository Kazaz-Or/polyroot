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

// TestE2EUserFlow walks the documented first-run flow with real commands:
// setup, workspace add, `polyroot <workspace>`, a hand edit, an update and a
// removal.
func TestE2EUserFlow(t *testing.T) {
	e := setupE2E(t)
	cfgFile := filepath.Join(e.root, "cfg", "config.yaml")
	if err := os.Remove(cfgFile); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "workspace", "add", "shop", e.api); err == nil || !strings.Contains(err.Error(), "polyroot setup") {
		t.Fatalf("workspace add before setup should point to setup: %v", err)
	}

	out, err := run(t, "setup", "--agent", "claude")
	if err != nil || !strings.Contains(out, "Created") {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, _ := run(t, "setup", "--agent", "codex"); !strings.Contains(out, "already set up") {
		t.Fatalf("second setup must not change anything:\n%s", out)
	}

	// Paths with spaces, relative to the current directory.
	t.Chdir(filepath.Dir(e.api))
	out, err = run(t, "workspace", "add", "shop", "payments api", e.web)
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	if !strings.Contains(out, `Created workspace "shop": primary payments api, plus web`) {
		t.Errorf("add output:\n%s", out)
	}

	// polyroot <workspace> opens it.
	rec, err := e.open(t, "shop", "--", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "claude" || rec.Cwd != e.api || !slices.Equal(rec.Args[:2], []string{"--add-dir", e.web}) || rec.Args[len(rec.Args)-1] != "hi" {
		t.Errorf("shorthand open: %s %q in %s", rec.Name, rec.Args, rec.Cwd)
	}
	_ = os.Remove(e.record)
	if _, err := run(t, "-q", "shop", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}

	// Hand edits survive later changes.
	data, _ := os.ReadFile(cfgFile)
	edited := strings.Replace(string(data), "workspaces:", "# my note\nworkspaces:", 1)
	if err := os.WriteFile(cfgFile, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "workspace", "add", "shop", e.sdk, "--agent", "pi")
	if err != nil || !strings.Contains(out, "Updated workspace") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	data, _ = os.ReadFile(cfgFile)
	if !strings.Contains(string(data), "# my note") || !strings.Contains(string(data), "defaultAgent: pi") {
		t.Errorf("hand edit lost or agent not set:\n%s", data)
	}
	if _, err := os.Stat(cfgFile + ".bak"); err != nil {
		t.Error("no backup written")
	}
	out, _ = run(t, "show", "shop")
	if !strings.Contains(out, "shared sdk") {
		t.Errorf("sdk not added:\n%s", out)
	}

	// The same physical repo is reused, not registered twice.
	if _, err := run(t, "workspace", "add", "other", e.web, e.api); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgFile)
	if strings.Count(string(data), "/src/web") != 1 {
		t.Errorf("repo registered twice:\n%s", data)
	}

	for _, bad := range [][]string{
		{"workspace", "add", "list", e.api},          // shadows a command
		{"workspace", "add", "bad name", e.api},      // invalid name
		{"workspace", "add", "x", "/does/not/exist"}, // missing path
		{"workspace", "add", "x", "/"},               // unsafe root
	} {
		if _, err := run(t, bad...); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}

	if _, err := run(t, "workspace", "remove", "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "show", "shop"); err == nil {
		t.Error("removed workspace still exists")
	}
	if _, err := run(t, "nosuchworkspace"); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Errorf("unknown workspace via shorthand: %v", err)
	}
}

func TestHelpListsEverything(t *testing.T) {
	setupE2E(t)
	out, err := run(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setup", "workspace add", "workspace remove", "open", "list", "show", "command", "validate", "agents", "doctor", "--agent", "POLYROOT_CONFIG_HOME"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// TestE2ERepoDirsAndCompletion: setup with repo directories, workspaces by
// folder name, and shell completion through cobra's __complete entry point.
func TestE2ERepoDirsAndCompletion(t *testing.T) {
	e := setupE2E(t)
	src := filepath.Dir(e.api)
	for _, d := range []string{e.api, e.web, e.sdk} {
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(e.root, "cfg", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "setup", "--agent", "claude", "--repo-dir", src); err != nil || !strings.Contains(out, "repo directories:") {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	t.Chdir(e.root) // folder names must not depend on the current directory
	if out, err := run(t, "workspace", "add", "shop", "payments api", "web", "shared sdk"); err != nil {
		t.Fatalf("add by folder name: %v\n%s", err, out)
	}
	rec, err := e.open(t, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Cwd != e.api || !slices.Equal(rec.Args[:4], []string{"--add-dir", e.web, "--add-dir", e.sdk}) {
		t.Errorf("launch: %q in %s", rec.Args, rec.Cwd)
	}
	if _, err := run(t, "workspace", "add", "shop", "wbe"); err == nil || !strings.Contains(err.Error(), `did you mean "web"`) {
		t.Errorf("typo hint: %v", err)
	}

	complete := func(args ...string) []string {
		out, err := run(t, append([]string{"__complete"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var items []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" && !strings.HasPrefix(l, ":") && !strings.HasPrefix(l, "Completion ended") {
				items = append(items, strings.SplitN(l, "\t", 2)[0])
			}
		}
		return items
	}
	if got := complete("workspace", "add", "shop", "w"); !slices.Equal(got, []string{"web"}) {
		t.Errorf("repo completion: %v", got)
	}
	if got := complete("workspace", "add", "shop", "web", ""); slices.Contains(got, "web") || !slices.Contains(got, "shared sdk") {
		t.Errorf("repo completion should skip repos already typed: %v", got)
	}
	if got := complete("sh"); !slices.Contains(got, "shop") || !slices.Contains(got, "show") {
		t.Errorf("root completion (workspaces + commands): %v", got)
	}
	if got := complete("open", "--agent", "co"); !slices.Equal(got, []string{"codex"}) {
		t.Errorf("agent completion: %v", got)
	}
	if got := complete("workspace", "remove", ""); !slices.Equal(got, []string{"shop"}) {
		t.Errorf("remove completion: %v", got)
	}

	out, _ := run(t, "doctor")
	if !strings.Contains(out, "✓ repo directory "+src) {
		t.Errorf("doctor should list repo directories:\n%s", out)
	}
}
