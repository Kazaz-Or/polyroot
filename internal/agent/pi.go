package agent

import (
	"path/filepath"

	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Pi adapts the pi coding agent. See docs/agents/pi.md.
type Pi struct{}

func (Pi) Name() string { return "pi" }

func (Pi) Info() Info {
	return Info{
		Display: "Pi",
		Binary:  "pi",
		// Pi has no path sandbox: its tools can already reach every repo.
		MultiRoot:        "unrestricted",
		Context:          "system prompt",
		RepoInstructions: "system prompt",
		RequiredFlags:    []string{"--append-system-prompt"},
	}
}

func (p Pi) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	ctxFile, err := instructionsFile(ws, filepath.Join(runDir, "workspace.md"))
	if err != nil {
		return nil, err
	}
	spec := &LaunchSpec{Agent: p.Name(), Command: "pi", Dir: ws.Primary().Path, RunDir: runDir, Files: []File{ctxFile}}
	// pi reads --append-system-prompt as a file when the path exists.
	spec.Args = append(spec.Args, "--append-system-prompt", ctxFile.Path)
	// pi loads AGENTS.md/CLAUDE.md only from the cwd upward; append the
	// other repositories' instruction files explicitly.
	for _, r := range ws.Additional() {
		if f := repoInstructionFile(r); f != "" {
			spec.Args = append(spec.Args, "--append-system-prompt", f)
		}
	}
	spec.Args = append(spec.Args, args...)
	return spec, nil
}

func (p Pi) Check(d Detection) []string { return checkFlags(p.Info(), d) }
