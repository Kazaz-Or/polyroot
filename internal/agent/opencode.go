package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// OpenCode adapts OpenCode. See docs/agents/opencode.md.
type OpenCode struct{}

const openCodeConfigEnv = "OPENCODE_CONFIG_CONTENT"

func (OpenCode) Name() string { return "opencode" }

func (OpenCode) Info() Info {
	return Info{
		Display:          "OpenCode",
		Binary:           "opencode",
		MultiRoot:        "config (external_directory)",
		Context:          "config (instructions)",
		RepoInstructions: "config (instructions)",
	}
}

func (o OpenCode) Build(ws *workspace.Resolved, args []string, runDir string) (*LaunchSpec, error) {
	if os.Getenv(openCodeConfigEnv) != "" {
		return nil, errors.New("opencode: " + openCodeConfigEnv + " is already set in your environment; Polyroot passes the workspace through it and will not overwrite yours (unset it, or move it into an opencode.json)")
	}
	ctxFile, err := instructionsFile(ws, filepath.Join(runDir, "workspace.md"))
	if err != nil {
		return nil, err
	}
	instructions := []string{ctxFile.Path}
	allow := map[string]string{}
	for _, r := range ws.Additional() {
		// OpenCode permission patterns treat * and ? as wildcards.
		if strings.ContainsAny(r.Path, "*?") {
			return nil, fmt.Errorf("opencode cannot express a permission for %s (%s): the path contains a glob character", r.Name, r.Path)
		}
		allow[r.Path+"/*"] = "allow"
		if f := repoInstructionFile(r); f != "" {
			instructions = append(instructions, f)
		}
	}
	// Inline config is merged over the user's config for this process only:
	// `instructions` arrays concatenate and permissions deep-merge.
	cfg := map[string]any{"instructions": instructions}
	if len(allow) > 0 {
		cfg["permission"] = map[string]any{"external_directory": allow}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return &LaunchSpec{
		Agent: o.Name(), Command: "opencode", Dir: ws.Primary().Path, RunDir: runDir,
		Args:  append([]string{}, args...),
		Env:   []EnvVar{{openCodeConfigEnv, string(data)}},
		Files: []File{ctxFile},
	}, nil
}

func (o OpenCode) Check(d Detection) []string {
	probs := checkFlags(o.Info(), d)
	if os.Getenv(openCodeConfigEnv) != "" {
		probs = append(probs, openCodeConfigEnv+" is set in your environment; `polyroot open --agent opencode` will refuse to override it")
	}
	return probs
}
