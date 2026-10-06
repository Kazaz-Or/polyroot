package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveRepo turns a user-supplied repository reference into a canonical
// path. It tries, in order:
//
//  1. a repository already registered under that name;
//  2. a directory of that name inside one of RepoDirs;
//  3. a path relative to cwd (or absolute, or starting with ~).
//
// References that are explicit paths (/x, ~/x, ./x, ../x, .) skip 1 and 2.
// name is set when the reference matched a registered repository.
func (c *Config) ResolveRepo(ref, cwd string) (name, path string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty repository reference")
	}
	explicit := ref == "." || ref == ".." || filepath.IsAbs(ref) ||
		strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") ||
		strings.HasPrefix(ref, "$")
	if !explicit {
		if r := c.Repos[ref]; r != nil {
			if r.PathErr != nil {
				return "", "", fmt.Errorf("repo %q (%s): %w", ref, r.Raw, r.PathErr)
			}
			return ref, r.Path, nil
		}
		var hits []string
		for _, d := range c.RepoDirPaths {
			if p := filepath.Join(d, ref); isDir(p) {
				hits = append(hits, p)
			}
		}
		switch {
		case len(hits) == 1:
			path, err = RepoPath(hits[0], cwd)
			return c.registeredName(path), path, err
		case len(hits) > 1:
			return "", "", fmt.Errorf("%q exists in several repo directories: %s; pass the full path of the one you mean", ref, strings.Join(hits, ", "))
		}
		if !isDir(filepath.Join(cwd, ref)) {
			return "", "", c.notFound(ref)
		}
	}
	path, err = RepoPath(ref, cwd)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", ref, err)
	}
	return c.registeredName(path), path, nil
}

func (c *Config) registeredName(path string) string {
	for n, r := range c.Repos {
		if r.Path == path {
			return n
		}
	}
	return ""
}

func (c *Config) notFound(ref string) error {
	where := "your repoDirs"
	if len(c.RepoDirPaths) > 0 {
		where = strings.Join(c.RepoDirPaths, ", ")
	}
	msg := fmt.Sprintf("no repository %q: not a registered repo, not found in %s, and not a directory here", ref, where)
	if len(c.RepoDirPaths) == 0 {
		msg += " (add `repoDirs:` to the config to refer to repositories by folder name)"
	}
	if s := closest(ref, c.RepoCandidates()); s != "" {
		msg += fmt.Sprintf("; did you mean %q?", s)
	}
	return fmt.Errorf("%s", msg)
}

// RepoCandidates returns names a user can type: registered repos plus the
// directories directly inside RepoDirs (Git or not). Used for completion and hints.
func (c *Config) RepoCandidates() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range SortedKeys(c.Repos) {
		add(n)
	}
	for _, d := range c.RepoDirPaths {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				add(e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// closest returns the candidate nearest to s by edit distance, if close enough.
func closest(s string, candidates []string) string {
	best, bestD := "", 3 // ponytail: fixed threshold; fine for repo-name typos
	for _, c := range candidates {
		if d := editDistance(strings.ToLower(s), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
