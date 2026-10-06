package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

func testWorkspace(t *testing.T) *workspace.Resolved {
	t.Helper()
	root := t.TempDir()
	repo := func(name string, instr ...string) workspace.Repo {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return workspace.Repo{Name: name, Path: p, Instructions: instr}
	}
	return &workspace.Resolved{Name: "payments", Repos: []workspace.Repo{
		repo("payments api"),
		repo("payments-web", "AGENTS.md", "CLAUDE.md"),
		repo("shared-sdk", "CLAUDE.md"),
		repo("helm"),
	}}
}

const runDir = "/cache/polyroot/run/abc"

func paths(ws *workspace.Resolved, flag string) []string {
	var out []string
	for _, r := range ws.Additional() {
		out = append(out, flag, r.Path)
	}
	return out
}

func envMap(spec *LaunchSpec) map[string]string {
	m := map[string]string{}
	for _, e := range spec.Env {
		m[e.Key] = e.Value
	}
	return m
}

func TestClaude(t *testing.T) {
	ws := testWorkspace(t)
	t.Setenv(claudeMDEnv, "") // restores the original on cleanup
	_ = os.Unsetenv(claudeMDEnv)
	spec, err := Claude{}.Build(ws, []string{"--model", "opus", "fix it"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	want := append(paths(ws, "--add-dir"), "--append-system-prompt-file", runDir+"/workspace.md", "--model", "opus", "fix it")
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args:\n got %q\nwant %q", spec.Args, want)
	}
	if spec.Dir != ws.Primary().Path || spec.Command != "claude" || spec.RunDir != runDir {
		t.Fatalf("spec: %+v", spec)
	}
	if envMap(spec)[claudeMDEnv] != "1" {
		t.Error("expected CLAUDE.md loading env")
	}
	if len(spec.Files) != 1 || !strings.Contains(string(spec.Files[0].Content), "# Polyroot Workspace: payments") {
		t.Errorf("files: %+v", spec.Files)
	}

	// An explicit user choice is respected.
	t.Setenv(claudeMDEnv, "0")
	spec, _ = Claude{}.Build(ws, nil, runDir)
	if _, ok := envMap(spec)[claudeMDEnv]; ok {
		t.Error("must not override user's " + claudeMDEnv)
	}
}

func TestCodex(t *testing.T) {
	ws := testWorkspace(t)
	spec, err := Codex{}.Build(ws, []string{"exec", "hi"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	n := len(ws.Additional()) * 2
	if !reflect.DeepEqual(spec.Args[:n], paths(ws, "--add-dir")) {
		t.Fatalf("add-dir: %q", spec.Args[:n])
	}
	if spec.Args[n] != "-c" || !strings.HasPrefix(spec.Args[n+1], `developer_instructions="# Polyroot Workspace: payments\n`) {
		t.Fatalf("config override: %q", spec.Args[n:n+2])
	}
	if !reflect.DeepEqual(spec.Args[n+2:], []string{"exec", "hi"}) {
		t.Fatalf("forwarded: %q", spec.Args[n+2:])
	}
	if len(spec.Files) != 0 || spec.RunDir != "" {
		t.Error("codex needs no generated files")
	}
}

func TestTOMLString(t *testing.T) {
	got := tomlString("a \"q\" \\ \n\t\r\x01 é")
	want := `"a \"q\" \\ \n\t\r\u0001 é"`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	// TOML basic-string escapes are a subset of JSON's; round-trip via JSON.
	var back string
	if err := json.Unmarshal([]byte(got), &back); err != nil || back != "a \"q\" \\ \n\t\r\x01 é" {
		t.Fatalf("round trip: %q %v", back, err)
	}
}

func TestGemini(t *testing.T) {
	ws := testWorkspace(t)
	spec, err := Gemini{}.Build(ws, []string{"-m", "pro", "hello"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range ws.Additional() {
		want = append(want, "--include-directories="+r.Path)
	}
	want = append(want, "--include-directories="+runDir+"/gemini", "-m", "pro", "hello")
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args:\n got %q\nwant %q", spec.Args, want)
	}
	if len(spec.Files) != 1 || spec.Files[0].Path != runDir+"/gemini/GEMINI.md" || len(spec.Env) != 0 {
		t.Errorf("spec: %+v", spec)
	}
}

func TestGeminiRejectsCommaPaths(t *testing.T) {
	ws := testWorkspace(t)
	ws.Repos[2].Path = "/src/a,b"
	if _, err := (Gemini{}).Build(ws, nil, runDir); err == nil || !strings.Contains(err.Error(), "commas") {
		t.Fatalf("expected comma error, got %v", err)
	}
}

func TestGeminiSandboxLimit(t *testing.T) {
	ws := testWorkspace(t)
	for i := range 4 {
		ws.Repos = append(ws.Repos, workspace.Repo{Name: fmt.Sprint(i), Path: fmt.Sprintf("/src/%d", i)})
	}
	_, err := Gemini{}.Build(ws, []string{"--sandbox"}, runDir)
	if runtime.GOOS == "darwin" && err == nil {
		t.Fatal("expected sandbox root-limit error on macOS")
	}
	if runtime.GOOS != "darwin" && err != nil {
		t.Fatalf("limit only applies to macOS seatbelt: %v", err)
	}
	if !hasSandboxFlag([]string{"-s"}) || hasSandboxFlag([]string{"--sandbox=false"}) || hasSandboxFlag([]string{"--", "-s"}) {
		t.Error("hasSandboxFlag")
	}
}

func TestOpenCode(t *testing.T) {
	ws := testWorkspace(t)
	t.Setenv(openCodeConfigEnv, "")
	_ = os.Unsetenv(openCodeConfigEnv)
	spec, err := OpenCode{}.Build(ws, []string{"run", "hi"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Args, []string{"run", "hi"}) || spec.Dir != ws.Primary().Path {
		t.Fatalf("spec: %+v", spec)
	}
	var cfg struct {
		Instructions []string
		Permission   struct {
			ExternalDirectory map[string]string `json:"external_directory"`
		}
	}
	if err := json.Unmarshal([]byte(envMap(spec)[openCodeConfigEnv]), &cfg); err != nil {
		t.Fatal(err)
	}
	wantInstr := []string{runDir + "/workspace.md", filepath.Join(ws.Repos[1].Path, "AGENTS.md"), filepath.Join(ws.Repos[2].Path, "CLAUDE.md")}
	if !reflect.DeepEqual(cfg.Instructions, wantInstr) {
		t.Errorf("instructions: %q", cfg.Instructions)
	}
	if len(cfg.Permission.ExternalDirectory) != 3 || cfg.Permission.ExternalDirectory[ws.Repos[3].Path+"/*"] != "allow" {
		t.Errorf("permissions: %v", cfg.Permission.ExternalDirectory)
	}

	t.Setenv(openCodeConfigEnv, `{"x":1}`)
	if _, err := (OpenCode{}).Build(ws, nil, runDir); err == nil {
		t.Error("must refuse to overwrite user's " + openCodeConfigEnv)
	}
}

func TestPi(t *testing.T) {
	ws := testWorkspace(t)
	spec, err := Pi{}.Build(ws, []string{"-p", "hi"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--append-system-prompt", runDir + "/workspace.md",
		"--append-system-prompt", filepath.Join(ws.Repos[1].Path, "AGENTS.md"),
		"--append-system-prompt", filepath.Join(ws.Repos[2].Path, "CLAUDE.md"),
		"-p", "hi",
	}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args:\n got %q\nwant %q", spec.Args, want)
	}
}

func TestAliasAndCustom(t *testing.T) {
	ws := testWorkspace(t)
	cfg := &config.Config{Agents: map[string]*config.Agent{
		"opus":  {Extends: "claude", Args: []string{"--model", "opus"}},
		"tool":  {Command: "/opt/tool", Args: []string{"--tui"}, AddDirFlag: "--dir", ContextFlag: "--rules", Env: map[string]string{"B": "2", "A": "1"}},
		"mono":  {Command: "mono"},
		"plain": {Command: "plain", AddDirFlag: "--dir"},
	}}
	opus, err := Lookup(cfg, "opus")
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := opus.Build(ws, []string{"hi"}, runDir)
	if spec.Agent != "opus" || !reflect.DeepEqual(spec.Args[len(spec.Args)-3:], []string{"--model", "opus", "hi"}) {
		t.Errorf("alias: %+v", spec)
	}

	tool, _ := Lookup(cfg, "tool")
	spec, err = tool.Build(ws, []string{"x"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{"--tui"}, paths(ws, "--dir")...), "--rules", runDir+"/workspace.md", "x")
	if !reflect.DeepEqual(spec.Args, want) || spec.Command != "/opt/tool" {
		t.Fatalf("custom args: %q", spec.Args)
	}
	if !reflect.DeepEqual(spec.Env, []EnvVar{{"A", "1"}, {"B", "2"}}) {
		t.Errorf("env: %v", spec.Env)
	}

	mono, _ := Lookup(cfg, "mono")
	if _, err := mono.Build(ws, nil, runDir); err == nil || !strings.Contains(err.Error(), "addDirFlag") {
		t.Errorf("custom agent without addDirFlag must not drop repos: %v", err)
	}
	plain, _ := Lookup(cfg, "plain")
	if spec, _ := plain.Build(ws, nil, runDir); len(spec.Warnings) == 0 || len(spec.Files) != 0 {
		t.Errorf("expected missing-context warning: %+v", spec)
	}
	if _, err := Lookup(cfg, "vim"); err == nil {
		t.Error("unknown agent")
	}
}

func TestHelpFixtures(t *testing.T) {
	for _, a := range Builtins() {
		data, err := os.ReadFile(filepath.Join("testdata", a.Name()+"-help.txt"))
		if err != nil {
			t.Fatal(err)
		}
		d := Detection{Path: "/bin/" + a.Name(), Help: string(data)}
		if m := d.MissingFlags(a.Info().RequiredFlags); len(m) > 0 {
			t.Errorf("%s: fixture lacks required flags %v", a.Name(), m)
		}
		if s := Status(a, d); s != "ready" && s != "warnings" {
			t.Errorf("%s: status %s", a.Name(), s)
		}
	}
	old := Detection{Path: "/bin/claude", Help: "Usage: claude [options]\n  --model <model>"}
	if Status(Claude{}, old) != "incompatible" || len(Claude{}.Check(old)) == 0 {
		t.Error("old help text should be incompatible")
	}
	if Status(Claude{}, Detection{}) != "not installed" {
		t.Error("not installed")
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain/path-1.2": "plain/path-1.2",
		"has space":      "'has space'",
		"it's":           `'it'\''s'`,
		"":               "''",
		"$HOME":          "'$HOME'",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestRun re-executes this test binary as a fake agent.
func TestRun(t *testing.T) {
	if os.Getenv("POLYROOT_FAKE_AGENT") == "1" {
		return
	}
	cwd := t.TempDir()
	run := filepath.Join(t.TempDir(), "run", "x")
	spec := &LaunchSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestFakeAgent", "--", "arg with space", "it's"},
		Dir:     cwd,
		Env:     []EnvVar{{"POLYROOT_FAKE_AGENT", "1"}, {"FAKE_FILE", filepath.Join(run, "ctx.md")}, {"FAKE_CWD", cwd}},
		Files:   []File{{Path: filepath.Join(run, "ctx.md"), Content: []byte("context")}},
		RunDir:  run,
	}
	code, err := Run(spec)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code %d, want 7 (fake agent checks failed?)", code)
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Error("run dir was not cleaned up")
	}

	spec.Command = "polyroot-no-such-agent"
	if _, err := Run(spec); err == nil {
		t.Error("expected not-installed error")
	}
}

func TestFakeAgent(t *testing.T) {
	if os.Getenv("POLYROOT_FAKE_AGENT") != "1" {
		t.Skip("helper process")
	}
	args := os.Args[len(os.Args)-2:]
	data, err := os.ReadFile(os.Getenv("FAKE_FILE"))
	wd, _ := os.Getwd()
	wd, _ = filepath.EvalSymlinks(wd)
	want, _ := filepath.EvalSymlinks(os.Getenv("FAKE_CWD"))
	if err == nil && string(data) == "context" && args[0] == "arg with space" && args[1] == "it's" && wd == want {
		os.Exit(7)
	}
	os.Exit(1)
}

// Optional integration tests: only run where the real agent is installed.
func TestInstalledAgents(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	for _, a := range Builtins() {
		t.Run(a.Name(), func(t *testing.T) {
			if _, err := exec.LookPath(a.Info().Binary); err != nil {
				t.Skip("not installed")
			}
			d := Detect(context.Background(), a.Info().Binary)
			if d.Version == "" {
				t.Errorf("no version parsed (err: %v)", d.Err)
			}
			if m := d.MissingFlags(a.Info().RequiredFlags); len(m) > 0 {
				t.Errorf("installed %s %s lacks %v", a.Name(), d.Version, m)
			}
		})
	}
}

func TestBuiltinOverride(t *testing.T) {
	ws := testWorkspace(t)
	cfg := &config.Config{Agents: map[string]*config.Agent{
		"claude": {
			Command: "/opt/bin/claude-wrapper",
			Args:    []string{"--dangerously-skip-permissions"},
			Env:     map[string]string{claudeMDEnv: "0", "EXTRA": "x"},
		},
		"claude-opus": {Extends: "claude", Args: []string{"--model", "opus"}},
		"codex-fast":  {Extends: "codex", Command: "codex-nightly"},
	}}
	t.Setenv(claudeMDEnv, "")
	_ = os.Unsetenv(claudeMDEnv)

	claude, err := Lookup(cfg, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if claude.Info().Binary != "/opt/bin/claude-wrapper" {
		t.Errorf("detection must use the override binary: %q", claude.Info().Binary)
	}
	spec, err := claude.Build(ws, []string{"hi"}, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "/opt/bin/claude-wrapper" || spec.Agent != "claude" {
		t.Errorf("spec: %+v", spec)
	}
	// Polyroot flags first, then config args, then forwarded args.
	if tail := spec.Args[len(spec.Args)-2:]; !reflect.DeepEqual(tail, []string{"--dangerously-skip-permissions", "hi"}) {
		t.Errorf("args: %q", spec.Args)
	}
	if env := envMap(spec); env[claudeMDEnv] != "0" || env["EXTRA"] != "x" || len(spec.Env) != 2 {
		t.Errorf("config env must replace Polyroot's value, not duplicate it: %v", spec.Env)
	}

	// An agent extending claude inherits the claude override.
	opus, _ := Lookup(cfg, "claude-opus")
	spec, _ = opus.Build(ws, []string{"hi"}, runDir)
	if spec.Agent != "claude-opus" || spec.Command != "/opt/bin/claude-wrapper" ||
		!reflect.DeepEqual(spec.Args[len(spec.Args)-4:], []string{"--dangerously-skip-permissions", "--model", "opus", "hi"}) {
		t.Errorf("extends: %s %q", spec.Command, spec.Args)
	}

	fast, _ := Lookup(cfg, "codex-fast")
	if spec, _ := fast.Build(ws, nil, runDir); spec.Command != "codex-nightly" {
		t.Errorf("extends with command: %q", spec.Command)
	}

	names := []string{}
	for _, a := range All(cfg) {
		names = append(names, a.Name())
	}
	if !reflect.DeepEqual(names, []string{"claude", "codex", "gemini", "opencode", "pi", "claude-opus", "codex-fast"}) {
		t.Errorf("All: %v", names)
	}
}
