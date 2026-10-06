package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Kazaz-Or/polyroot/internal/agent"
	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/configedit"
)

var workspaceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// ask runs one interactive prompt. Set ACCESSIBLE=1 for screen readers.
func ask(field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errors.New("cancelled; nothing was written")
	}
	return err
}

func (a *app) setupCmd() *cobra.Command {
	var agentFlag string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup: choose your default agent and create the config",
		Long: `First-time setup. Choose your default coding agent and create config.yaml.

Run it once. Afterwards, add workspaces with "polyroot workspace add" or edit
config.yaml by hand.`,
		Example: "  polyroot setup\n  polyroot setup --agent codex",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			file := a.dirs.File()
			if _, err := os.Stat(file); err == nil {
				fmt.Fprintf(a.stdout, "Polyroot is already set up: %s\n\nAdd a workspace:  polyroot workspace add <name> <repo-path>...\nOr edit the file by hand.\n", file)
				return nil
			}
			choice := agentFlag
			if choice == "" {
				if !isTerminal() {
					return errors.New("no terminal for the interactive prompt; pass --agent <name>")
				}
				var opts []huh.Option[string]
				for _, ad := range agent.Builtins() {
					label := fmt.Sprintf("%-9s %s", ad.Name(), ad.Info().Display)
					if _, err := exec.LookPath(ad.Info().Binary); err == nil {
						label += "  (installed)"
						if choice == "" {
							choice = ad.Name()
						}
					}
					opts = append(opts, huh.NewOption(label, ad.Name()))
				}
				if err := ask(huh.NewSelect[string]().
					Title("Which coding agent should Polyroot use by default?").
					Description("You can still pick another one per launch with --agent.").
					Options(opts...).Value(&choice)); err != nil {
					return err
				}
			}
			if _, err := agent.Lookup(nil, choice); err != nil {
				return err
			}
			doc := configedit.NewDocument()
			doc.SetDefaultAgent(choice)
			if err := doc.Save(file, agent.BuiltinNames()); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Created %s (default agent: %s)\n\nNext, create a workspace from your repositories:\n  polyroot workspace add <name> <primary-repo-path> <other-repo-path>...\n", file, choice)
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "default agent (skips the prompt): "+strings.Join(agent.BuiltinNames(), ", "))
	return cmd
}

func (a *app) workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Add, update or remove workspaces",
		Long: `Manage workspaces in config.yaml. Changes are made in place: comments and
anything you edited by hand are kept, and the previous file is saved as
config.yaml.bak.`,
	}
	cmd.AddCommand(a.workspaceAddCmd(), a.workspaceRemoveCmd())
	return cmd
}

func (a *app) workspaceAddCmd() *cobra.Command {
	var primary, agentFlag string
	cmd := &cobra.Command{
		Use:     "add [name] [repo-path...]",
		Aliases: []string{"create"},
		Short:   "Create a workspace from repository paths, or add repositories to one",
		Long: `Create a workspace from repository paths. The first path is the primary
repository: the agent starts there and the others are added alongside it.

If the workspace exists, the paths are added to it. Each path is registered
under "repos:" once (named after its directory) and can be shared by any
number of workspaces. Without arguments, you are prompted for everything.`,
		Example: `  polyroot workspace add payments ~/git/payments-api ~/git/payments-web ~/git/shared-sdk
  polyroot workspace add payments ~/git/helm              # add a repo to an existing workspace
  polyroot workspace add gaming ~/git/gaming-api ~/git/shared-sdk --agent codex
  polyroot workspace add                                  # interactive`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var name, notes string
			var paths []string
			if len(args) > 0 {
				name, paths = args[0], args[1:]
			}
			if name == "" || len(paths) == 0 {
				if !isTerminal() {
					return errors.New("missing arguments: polyroot workspace add <name> <repo-path> [<repo-path> ...]")
				}
				var err error
				if name, paths, notes, err = a.promptWorkspace(cmd.Root(), name); err != nil {
					return err
				}
			}
			return a.addWorkspace(cmd.Root(), name, paths, primary, agentFlag, notes)
		},
	}
	cmd.Flags().StringVar(&primary, "primary", "", "primary repository (path or repo name); default: the first path, or the current primary")
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "default agent for this workspace")
	return cmd
}

func (a *app) workspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a workspace (its repositories stay registered)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			doc, _, err := a.loadForEdit()
			if err != nil {
				return err
			}
			if !doc.RemoveWorkspace(args[0]) {
				return fmt.Errorf("unknown workspace %q (see `polyroot list`)", args[0])
			}
			if err := doc.Save(a.dirs.File(), agent.BuiltinNames()); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Removed workspace %q. Its repositories stay registered under repos: in %s.\n", args[0], a.dirs.File())
			return nil
		},
	}
}

func (a *app) loadForEdit() (*configedit.Document, *config.Config, error) {
	file := a.dirs.File()
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("no config yet; run `polyroot setup` first")
	}
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Parse(data, file)
	if err != nil {
		return nil, nil, err
	}
	doc, err := configedit.LoadDocument(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return doc, cfg, nil
}

