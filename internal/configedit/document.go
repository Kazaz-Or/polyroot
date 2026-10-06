// Package configedit edits the Polyroot config file in place, keeping
// comments, key order and anything it does not manage.
package configedit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Kazaz-Or/polyroot/internal/config"
)

const header = `Polyroot configuration, created by "polyroot setup".
Edit it freely: polyroot commands keep your changes and comments.
All options: https://github.com/Kazaz-Or/polyroot/blob/master/examples/config.yaml`

// Document is a config file edited at the YAML node level, so comments,
// key order and anything setup doesn't manage (groups, agents, ...) survive.
type Document struct {
	doc *yaml.Node
}

// Workspace is what setup manages for a workspace. Other keys already
// present on the workspace (groups, defaultAgent, ...) are kept.
type Workspace struct {
	Primary      string
	Repos        []string
	Context      string // relative to the config dir; "" keeps the current value
	DefaultAgent string // "" keeps the current value
}

// NewDocument returns an empty config.
func NewDocument() *Document {
	top := &yaml.Node{Kind: yaml.MappingNode, HeadComment: header}
	mapSet(top, "version", scalar("1"))
	return &Document{doc: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{top}}}
}

// LoadDocument parses existing config data.
func LoadDocument(data []byte) (*Document, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return NewDocument(), nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config is not a YAML mapping")
	}
	return &Document{doc: &doc}, nil
}

func (d *Document) top() *yaml.Node { return d.doc.Content[0] }

// SetDefaultAgent sets the global defaultAgent.
func (d *Document) SetDefaultAgent(name string) {
	mapSet(d.top(), "defaultAgent", scalar(name))
}

// SetRepoDirs sets the directories that hold repositories.
func (d *Document) SetRepoDirs(dirs []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, dir := range dirs {
		seq.Content = append(seq.Content, scalar(dir))
	}
	mapSet(d.top(), "repoDirs", seq)
}

// AddRepo registers a repository unless the name already exists.
func (d *Document) AddRepo(name, path string) {
	repos := d.section("repos")
	if mapGet(repos, name) == nil {
		mapSet(repos, name, scalar(path))
	}
}

// SetWorkspace creates or updates a workspace.
func (d *Document) SetWorkspace(name string, w Workspace) {
	ws := mapGet(d.section("workspaces"), name)
	if ws == nil || ws.Kind != yaml.MappingNode {
		ws = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(d.section("workspaces"), name, ws)
	}
	mapSet(ws, "primary", scalar(w.Primary))
	if len(w.Repos) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, r := range w.Repos {
			seq.Content = append(seq.Content, scalar(r))
		}
		mapSet(ws, "repos", seq)
	} else {
		mapDelete(ws, "repos")
	}
	if w.Context != "" {
		mapSet(ws, "context", scalar(w.Context))
	}
	if w.DefaultAgent != "" {
		mapSet(ws, "defaultAgent", scalar(w.DefaultAgent))
	}
}

// RemoveWorkspace deletes a workspace; it reports whether it existed.
func (d *Document) RemoveWorkspace(name string) bool {
	ws := mapGet(d.top(), "workspaces")
	if ws == nil || mapGet(ws, name) == nil {
		return false
	}
	mapDelete(ws, name)
	return true
}

// Bytes renders the document.
func (d *Document) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// Save validates the document as a Polyroot config and writes it atomically.
// An existing file is kept as <file>.bak.
func (d *Document) Save(file string, builtinAgents []string) error {
	data, err := d.Bytes()
	if err != nil {
		return err
	}
	cfg, err := config.Parse(data, file)
	if err != nil {
		return fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	if errs := cfg.Check(builtinAgents); len(errs) > 0 {
		return fmt.Errorf("refusing to write an invalid config: %w", errors.Join(errs...))
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(file); err == nil {
		if err := os.WriteFile(file+".bak", old, 0o644); err != nil {
			return err
		}
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// section returns the top-level mapping for key, creating it if needed.
func (d *Document) section(key string) *yaml.Node {
	n := mapGet(d.top(), key)
	if n == nil || n.Kind != yaml.MappingNode {
		// An empty `repos:` parses as null; replace it with a mapping.
		n = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(d.top(), key, n)
	}
	return n
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			// Keep comments attached to the old value.
			val.LineComment, val.HeadComment, val.FootComment = old.LineComment, old.HeadComment, old.FootComment
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key), val)
}

func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// RepoName picks a config name for path: its directory name, or
// "<parent>-<dir>" when that is taken by another path.
func RepoName(path string, taken map[string]string) string {
	base := filepath.Base(path)
	candidates := []string{base, filepath.Base(filepath.Dir(path)) + "-" + base}
	for _, c := range candidates {
		if p, ok := taken[c]; !ok || p == path {
			return c
		}
	}
	for i := 2; ; i++ {
		c := base + "-" + strconv.Itoa(i)
		if _, ok := taken[c]; !ok {
			return c
		}
	}
}

// DisplayPath writes paths under home as ~/... so configs stay portable.
func DisplayPath(path, home string) string {
	if home != "" {
		if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~/" + rel
		}
	}
	return path
}
