package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kazaz-Or/polyroot/internal/config"
)

var builtins = []string{"claude", "codex"}

const handWritten = `# my polyroot config
version: 1
defaultAgent: claude # the one I use

repos:
  api: ~/git/api # main service
  sdk:
    path: ~/git/sdk

groups:
  platform:
    repos: [sdk]

workspaces:
  payments:
    primary: api
    groups: [platform] # shared stuff
    defaultAgent: codex

agents:
  claude-opus:
    extends: claude
    args: [--model, opus]
`

func TestEditPreservesHandWrittenContent(t *testing.T) {
	doc, err := LoadDocument([]byte(handWritten))
	if err != nil {
		t.Fatal(err)
	}
	doc.AddRepo("web", "~/git/web")
	doc.AddRepo("api", "/somewhere/else") // existing name: unchanged
	doc.SetWorkspace("payments", Workspace{Primary: "api", Repos: []string{"web"}})
	doc.SetWorkspace("gaming", Workspace{Primary: "web", Repos: []string{"sdk"}, Context: "contexts/gaming.md", DefaultAgent: "claude"})
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"# my polyroot config",
		"defaultAgent: claude # the one I use",
		"api: ~/git/api # main service",
		"path: ~/git/sdk",
		"web: ~/git/web",
		"groups: [platform] # shared stuff",
		"defaultAgent: codex",
		"repos: [web]",
		"extends: claude",
		"context: contexts/gaming.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/somewhere/else") {
		t.Error("AddRepo must not overwrite an existing repo")
	}
	cfg, err := config.Parse(out, "/tmp/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ws := cfg.Workspaces["payments"]; ws.Primary != "api" || len(ws.Groups) != 1 || ws.DefaultAgent != "codex" || ws.Repos[0] != "web" {
		t.Errorf("payments: %+v", ws)
	}
	if ws := cfg.Workspaces["gaming"]; ws.DefaultAgent != "claude" || ws.Context != "contexts/gaming.md" {
		t.Errorf("gaming: %+v", ws)
	}
}

func TestNewDocumentAndRemove(t *testing.T) {
	doc := NewDocument()
	doc.SetDefaultAgent("claude")
	doc.AddRepo("a", "/x/a")
	doc.SetWorkspace("w", Workspace{Primary: "a"})
	if !doc.RemoveWorkspace("w") || doc.RemoveWorkspace("w") {
		t.Fatal("RemoveWorkspace")
	}
	out, _ := doc.Bytes()
	if !strings.HasPrefix(string(out), "# Polyroot configuration") || !strings.Contains(string(out), "version: 1") {
		t.Fatalf("new document:\n%s", out)
	}
}

func TestEmptySectionsAreReplaced(t *testing.T) {
	doc, err := LoadDocument([]byte("version: 1\nrepos:\nworkspaces:\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc.AddRepo("a", "/x")
	out, _ := doc.Bytes()
	if !strings.Contains(string(out), "repos:\n  a: /x") {
		t.Fatalf("null section not replaced:\n%s", out)
	}
}

func TestSaveValidatesAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := NewDocument()
	bad.SetWorkspace("w", Workspace{Primary: "missing-repo"})
	if err := bad.Save(file, builtins); err == nil {
		t.Fatal("invalid config must not be written")
	}
	if data, _ := os.ReadFile(file); string(data) != "version: 1\n" {
		t.Fatal("file changed after a refused save")
	}
	good := NewDocument()
	good.SetDefaultAgent("claude")
	if err := good.Save(file, builtins); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file + ".bak"); string(data) != "version: 1\n" {
		t.Fatalf("backup: %q", data)
	}
}

func TestRepoNameAndDisplayPath(t *testing.T) {
	taken := map[string]string{"api": "/a/api"}
	for path, want := range map[string]string{
		"/a/api": "api",   // same path keeps its name
		"/b/web": "web",   // free name
		"/b/api": "b-api", // clash: parent-prefixed
	} {
		if got := RepoName(path, taken); got != want {
			t.Errorf("RepoName(%s) = %s, want %s", path, got, want)
		}
	}
	taken["b-api"] = "/c/b/api"
	if got := RepoName("/b/api", taken); got != "api-2" {
		t.Errorf("double clash: %s", got)
	}
	if DisplayPath("/home/me/git/x", "/home/me") != "~/git/x" || DisplayPath("/opt/x", "/home/me") != "/opt/x" {
		t.Error("DisplayPath")
	}
}
