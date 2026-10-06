package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Gemini adapts Gemini CLI. See docs/agents/gemini.md.
type Gemini struct{}

// Gemini's macOS seatbelt sandbox mounts at most this many include dirs.
const geminiSeatbeltMaxDirs = 5

func (Gemini) Name() string { return "gemini" }

func (Gemini) Info() Info {
	return Info{
		Display:          "Gemini CLI",
		Binary:           "gemini",
		MultiRoot:        "native (--include-directories)",
		Context:          "GEMINI.md (include dir)",
		RepoInstructions: "native (GEMINI.md)",
		RequiredFlags:    []string{"--include-directories"},
	}
}

func (g Gemini) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	// Gemini memory files are discovered by name inside include directories.
	ctxDir := filepath.Join(runDir, "gemini")
	ctxFile, err := instructionsFile(ws, filepath.Join(ctxDir, "GEMINI.md"))
	if err != nil {
		return nil, err
	}
	// Gemini loads GEMINI.md from every workspace directory (include dirs
	// too) in trusted folders, so the run dir is added as one more.
	spec := &LaunchSpec{
		Agent: g.Name(), Command: "gemini", Dir: ws.Primary().Path, RunDir: runDir,
		Files: []File{ctxFile},
	}
	dirs := []string{}
	for _, r := range ws.Additional() {
		dirs = append(dirs, r.Path)
	}
	dirs = append(dirs, ctxDir)
	for _, d := range dirs {
		// Gemini splits --include-directories values on commas.
		if strings.Contains(d, ",") {
			return nil, fmt.Errorf("gemini cannot include %s: Gemini CLI splits --include-directories on commas; rename the directory or use another agent", d)
		}
		// The =form keeps yargs from treating a forwarded prompt as another directory.
		spec.Args = append(spec.Args, "--include-directories="+d)
	}
	spec.Args = append(spec.Args, args...)

	if runtime.GOOS == "darwin" && len(dirs) > geminiSeatbeltMaxDirs {
		msg := fmt.Sprintf("this workspace needs %d include directories but Gemini's macOS sandbox mounts only %d", len(dirs), geminiSeatbeltMaxDirs)
		if hasSandboxFlag(args) {
			return nil, errors.New(msg + "; run without --sandbox or use a smaller workspace")
		}
		spec.Warnings = append(spec.Warnings, msg+"; do not enable the sandbox for this workspace")
	}
	return spec, nil
}

func hasSandboxFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-s" || a == "--sandbox" || strings.HasPrefix(a, "--sandbox=") && a != "--sandbox=false" {
			return true
		}
	}
	return false
}

func (g Gemini) Check(d Detection) []string { return checkFlags(g.Info(), d) }
