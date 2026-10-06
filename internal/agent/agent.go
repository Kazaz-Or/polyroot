// Package agent translates a resolved workspace into a launch specification
// for a specific coding agent. Every agent-specific detail lives here.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Adapter maps a workspace onto one agent's native capabilities.
type Adapter interface {
	Name() string
	Info() Info
	// Build returns the launch spec for ws. args are forwarded verbatim after
	// Polyroot's own arguments. Generated files must live under runDir.
	// Build must fail rather than drop a repository it cannot represent.
	Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error)
	// Check returns problems found in the detected installation and the
	// user's environment (missing flags, conflicting settings). Never fatal
	// for unrelated agents.
	Check(d Detection) []string
}

// Info is the static description of an adapter.
type Info struct {
	Display string // human name, e.g. "Claude Code"
	Binary  string // executable looked up in PATH
	// Capability labels shown by `polyroot agents`.
	MultiRoot        string // how extra repos are exposed
	Context          string // how workspace instructions are delivered
	RepoInstructions string // how extra repos' AGENTS.md/CLAUDE.md are loaded
	// RequiredFlags must appear in `<binary> --help` for the adapter to work.
	RequiredFlags []string
}

// LaunchSpec fully describes how an agent will be started.
type LaunchSpec struct {
	Agent    string
	Command  string   // executable name or path, looked up in PATH at launch
	Args     []string // arguments after the executable
	Dir      string   // working directory (the primary repository)
	Env      []EnvVar // additions to the inherited environment
	Files    []File   // generated before launch, removed after exit
	RunDir   string   // Polyroot-owned directory holding Files ("" if none)
	Warnings []string
}

// EnvVar is one environment addition.
type EnvVar struct{ Key, Value string }

// File is a generated file.
type File struct {
	Path    string
	Content []byte
}

// Detection is what Polyroot learned about an installed agent locally.
type Detection struct {
	Path    string // resolved executable, "" when not installed
	Version string
	Help    string
	Err     error // why detection failed, if it did
}

// Installed reports whether the binary was found.
func (d Detection) Installed() bool { return d.Path != "" }

var versionRE = regexp.MustCompile(`\d+\.\d+(\.\d+)?([-+][0-9A-Za-z.-]+)?`)

// Detect looks up binary in PATH and runs `--version` and `--help` with a
// timeout. It never returns an error; failures are recorded in Detection.Err.
func Detect(ctx context.Context, binary string) Detection {
	path, err := exec.LookPath(binary)
	if err != nil {
		return Detection{Err: fmt.Errorf("%s not found in PATH", binary)}
	}
	d := Detection{Path: path}
	if out, err := probe(ctx, path, "--version"); err != nil {
		d.Err = fmt.Errorf("%s --version: %w", binary, err)
	} else {
		d.Version = versionRE.FindString(out)
	}
	if out, err := probe(ctx, path, "--help"); err != nil && d.Err == nil {
		d.Err = fmt.Errorf("%s --help: %w", binary, err)
	} else {
		d.Help = out
	}
	return d
}

func probe(ctx context.Context, path, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	cmd.Stdin = nil
	// Keep agents from starting update checks or TUIs while probing.
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "CI=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", errors.New("timed out")
	}
	// Some CLIs exit non-zero after printing help; the text is what matters.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(out) > 0 {
		err = nil
	}
	return string(out), err
}

// MissingFlags returns the flags in want that do not appear in the help text.
func (d Detection) MissingFlags(want []string) []string {
	var missing []string
	for _, f := range want {
		if !strings.Contains(d.Help, f) {
			missing = append(missing, f)
		}
	}
	return missing
}

// Status summarizes detection and adapter checks for display.
func Status(a Adapter, d Detection) string {
	switch {
	case !d.Installed():
		return "not installed"
	case len(d.MissingFlags(a.Info().RequiredFlags)) > 0:
		return "incompatible"
	case len(a.Check(d)) > 0:
		return "warnings"
	}
	return "ready"
}

// checkFlags is the shared part of Check: required flags present in --help.
func checkFlags(info Info, d Detection) []string {
	if !d.Installed() || d.Help == "" {
		return nil
	}
	if m := d.MissingFlags(info.RequiredFlags); len(m) > 0 {
		return []string{fmt.Sprintf("`%s --help` does not mention %s; this version may be too old for Polyroot (upgrade %s)",
			info.Binary, strings.Join(m, ", "), info.Display)}
	}
	return nil
}

// Builtins returns Polyroot's built-in adapters in display order.
func Builtins() []Adapter {
	return []Adapter{Claude{}, Codex{}, Gemini{}, OpenCode{}, Pi{}}
}

// BuiltinNames returns the names of the built-in adapters.
func BuiltinNames() []string {
	var names []string
	for _, a := range Builtins() {
		names = append(names, a.Name())
	}
	return names
}

// All returns the built-in adapters (with config overrides applied)
// followed by custom agents from cfg.
func All(cfg *config.Config) []Adapter {
	var all []Adapter
	for _, name := range BuiltinNames() {
		a, _ := Lookup(cfg, name)
		all = append(all, a)
	}
	if cfg != nil {
		for _, name := range config.SortedKeys(cfg.Agents) {
			if isBuiltin(name) {
				continue
			}
			if a, err := Lookup(cfg, name); err == nil {
				all = append(all, a)
			}
		}
	}
	return all
}

func isBuiltin(name string) bool {
	for _, a := range Builtins() {
		if a.Name() == name {
			return true
		}
	}
	return false
}

// Lookup finds an agent by name: a built-in (with any config overrides),
// a name extending a built-in, or a generic command agent.
func Lookup(cfg *config.Config, name string) (Adapter, error) {
	var def *config.Agent
	if cfg != nil {
		def = cfg.Agents[name]
	}
	for _, a := range Builtins() {
		if a.Name() == name {
			if def != nil {
				return Configured{name: name, base: a, def: def}, nil
			}
			return a, nil
		}
	}
	switch {
	case def == nil:
		return nil, fmt.Errorf("unknown agent %q (built-in: %s; custom agents go under `agents:` in config)",
			name, strings.Join(BuiltinNames(), ", "))
	case def.Extends != "":
		if !isBuiltin(def.Extends) {
			return nil, fmt.Errorf("agent %q extends unknown built-in %q", name, def.Extends)
		}
		// The base keeps its own overrides, so `claude` settings apply to
		// every agent that extends claude.
		base, err := Lookup(cfg, def.Extends)
		if err != nil {
			return nil, err
		}
		return Configured{name: name, base: base, def: def}, nil
	}
	return Custom{name: name, def: def}, nil
}

// setEnv adds or replaces an environment addition.
func (s *LaunchSpec) setEnv(key, value string) {
	for i := range s.Env {
		if s.Env[i].Key == key {
			s.Env[i].Value = value
			return
		}
	}
	s.Env = append(s.Env, EnvVar{key, value})
}

// instructionsFile renders the workspace instructions into a file under dir.
func instructionsFile(ws *workspace.Resolved, path string) (File, error) {
	text, err := workspace.Instructions(ws)
	if err != nil {
		return File{}, err
	}
	return File{Path: path, Content: []byte(text)}, nil
}

// repoInstructionFile picks the instruction file an AGENTS.md-first agent
// would load for a repo: AGENTS.md, else CLAUDE.md. "" if neither exists.
func repoInstructionFile(r workspace.Repo) string {
	for _, want := range []string{"AGENTS.md", "CLAUDE.md"} {
		for _, have := range r.Instructions {
			if have == want {
				return filepath.Join(r.Path, have)
			}
		}
	}
	return ""
}
