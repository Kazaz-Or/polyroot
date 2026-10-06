// Package config loads and validates the Polyroot YAML configuration.
//
// It knows about repositories, groups, workspaces and custom agent
// definitions, but nothing about how any coding agent is launched.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only config schema version this build understands.
const SchemaVersion = 1

// Config is the parsed configuration file.
type Config struct {
	Version      int                   `yaml:"version"`
	DefaultAgent string                `yaml:"defaultAgent"`
	Repos        map[string]*Repo      `yaml:"repos"`
	Groups       map[string]*Group     `yaml:"groups"`
	Workspaces   map[string]*Workspace `yaml:"workspaces"`
	Agents       map[string]*Agent     `yaml:"agents"`

	// File is the absolute path the config was loaded from.
	File string `yaml:"-"`
}

// Repo is a physical repository on disk, registered once.
type Repo struct {
	// Path as written in the config file.
	Raw string `yaml:"path"`

	// Path is the expanded, absolute and (when it exists) symlink-resolved path.
	Path string `yaml:"-"`
	// PathErr is set when the path is missing, unusable or unsafe.
	PathErr error `yaml:"-"`
}

// UnmarshalYAML accepts both `name: {path: ~/x}` and the shorthand `name: ~/x`.
func (r *Repo) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.Raw = n.Value
		return nil
	}
	type plain Repo
	return n.Decode((*plain)(r))
}

// Group is a reusable, named set of repositories and nested groups.
type Group struct {
	Repos  []string `yaml:"repos"`
	Groups []string `yaml:"groups"`
}

// Workspace is a logical codebase made of repositories and groups.
type Workspace struct {
	Primary      string   `yaml:"primary"`
	Repos        []string `yaml:"repos"`
	Groups       []string `yaml:"groups"`
	Context      string   `yaml:"context"`
	DefaultAgent string   `yaml:"defaultAgent"`

	// ContextPath is the expanded absolute path of Context ("" when unset).
	ContextPath string `yaml:"-"`
}

// Agent customizes how an agent is launched. There are three forms:
//
//   - a built-in name (claude, codex, ...): override its executable, default
//     arguments and environment;
//   - `extends: <built-in>`: a new name for a built-in with its own defaults;
//   - `command: <executable>`: a generic command adapter.
type Agent struct {
	// Extends names the built-in adapter a new agent name is based on.
	Extends string `yaml:"extends"`

	// Command is the executable (name in PATH or path). For built-ins and
	// extends it replaces the default binary, e.g. a wrapper script.
	Command string `yaml:"command"`
	// Args are passed on every launch, before arguments given after `--`.
	Args []string `yaml:"args"`
	// Env is added to the agent's environment (overrides Polyroot's values).
	Env map[string]string `yaml:"env"`

	// AddDirFlag (generic agents) is emitted once per additional repository: [flag, path].
	AddDirFlag string `yaml:"addDirFlag"`
	// ContextFlag (generic agents) receives the generated instructions file: [flag, file].
	ContextFlag string `yaml:"contextFlag"`
}

// Load reads and parses the config file at path. It expands repository and
// context paths but does not run structural validation; call Check for that.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config file at %s\n  run `polyroot setup` to create it, or write it by hand (all options: https://github.com/Kazaz-Or/polyroot/blob/master/examples/config.yaml)", path)
	}
	if err != nil {
		return nil, err
	}
	return Parse(data, path)
}

// Parse parses config data as if it had been read from file.
func Parse(data []byte, file string) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if cfg.Version != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported config version %d (this polyroot supports `version: %d`)", file, cfg.Version, SchemaVersion)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	cfg.File = abs
	dir := filepath.Dir(abs)

	for name, r := range cfg.Repos {
		if r == nil {
			return nil, fmt.Errorf("%s: repo %q has no path", file, name)
		}
		r.Path, r.PathErr = RepoPath(r.Raw, dir)
	}
	for name, a := range cfg.Agents {
		// A command with a path component (~/bin/x, ./x, /opt/x) is a file path;
		// a bare name is looked up in PATH at launch.
		if a != nil && (strings.HasPrefix(a.Command, "~") || strings.ContainsRune(a.Command, '/')) {
			p, err := ExpandPath(a.Command, dir)
			if err != nil {
				return nil, fmt.Errorf("%s: agent %q command: %w", file, name, err)
			}
			a.Command = p
		}
	}
	for _, ws := range cfg.Workspaces {
		if ws != nil && ws.Context != "" {
			ws.ContextPath, _ = ExpandPath(ws.Context, dir)
		}
	}
	return cfg, nil
}

// Dir is the directory containing the config file; relative paths resolve against it.
func (c *Config) Dir() string { return filepath.Dir(c.File) }

