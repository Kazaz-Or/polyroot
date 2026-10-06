package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeRepos creates directories under a temp root and returns the root.
func writeRepos(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real // macOS: /var -> /private/var
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func parse(t *testing.T, root, yaml string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(yaml), filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func mustCheck(t *testing.T, cfg *Config) {
	t.Helper()
	if errs := cfg.Check([]string{"claude", "codex"}); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func checkHas(t *testing.T, cfg *Config, want string) {
	t.Helper()
	for _, e := range cfg.Check([]string{"claude", "codex"}) {
		if strings.Contains(e.Error(), want) {
			return
		}
	}
	t.Fatalf("expected an error containing %q, got %v", want, cfg.Check([]string{"claude", "codex"}))
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("PR_TEST_DIR", "/opt/src")
	cases := map[string]string{
		"~":                      home,
		"~/git/x":                filepath.Join(home, "git/x"),
		"$HOME/git/x":            filepath.Join(home, "git/x"),
		"${HOME}/git/x":          filepath.Join(home, "git/x"),
		"${PR_TEST_DIR}/a b":     "/opt/src/a b",
		"$PR_TEST_DIR/../other/": "/opt/other",
		"/abs/./path//x":         "/abs/path/x",
		"relative/dir":           "/base/relative/dir",
	}
	for in, want := range cases {
		got, err := ExpandPath(in, "/base")
		if err != nil || got != want {
			t.Errorf("ExpandPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "$PR_TEST_UNSET_VAR/x", "~someone/x"} {
		if _, err := ExpandPath(bad, "/base"); err == nil {
			t.Errorf("ExpandPath(%q) should fail", bad)
		}
	}
}

func TestRepoPath(t *testing.T) {
	root := writeRepos(t, "with space/repo", "plain")
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "plain"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	if p, err := RepoPath(filepath.Join(root, "with space/repo"), "/"); err != nil || p != filepath.Join(root, "with space/repo") {
		t.Errorf("space path: %q %v", p, err)
	}
	if p, err := RepoPath(filepath.Join(root, "link"), "/"); err != nil || p != filepath.Join(root, "plain") {
		t.Errorf("symlink not canonicalized: %q %v", p, err)
	}
	if _, err := RepoPath(filepath.Join(root, "missing"), "/"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing: %v", err)
	}
	if _, err := RepoPath(filepath.Join(root, "file"), "/"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file: %v", err)
	}
	home, _ := os.UserHomeDir()
	for _, unsafe := range []string{"/", "~", "$HOME", filepath.Dir(home)} {
		if _, err := RepoPath(unsafe, "/"); err == nil {
			t.Errorf("RepoPath(%q) should be rejected", unsafe)
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for name, yaml := range map[string]string{
		"version":       "version: 2\n",
		"no version":    "repos: {}\n",
		"unknown field": "version: 1\nrepoz: {}\n",
		"dup repo":      "version: 1\nrepos:\n  a: /x\n  a: /y\n",
		"dup workspace": "version: 1\nworkspaces:\n  w: {primary: a}\n  w: {primary: a}\n",
	} {
		if _, err := Parse([]byte(yaml), "/tmp/config.yaml"); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

func TestRepoShorthandAndRelativePaths(t *testing.T) {
	root := writeRepos(t, "git/a", "git/b")
	cfg := parse(t, root, `
version: 1
repos:
  a: git/a
  b:
    path: ./git/b
workspaces:
  w: {primary: a, repos: [b]}
`)
	mustCheck(t, cfg)
	if cfg.Repos["a"].Path != filepath.Join(root, "git/a") || cfg.Repos["b"].Path != filepath.Join(root, "git/b") {
		t.Fatalf("paths: %q %q", cfg.Repos["a"].Path, cfg.Repos["b"].Path)
	}
}

func TestCheckReferences(t *testing.T) {
	root := writeRepos(t, "a", "b")
	cfg := parse(t, root, `
version: 1
defaultAgent: nope
repos:
  a: a
  b: b
  b2: b/../b
groups:
  g1: {repos: [a, ghost], groups: [missing-group]}
workspaces:
  w1: {repos: [a]}
  w2: {primary: ghost}
  w3: {primary: a, groups: [nogroup], defaultAgent: other, context: ctx/missing.md}
agents:
  claude: {extends: codex}
  codex: {addDirFlag: --dir}
  neither: {}
  badbase: {extends: vim}
  badenv: {command: x, env: {"A=B": "1"}}
`)
	for _, want := range []string{
		`defaultAgent "nope"`,
		`unknown repo "ghost"`,
		`unknown group "missing-group"`,
		`workspace "w1" has no`,
		`primary "ghost"`,
		`unknown group "nogroup"`,
		`defaultAgent "other"`,
		`context file ctx/missing.md`,
		`"claude" is built-in; override it`,
		`"codex": addDirFlag and contextFlag only apply`,
		`invalid environment variable name "A=B"`,
		`"neither" needs`,
		`extends "vim"`,
		`point to the same directory`,
	} {
		checkHas(t, cfg, want)
	}
}

func TestCheckGroupCycle(t *testing.T) {
	root := writeRepos(t, "a")
	cfg := parse(t, root, `
version: 1
repos: {a: a}
groups:
  x: {groups: [y]}
  y: {groups: [z]}
  z: {groups: [x]}
`)
	checkHas(t, cfg, "cycle: x -> y -> z -> x")
}

func TestMissingRepoPathIsNotAStructuralError(t *testing.T) {
	root := writeRepos(t, "a")
	cfg := parse(t, root, "version: 1\nrepos: {a: a, gone: gone}\nworkspaces: {w: {primary: a}}\n")
	mustCheck(t, cfg) // reported by the resolver, per workspace
	if cfg.Repos["gone"].PathErr == nil {
		t.Fatal("expected PathErr for missing repo")
	}
}

func TestAgentPrecedence(t *testing.T) {
	cfg := &Config{DefaultAgent: "claude", Workspaces: map[string]*Workspace{
		"w":     {DefaultAgent: "codex"},
		"plain": {},
	}}
	for _, c := range []struct{ ws, flag, want string }{
		{"w", "pi", "pi"},
		{"w", "", "codex"},
		{"plain", "", "claude"},
	} {
		if got, _ := cfg.AgentFor(c.ws, c.flag); got != c.want {
			t.Errorf("AgentFor(%q, %q) = %q, want %q", c.ws, c.flag, got, c.want)
		}
	}
	cfg.DefaultAgent = ""
	if _, err := cfg.AgentFor("plain", ""); err == nil {
		t.Error("expected error when no agent is configured")
	}
}

func TestDefaultDirs(t *testing.T) {
	t.Setenv("POLYROOT_CONFIG_HOME", "/custom/cfg")
	t.Setenv("POLYROOT_CACHE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	d, err := DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if d.Config != "/custom/cfg" || d.Cache != "/xdg/cache/polyroot" || d.File() != "/custom/cfg/config.yaml" {
		t.Fatalf("%+v", d)
	}
	t.Setenv("POLYROOT_CONFIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "relative") // ignored per XDG spec
	home, _ := os.UserHomeDir()
	if d, _ := DefaultDirs(); d.Config != filepath.Join(home, ".config/polyroot") {
		t.Fatalf("config dir %q", d.Config)
	}
}

func TestExampleConfig(t *testing.T) {
	data, err := os.ReadFile("../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, "../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if errs := cfg.Check([]string{"claude", "codex", "gemini", "opencode", "pi"}); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestAgentOverrides(t *testing.T) {
	root := writeRepos(t, "a")
	cfg := parse(t, root, `
version: 1
defaultAgent: claude
repos: {a: a}
workspaces: {w: {primary: a}}
agents:
  claude:                       # override a built-in
    command: ~/bin/claude-wrapper
    args: [--dangerously-skip-permissions]
    env: {CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD: "0"}
  codex: {args: [--search]}
  yolo: {extends: claude, command: ./bin/claude, args: [--model, opus]}
  plain: {command: my-agent}
`)
	mustCheck(t, cfg)
	home, _ := os.UserHomeDir()
	if got := cfg.Agents["claude"].Command; got != filepath.Join(home, "bin/claude-wrapper") {
		t.Errorf("~ not expanded in command: %q", got)
	}
	if got := cfg.Agents["yolo"].Command; got != filepath.Join(root, "bin/claude") {
		t.Errorf("relative command not resolved against config dir: %q", got)
	}
	if got := cfg.Agents["plain"].Command; got != "my-agent" {
		t.Errorf("bare command must stay a PATH lookup: %q", got)
	}
	if _, err := Parse([]byte("version: 1\nagents: {x: {command: $POLYROOT_NOPE/x}}\n"), filepath.Join(root, "c.yaml")); err == nil {
		t.Error("unset variable in command must fail")
	}
}

func TestResolveRepo(t *testing.T) {
	root := writeRepos(t, "git/api", "git/web", "git/shared", "work/shared", "work/helm", "here/local", "outside/x")
	t.Setenv("PR_TEST_ROOT", root)
	for _, r := range []string{"git/api", "git/web", "work/helm"} {
		if err := os.MkdirAll(filepath.Join(root, r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := parse(t, root, `
version: 1
repoDirs: [git, $PR_TEST_ROOT/work, missing-dir]
repos:
  registered: outside/x
`)
	cwd := filepath.Join(root, "here")
	cases := []struct{ ref, wantName, wantPath, wantErr string }{
		{"registered", "registered", root + "/outside/x", ""},        // 1. registered name
		{"api", "", root + "/git/api", ""},                           // 2. folder in a repo dir
		{"helm", "", root + "/work/helm", ""},                        // 2. second repo dir
		{"local", "", root + "/here/local", ""},                      // 3. relative to cwd
		{"./local", "", root + "/here/local", ""},                    // explicit path
		{root + "/outside/x", "registered", root + "/outside/x", ""}, // path of a registered repo
		{"shared", "", "", "several repo directories"},               // ambiguous
		{"wbe", "", "", `did you mean "web"`},                        // typo
		{"nothing-like-it", "", "", "no repository"},
	}
	for _, c := range cases {
		name, path, err := cfg.ResolveRepo(c.ref, cwd)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: want error %q, got %v", c.ref, c.wantErr, err)
			}
			continue
		}
		if err != nil || name != c.wantName || path != c.wantPath {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q)", c.ref, name, path, err, c.wantName, c.wantPath)
		}
	}
	if got := cfg.MissingRepoDirs(); len(got) != 1 || !strings.HasSuffix(got[0], "missing-dir") {
		t.Errorf("MissingRepoDirs: %v", got)
	}
	// Candidates: registered names plus every folder directly inside repoDirs,
	// Git or not ("shared" is in two dirs and listed once).
	if got := cfg.RepoCandidates(); !slices.Equal(got, []string{"api", "helm", "registered", "shared", "web"}) {
		t.Errorf("candidates: %v", got)
	}
}

func TestRepoDirsUnsetVariable(t *testing.T) {
	root := writeRepos(t)
	cfg := parse(t, root, "version: 1\nrepoDirs: [$PR_TEST_NOT_SET/x]\n")
	checkHas(t, cfg, "repoDirs entry")
}