func validWorkspaceName(root *cobra.Command, name string) error {
	if !workspaceNameRE.MatchString(name) {
		return fmt.Errorf("invalid workspace name %q: use letters, digits, '.', '_' or '-'", name)
	}
	// `polyroot <workspace>` must not be shadowed by a command.
	for _, c := range root.Commands() {
		if c.Name() == name || slices.Contains(c.Aliases, name) || name == "help" {
			return fmt.Errorf("%q is a polyroot command; choose another workspace name", name)
		}
	}
	return nil
}

func (a *app) addWorkspace(root *cobra.Command, name string, paths []string, primaryArg, agentFlag, notes string) error {
	if err := validWorkspaceName(root, name); err != nil {
		return err
	}
	if agentFlag != "" {
		if _, err := agent.Lookup(nil, agentFlag); err != nil {
			return err
		}
	}
	doc, cfg, err := a.loadForEdit()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Map each path to a repo name, reusing an existing registration.
	taken := map[string]string{}
	for n, r := range cfg.Repos {
		taken[n] = r.Path
	}
	nameFor := func(raw string) (string, error) {
		p, err := config.RepoPath(raw, cwd)
		if err != nil {
			return "", fmt.Errorf("%s: %w", raw, err)
		}
		for n, existing := range taken {
			if existing == p {
				return n, nil
			}
		}
		n := configedit.RepoName(p, taken)
		taken[n] = p
		doc.AddRepo(n, configedit.DisplayPath(p, home))
		fmt.Fprintf(a.stdout, "Registered repo %s: %s\n", n, configedit.DisplayPath(p, home))
		return n, nil
	}
	var added []string
	for _, raw := range paths {
		n, err := nameFor(raw)
		if err != nil {
			return err
		}
		if !slices.Contains(added, n) {
			added = append(added, n)
		}
	}

	ws := configedit.Workspace{DefaultAgent: agentFlag}
	existing := cfg.Workspaces[name]
	if existing != nil {
		ws.Primary, ws.Repos = existing.Primary, slices.Clone(existing.Repos)
	} else if len(added) > 0 {
		ws.Primary = added[0]
	}
	if primaryArg != "" {
		if cfg.Repos[primaryArg] != nil || taken[primaryArg] != "" {
			ws.Primary = primaryArg
		} else if ws.Primary, err = nameFor(primaryArg); err != nil {
			return err
		}
	}
	for _, n := range added {
		if !slices.Contains(ws.Repos, n) {
			ws.Repos = append(ws.Repos, n)
		}
	}
	ws.Repos = slices.DeleteFunc(ws.Repos, func(n string) bool { return n == ws.Primary })
	if ws.Primary == "" {
		return errors.New("a new workspace needs at least one repository path")
	}

	if strings.TrimSpace(notes) != "" && (existing == nil || existing.Context == "") {
		rel := filepath.Join("contexts", name+".md")
		file := filepath.Join(a.dirs.Config, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte("# "+name+"\n\n"+strings.TrimSpace(notes)+"\n"), 0o644); err != nil {
			return err
		}
		ws.Context = rel
	}

	doc.SetWorkspace(name, ws)
	if err := doc.Save(a.dirs.File(), agent.BuiltinNames()); err != nil {
		return err
	}
	verb := "Created"
	if existing != nil {
		verb = "Updated"
	}
	fmt.Fprintf(a.stdout, "%s workspace %q: primary %s", verb, name, ws.Primary)
	if len(ws.Repos) > 0 {
		fmt.Fprintf(a.stdout, ", plus %s", strings.Join(ws.Repos, ", "))
	}
	if existing != nil && len(existing.Groups) > 0 {
		fmt.Fprintf(a.stdout, ", plus groups %s", strings.Join(existing.Groups, ", "))
	}
	fmt.Fprintf(a.stdout, "\n\nOpen it:  polyroot %s\n", name)
	return nil
}

// promptWorkspace asks for whatever was not given on the command line.
func (a *app) promptWorkspace(root *cobra.Command, name string) (string, []string, string, error) {
	if name == "" {
		if err := ask(huh.NewInput().
			Title("Workspace name").
			Description("A short name for the product, e.g. payments. You will open it with: polyroot <name>").
			Validate(func(s string) error { return validWorkspaceName(root, s) }).
			Value(&name)); err != nil {
			return "", nil, "", err
		}
	}
	cwd, _ := os.Getwd()
	var paths []string
	for {
		var p string
		title := "Primary repository path (the agent starts here)"
		if len(paths) > 0 {
			title = "Another repository path (leave empty to finish)"
		}
		if err := ask(huh.NewInput().Title(title).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					if len(paths) == 0 {
						return errors.New("enter at least one repository path")
					}
					return nil
				}
				_, err := config.RepoPath(strings.TrimSpace(s), cwd)
				return err
			}).Value(&p)); err != nil {
			return "", nil, "", err
		}
		if p = strings.TrimSpace(p); p == "" {
			break
		}
		paths = append(paths, p)
	}
	var notes string
	if err := ask(huh.NewText().
		Title("Describe this workspace for the agent (optional)").
		Description("How the repositories fit together. Saved as a context file you can edit later.").
		Value(&notes)); err != nil {
		return "", nil, "", err
	}
	return name, paths, notes, nil
}
