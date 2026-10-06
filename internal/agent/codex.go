package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// Codex adapts OpenAI Codex CLI. See docs/agents/codex.md.
type Codex struct{}

func (Codex) Name() string { return "codex" }

func (Codex) Info() Info {
	return Info{
		Display:          "Codex CLI",
		Binary:           "codex",
		MultiRoot:        "native (--add-dir)",
		Context:          "developer_instructions",
		RepoInstructions: "listed in map",
		RequiredFlags:    []string{"--add-dir", "--config"},
	}
}

func (c Codex) Build(ws *workspace.Resolved, args []string, _ string) (*LaunchSpec, error) {
	text, err := workspace.Instructions(ws)
	if err != nil {
		return nil, err
	}
	// Linux caps one argv element at 128 KiB (MAX_ARG_STRLEN).
	if len(text) > 120<<10 {
		return nil, fmt.Errorf("codex: workspace instructions are %d KiB; Codex receives them as one argument, which is limited to 128 KiB (shorten the context file)", len(text)>>10)
	}
	spec := &LaunchSpec{Agent: c.Name(), Command: "codex", Dir: ws.Primary().Path}
	for _, r := range ws.Additional() {
		spec.Args = append(spec.Args, "--add-dir", r.Path)
	}
	// -c values are parsed as TOML; pass a quoted TOML string so markdown
	// can never be misread as another TOML type.
	spec.Args = append(spec.Args, "-c", "developer_instructions="+tomlString(text))
	spec.Args = append(spec.Args, args...)
	return spec, nil
}

var codexDevInstr = regexp.MustCompile(`(?m)^\s*developer_instructions\s*=`)

func (c Codex) Check(d Detection) []string {
	probs := checkFlags(c.Info(), d)
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".codex")
		}
	}
	file := filepath.Join(home, "config.toml")
	if data, err := os.ReadFile(file); err == nil && codexDevInstr.Match(data) {
		probs = append(probs, fmt.Sprintf("%s sets developer_instructions; Polyroot's workspace instructions replace it for Polyroot launches", file))
	}
	return probs
}

// tomlString encodes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
