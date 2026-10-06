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
	"golang.org/x/term"

	"github.com/Kazaz-Or/polyroot/internal/agent"
	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/configedit"
)

var workspaceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
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
	var repoDirs []string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup: default agent and where your repositories live",
		Long: `First-time setup. Choose your default coding agent and the directories that
hold your repositories (Git or not), and create config.yaml.

With repository directories configured, workspaces can name repositories by
folder name ("payments-api") instead of full paths. Add more directories
later by editing "repoDirs:" in config.yaml.

Run it once. Afterwards, add workspaces with "polyroot workspace add" or edit
config.yaml by hand.`,
		Example: "  polyroot setup\n  polyroot setup --agent codex --repo-dir ~/git --repo-dir ~/work",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file := a.dirs.File()
			if _, err := os.Stat(file); err == nil {
				fmt.Fprintf(a.stdout, "Polyroot is already set up: %s\n\nAdd a workspace:  polyroot workspace add <name> <repo>...\nChange settings by editing the file.\n", file)
				return nil
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			interactive := isTerminal()
			if agentFlag == "" && !interactive {
				return errors.New("no terminal for the interactive prompts; pass --agent <name> (and optionally --repo-dir <dir>)")
			}

			choice := agentFlag
			if choice == "" {
				if choice, err = promptAgent(); err != nil {
					return err
				}
			}
			if _, err := agent.Lookup(nil, choice); err != nil {
				return err
			}

			if !cmd.Flags().Changed("repo-dir") && interactive {
				if repoDirs, err = promptRepoDirs(home); err != nil {
					return err
				}
			}
			var display []string
			for _, d := range repoDirs {
				p, err := config.ExpandPath(d, home)
				if err != nil {
					return err
				}
				if st, err := os.Stat(p); err != nil || !st.IsDir() {
					return fmt.Errorf("repository directory %s does not exist", p)
				}
				display = append(display, configedit.DisplayPath(p, home))
			}

			doc := configedit.NewDocument()
			doc.SetDefaultAgent(choice)
			if len(display) > 0 {
				doc.SetRepoDirs(display)
			}
			if err := doc.Save(file, agent.BuiltinNames()); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Created %s\n  default agent:    %s\n", file, choice)
			if len(display) > 0 {
				fmt.Fprintf(a.stdout, "  repo directories: %s\n", strings.Join(display, ", "))
			}
			fmt.Fprintln(a.stdout, "\nNext, create a workspace (the first repository is where the agent starts):\n  polyroot workspace add <name> <repo> <repo>...")
			if len(display) > 0 {
				fmt.Fprintln(a.stdout, "Repositories can be folder names from your repo directories, e.g. payments-api.")
			}
			fmt.Fprintln(a.stdout, "\nTab completion: see `polyroot completion --help`.")
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "default agent (skips the prompt): "+strings.Join(agent.BuiltinNames(), ", "))
	cmd.Flags().StringSliceVar(&repoDirs, "repo-dir", nil, "directory holding your repositories (repeatable; skips the prompt)")
	return cmd
}

func promptAgent() (string, error) {
	choice := ""
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
	err := ask(huh.NewSelect[string]().
		Title("Which coding agent should Polyroot use by default?").
		Description("You can still pick another one per launch with --agent.").
		Options(opts...).Value(&choice))
	return choice, err
}

// commonRepoDirs are suggested when they exist.
var commonRepoDirs = []string{"git", "code", "src", "projects", "dev", "repos", "workspace", "Developer"}

func promptRepoDirs(home string) ([]string, error) {
	var found []string
	for _, d := range commonRepoDirs {
		if st, err := os.Stat(filepath.Join(home, d)); err == nil && st.IsDir() {
			found = append(found, "~/"+d)
		}
	}
	value := strings.Join(found, ", ")
	err := ask(huh.NewInput().
		Title("Where do you keep your code?").
		Description("One or more directories holding your repositories or project folders, comma-separated.\nWorkspaces can then use folder names instead of paths. Leave empty to always use paths.").
		Placeholder("~/git, ~/work").
		Validate(func(s string) error {
			for _, d := range splitList(s) {
				p, err := config.ExpandPath(d, home)
				if err != nil {
					return err
				}
				if st, err := os.Stat(p); err != nil || !st.IsDir() {
					return fmt.Errorf("%s does not exist", d)
				}
			}
			return nil
		}).Value(&value))
	return splitList(value), err
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
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
				_, cfg, err := a.loadForEdit()
				if err != nil {
					return err
				}
				if name, paths, notes, err = a.promptWorkspace(cmd.Root(), cfg, name); err != nil {
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
		if p := taken[raw]; p != "" {
			return raw, nil // registered earlier in this run
		}
		n, p, err := cfg.ResolveRepo(raw, cwd)
		if err != nil {
			return "", err
		}
		if n != "" {
			return n, nil
		}
		for n, existing := range taken {
			if existing == p {
				return n, nil
			}
		}
		n = configedit.RepoName(p, taken)
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
		if ws.Primary, err = nameFor(primaryArg); err != nil {
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
func (a *app) promptWorkspace(root *cobra.Command, cfg *config.Config, name string) (string, []string, string, error) {
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
		title := "Primary repository (the agent starts here)"
		if len(paths) > 0 {
			title = "Another repository (leave empty to finish)"
		}
		hint := "A folder name from your repoDirs, a registered repo name, or a path."
		if len(cfg.RepoDirPaths) > 0 {
			hint = "A folder name from " + strings.Join(cfg.RepoDirs, ", ") + ", a registered repo name, or a path. Tab completes."
		}
		if err := ask(huh.NewInput().Title(title).Description(hint).
			Suggestions(cfg.RepoCandidates()).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					if len(paths) == 0 {
						return errors.New("enter at least one repository")
					}
					return nil
				}
				_, _, err := cfg.ResolveRepo(s, cwd)
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
