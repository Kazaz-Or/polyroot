package agent

import (
	"os"
	"path/filepath"

	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Claude adapts Claude Code. See docs/agents/claude.md.
type Claude struct{}

const claudeMDEnv = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"

func (Claude) Name() string { return "claude" }

func (Claude) Info() Info {
	return Info{
		Display:          "Claude Code",
		Binary:           "claude",
		MultiRoot:        "native (--add-dir)",
		Context:          "system prompt",
		RepoInstructions: "native (CLAUDE.md)",
		RequiredFlags:    []string{"--add-dir", "--append-system-prompt"},
	}
}

func (c Claude) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	ctxFile, err := instructionsFile(ws, filepath.Join(runDir, "workspace.md"))
	if err != nil {
		return nil, err
	}
	spec := &LaunchSpec{Agent: c.Name(), Command: "claude", Dir: ws.Primary().Path, RunDir: runDir, Files: []File{ctxFile}}
	for _, r := range ws.Additional() {
		spec.Args = append(spec.Args, "--add-dir", r.Path)
	}
	// --add-dir is variadic; ending with a single-value flag stops it from
	// swallowing a forwarded prompt as another directory.
	spec.Args = append(spec.Args, "--append-system-prompt-file", ctxFile.Path)
	spec.Args = append(spec.Args, args...)

	// Load CLAUDE.md from the extra roots, unless the user chose otherwise.
	if _, set := os.LookupEnv(claudeMDEnv); !set {
		spec.Env = append(spec.Env, EnvVar{claudeMDEnv, "1"})
	}
	return spec, nil
}

func (c Claude) Check(d Detection) []string { return checkFlags(c.Info(), d) }
