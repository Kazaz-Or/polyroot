// Package workspace resolves a logical workspace into its ordered set of
// physical repositories. It is agent-agnostic: adapters consume Resolved.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kazaz-Or/polyroot/internal/config"
)

// Repo is one repository in a resolved workspace.
type Repo struct {
	Name string
	Path string // absolute, canonical
	// Via is the group that pulled the repo in ("" when listed directly or primary).
	Via string
	// Instructions are repo-owned instruction files found at the repo root.
	Instructions []string
}

// Resolved is a fully resolved workspace. Repos[0] is always the primary.
type Resolved struct {
	Name    string
	Repos   []Repo
	Context string // absolute path of the workspace context file, or ""
}

// Primary returns the primary repository.
func (r *Resolved) Primary() Repo { return r.Repos[0] }

// Additional returns every repository except the primary.
func (r *Resolved) Additional() []Repo { return r.Repos[1:] }

// InstructionFileNames are the repo-owned instruction files Polyroot looks for.
var InstructionFileNames = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

// Members returns the workspace's repo names in resolution order without
// touching the filesystem: primary, then direct repos in listed order, then
// groups depth-first in listed order. Duplicates keep their first position.
// The second slice holds the group each repo came from.
// It assumes cfg.Check passed (no unknown names, no cycles).
func Members(cfg *config.Config, name string) (names, via []string, err error) {
	ws := cfg.Workspaces[name]
	if ws == nil {
		return nil, nil, fmt.Errorf("unknown workspace %q (see `polyroot list`)", name)
	}
	seen := map[string]bool{}
	addRepo := func(r, from string) {
		if !seen[r] {
			seen[r] = true
			names = append(names, r)
			via = append(via, from)
		}
	}
	addRepo(ws.Primary, "")
	for _, r := range ws.Repos {
		addRepo(r, "")
	}
	visited := map[string]bool{}
	var addGroup func(string)
	addGroup = func(g string) {
		if visited[g] || cfg.Groups[g] == nil {
			return
		}
		visited[g] = true
		for _, r := range cfg.Groups[g].Repos {
			addRepo(r, g)
		}
		for _, sub := range cfg.Groups[g].Groups {
			addGroup(sub)
		}
	}
	for _, g := range ws.Groups {
		addGroup(g)
	}
	return names, via, nil
}

// Resolve resolves a workspace and verifies every repository path.
// Any unusable repository fails the whole workspace: repos are never dropped.
func Resolve(cfg *config.Config, name string) (*Resolved, error) {
	names, via, err := Members(cfg, name)
	if err != nil {
		return nil, err
	}
	res := &Resolved{Name: name, Context: cfg.Workspaces[name].ContextPath}
	var problems []string
	for i, n := range names {
		repo := cfg.Repos[n]
		if repo == nil {
			return nil, fmt.Errorf("workspace %q references unknown repo %q; run `polyroot validate`", name, n)
		}
		if repo.PathErr != nil {
			shown := repo.Path
			if shown == "" {
				shown = repo.Raw
			}
			problems = append(problems, fmt.Sprintf("  %s: %s: %v", n, shown, repo.PathErr))
			continue
		}
		res.Repos = append(res.Repos, Repo{Name: n, Path: repo.Path, Via: via[i], Instructions: findInstructions(repo.Path)})
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("workspace %q has unusable repositories:\n%s\nfix the `path:` in %s or clone the repository there",
			name, strings.Join(problems, "\n"), cfg.File)
	}
	if res.Context != "" {
		if st, err := os.Stat(res.Context); err != nil || st.IsDir() {
			return nil, fmt.Errorf("workspace %q: context file %s is missing or not a file", name, res.Context)
		}
	}
	return res, nil
}

func findInstructions(dir string) []string {
	var found []string
	for _, f := range InstructionFileNames {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil && !st.IsDir() {
			found = append(found, f)
		}
	}
	return found
}

// IsGitRepo reports whether dir has a .git entry (directory or worktree file).
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Instructions renders the agent-neutral workspace map followed by the
// user's workspace context file, if any. It describes topology only; no
// source code is included.
func Instructions(r *Resolved) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Polyroot Workspace: %s\n\n", r.Name)
	b.WriteString("You are working in a logical multi-repository codebase.\n\n")
	b.WriteString("Primary repository (your working directory):\n\n")
	writeRepo(&b, r.Primary())
	if extra := r.Additional(); len(extra) > 0 {
		b.WriteString("\nAdditional repositories:\n\n")
		for _, repo := range extra {
			writeRepo(&b, repo)
		}
	}
	b.WriteString(`
Each repository is an independent Git repository. Run Git operations from the
appropriate repository root. A task may affect multiple repositories. When
investigating a product-level issue, search across the available repositories
when relevant. Files listed in brackets are repository-owned instructions;
read them before changing code in that repository if they are not already in
your context.
`)
	if r.Context != "" {
		data, err := os.ReadFile(r.Context)
		if err != nil {
			return "", fmt.Errorf("reading workspace context: %w", err)
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			b.WriteString("\n---\n\n")
			b.Write(data)
			if !strings.HasSuffix(string(data), "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String(), nil
}

func writeRepo(b *strings.Builder, r Repo) {
	fmt.Fprintf(b, "- %s: %s", r.Name, r.Path)
	if len(r.Instructions) > 0 {
		fmt.Fprintf(b, " [%s]", strings.Join(r.Instructions, ", "))
	}
	b.WriteString("\n")
}