// Check validates references between repos, groups, workspaces and agents.
// builtinAgents lists the agent names provided by Polyroot itself.
// It does not fail on missing repository paths; those are reported per
// workspace by the resolver so one broken checkout doesn't block the rest.
func (c *Config) Check(builtinAgents []string) []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	builtin := map[string]bool{}
	for _, n := range builtinAgents {
		builtin[n] = true
	}
	knownAgent := func(n string) bool { return builtin[n] || c.Agents[n] != nil }

	if c.DefaultAgent != "" && !knownAgent(c.DefaultAgent) {
		add("defaultAgent %q is not a known agent (built-in: %s)", c.DefaultAgent, strings.Join(builtinAgents, ", "))
	}

	for _, name := range SortedKeys(c.Agents) {
		a := c.Agents[name]
		builtinName := strings.Join(builtinAgents, ", ")
		switch {
		case a == nil:
			add("agent %q is empty; set `args`, `extends` or `command`", name)
		case builtin[name] && a.Extends != "":
			add("agent %q is built-in; override it with `command`, `args` or `env` instead of `extends`", name)
		case a.Extends != "" && !builtin[a.Extends]:
			add("agent %q extends %q, which is not a built-in agent (%s)", name, a.Extends, builtinName)
		case (builtin[name] || a.Extends != "") && (a.AddDirFlag != "" || a.ContextFlag != ""):
			add("agent %q: addDirFlag and contextFlag only apply to generic `command` agents; built-in adapters handle directories and context themselves", name)
		case !builtin[name] && a.Extends == "" && a.Command == "":
			add("agent %q needs `extends: <built-in>` or `command: <executable>` (built-in: %s)", name, builtinName)
		}
		for k := range a.envOrNil() {
			if k == "" || strings.ContainsAny(k, "=\x00") {
				add("agent %q: invalid environment variable name %q", name, k)
			}
		}
	}

	for _, name := range SortedKeys(c.Groups) {
		g := c.Groups[name]
		if g == nil {
			add("group %q is empty", name)
			continue
		}
		for _, r := range g.Repos {
			if c.Repos[r] == nil {
				add("group %q references unknown repo %q; add it under `repos:`", name, r)
			}
		}
		for _, sub := range g.Groups {
			if c.Groups[sub] == nil {
				add("group %q references unknown group %q", name, sub)
			}
		}
	}
	if cyc := c.groupCycle(); cyc != nil {
		add("groups form a cycle: %s", strings.Join(cyc, " -> "))
	}

	for _, name := range SortedKeys(c.Workspaces) {
		ws := c.Workspaces[name]
		if ws == nil {
			add("workspace %q is empty; it needs at least `primary:`", name)
			continue
		}
		if ws.Primary == "" {
			add("workspace %q has no `primary:` repository", name)
		} else if c.Repos[ws.Primary] == nil {
			add("workspace %q: primary %q is not a known repo; add it under `repos:`", name, ws.Primary)
		}
		for _, r := range ws.Repos {
			if c.Repos[r] == nil {
				add("workspace %q references unknown repo %q; add it under `repos:`", name, r)
			}
		}
		for _, g := range ws.Groups {
			if c.Groups[g] == nil {
				add("workspace %q references unknown group %q; add it under `groups:`", name, g)
			}
		}
		if ws.DefaultAgent != "" && !knownAgent(ws.DefaultAgent) {
			add("workspace %q: defaultAgent %q is not a known agent", name, ws.DefaultAgent)
		}
		if ws.Context != "" {
			if st, err := os.Stat(ws.ContextPath); err != nil {
				add("workspace %q: context file %s: %v", name, ws.Context, unwrapPathErr(err))
			} else if st.IsDir() {
				add("workspace %q: context %s is a directory; point it at a markdown file", name, ws.Context)
			}
		}
	}

	// Two names for the same physical repository would make duplicate roots.
	byPath := map[string]string{}
	for _, name := range SortedKeys(c.Repos) {
		p := c.Repos[name].Path
		if p == "" {
			continue
		}
		if other, ok := byPath[p]; ok {
			add("repos %q and %q point to the same directory %s; keep one", other, name, p)
			continue
		}
		byPath[p] = name
	}
	return errs
}

// groupCycle returns one cycle in the group graph, or nil.
func (c *Config) groupCycle() []string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) []string
	visit = func(n string) []string {
		switch state[n] {
		case visiting:
			for i, s := range stack {
				if s == n {
					return append(append([]string{}, stack[i:]...), n)
				}
			}
		case done:
			return nil
		}
		g := c.Groups[n]
		if g == nil {
			return nil
		}
		state[n] = visiting
		stack = append(stack, n)
		for _, sub := range g.Groups {
			if cyc := visit(sub); cyc != nil {
				return cyc
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return nil
	}
	for _, n := range SortedKeys(c.Groups) {
		if cyc := visit(n); cyc != nil {
			return cyc
		}
	}
	return nil
}

func (a *Agent) envOrNil() map[string]string {
	if a == nil {
		return nil
	}
	return a.Env
}

// AgentFor applies agent precedence: CLI flag, then workspace, then global.
func (c *Config) AgentFor(workspace, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if ws := c.Workspaces[workspace]; ws != nil && ws.DefaultAgent != "" {
		return ws.DefaultAgent, nil
	}
	if c.DefaultAgent != "" {
		return c.DefaultAgent, nil
	}
	return "", fmt.Errorf("no agent selected: pass --agent, or set `defaultAgent:` globally or on workspace %q", workspace)
}

// SortedKeys returns the keys of m in lexical order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
