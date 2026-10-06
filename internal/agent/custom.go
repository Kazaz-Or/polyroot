package agent

import (
	"fmt"
	"path/filepath"

	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Configured is a built-in adapter customized in config: either a built-in
// name with overrides (`agents: {claude: {args: [...]}}`) or a new name
// that extends a built-in. Config args come before args given after `--`.
type Configured struct {
	name string
	base Adapter
	def  *config.Agent
}

func (c Configured) Name() string { return c.name }

func (c Configured) Info() Info {
	info := c.base.Info()
	if c.name != c.base.Name() {
		info.Display = fmt.Sprintf("%s (%s)", info.Display, c.base.Name())
	}
	if c.def.Command != "" {
		info.Binary = c.def.Command
	}
	return info
}

func (c Configured) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	spec, err := c.base.Build(ws, append(append([]string{}, c.def.Args...), args...), runDir)
	if err != nil {
		return nil, err
	}
	spec.Agent = c.name
	if c.def.Command != "" {
		spec.Command = c.def.Command
	}
	for _, k := range config.SortedKeys(c.def.Env) {
		spec.setEnv(k, c.def.Env[k])
	}
	return spec, nil
}

func (c Configured) Check(d Detection) []string { return c.base.Check(d) }

// Custom is a generic command adapter defined in config. Every value is a
// separate argv element; nothing is passed through a shell.
type Custom struct {
	name string
	def  *config.Agent
}

func (c Custom) Name() string { return c.name }

func (c Custom) Info() Info {
	info := Info{Display: c.name + " (custom)", Binary: c.def.Command, MultiRoot: "none", Context: "none", RepoInstructions: "listed in map"}
	if c.def.AddDirFlag != "" {
		info.MultiRoot = "flag " + c.def.AddDirFlag
	}
	if c.def.ContextFlag != "" {
		info.Context = "flag " + c.def.ContextFlag
	}
	return info
}

func (c Custom) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	spec := &LaunchSpec{Agent: c.name, Command: c.def.Command, Dir: ws.Primary().Path}
	spec.Args = append(spec.Args, c.def.Args...)
	if extra := ws.Additional(); len(extra) > 0 {
		if c.def.AddDirFlag == "" {
			return nil, fmt.Errorf("agent %q cannot open workspace %q: it has %d additional repositories but no `addDirFlag` is configured", c.name, ws.Name, len(extra))
		}
		for _, r := range extra {
			spec.Args = append(spec.Args, c.def.AddDirFlag, r.Path)
		}
	}
	if c.def.ContextFlag != "" {
		f, err := instructionsFile(ws, filepath.Join(runDir, "workspace.md"))
		if err != nil {
			return nil, err
		}
		spec.RunDir = runDir
		spec.Files = append(spec.Files, f)
		spec.Args = append(spec.Args, c.def.ContextFlag, f.Path)
	} else {
		spec.Warnings = append(spec.Warnings, fmt.Sprintf("agent %q has no `contextFlag`; the workspace map and context are not passed to it", c.name))
	}
	spec.Args = append(spec.Args, args...)
	for _, k := range config.SortedKeys(c.def.Env) {
		spec.setEnv(k, c.def.Env[k])
	}
	return spec, nil
}

func (c Custom) Check(Detection) []string { return nil }
