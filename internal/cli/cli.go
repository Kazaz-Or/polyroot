// Package cli implements the polyroot command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kazaz-Or/polyroot/internal/agent"
	"github.com/Kazaz-Or/polyroot/internal/config"
	"github.com/Kazaz-Or/polyroot/internal/workspace"
)

// ExitError carries an exit code (e.g. the agent's) without extra output.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

type app struct {
	quiet, verbose bool
	dirs           config.Dirs
	stdout, stderr io.Writer
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	root := a.root(version)
	err := root.Execute()
	var exit ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	default:
		fmt.Fprintf(a.stderr, "polyroot: %v\n", err)
		return 1
	}
}

func (a *app) root(version string) *cobra.Command {
	var rootAgent string
	root := &cobra.Command{
		Short: "Multi-repo workspaces for coding agents",
		Use:   "polyroot [workspace] [-- agent args...]",
		Long: `Polyroot opens a logical multi-repository codebase in a coding agent
(Claude Code, Codex CLI, Gemini CLI, OpenCode, Pi, or your own).

Quick start:
  polyroot setup                                      once: choose your default agent
  polyroot workspace add payments ~/git/api ~/git/web create a workspace (first path = primary)
  polyroot payments                                   open it (same as: polyroot open payments)

Workspace commands:
  polyroot workspace add <name> <repo-path>...        create a workspace, or add repos to one
  polyroot workspace remove <name>                    remove a workspace

Opening:
  polyroot <workspace> [--agent NAME] [-- agent args...]
  Agent choice: --agent, then the workspace's defaultAgent, then the global defaultAgent.
  Everything after -- is passed to the agent unchanged.

Configuration:
  $POLYROOT_CONFIG_HOME/config.yaml, else $XDG_CONFIG_HOME/polyroot/config.yaml,
  else ~/.config/polyroot/config.yaml. Edit it by hand any time; polyroot keeps
  your changes. Generated launch files go to ~/.cache/polyroot
  ($POLYROOT_CACHE_HOME, $XDG_CACHE_HOME).

Docs: https://github.com/Kazaz-Or/polyroot`,
		Example: `  polyroot payments
  polyroot payments --agent codex -- --model o3
  polyroot command payments --agent gemini     # show exactly what would run
  polyroot workspace add payments ~/git/helm   # add a repo to a workspace`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return a.open(cmd, args, rootAgent)
		},
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if a.quiet && a.verbose {
				return errors.New("--quiet and --verbose are mutually exclusive")
			}
			var err error
			a.dirs, err = config.DefaultDirs()
			return err
		},
	}
	root.Flags().StringVarP(&rootAgent, "agent", "a", "", "agent to launch when opening a workspace")
	root.PersistentFlags().BoolVarP(&a.quiet, "quiet", "q", false, "print nothing but errors")
	root.PersistentFlags().BoolVarP(&a.verbose, "verbose", "v", false, "print more detail")
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.AddGroup(
		&cobra.Group{ID: "start", Title: "Set up:"},
		&cobra.Group{ID: "use", Title: "Use workspaces:"},
		&cobra.Group{ID: "check", Title: "Inspect and troubleshoot:"},
	)
	for group, cmds := range map[string][]*cobra.Command{
		"start": {a.setupCmd(), a.workspaceCmd()},
		"use":   {a.openCmd(), a.listCmd(), a.showCmd(), a.commandCmd()},
		"check": {a.validateCmd(), a.agentsCmd(), a.doctorCmd()},
	} {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	return root
}

// loadConfig loads the config file and fails on any structural problem.
func (a *app) loadConfig() (*config.Config, error) {
	cfg, err := config.Load(a.dirs.File())
	if err != nil {
		return nil, err
	}
	if errs := cfg.Check(agent.BuiltinNames()); len(errs) > 0 {
		return nil, fmt.Errorf("invalid config %s:\n%s", cfg.File, bullets(errs))
	}
	return cfg, nil
}

func bullets(errs []error) string {
	var lines []string
	for _, e := range errs {
		lines = append(lines, "  - "+e.Error())
	}
	return strings.Join(lines, "\n")
}

// splitDash splits positional args from those after `--`.
func splitDash(cmd *cobra.Command, args []string) (pos, rest []string) {
	if n := cmd.ArgsLenAtDash(); n >= 0 {
		return args[:n], args[n:]
	}
	return args, nil
}

type launch struct {
	cfg     *config.Config
	ws      *workspace.Resolved
	adapter agent.Adapter
	spec    *agent.LaunchSpec
}

func (a *app) prepare(cmd *cobra.Command, args []string, agentFlag string) (*launch, error) {
	pos, rest := splitDash(cmd, args)
	if len(pos) != 1 {
		return nil, fmt.Errorf("expected exactly one workspace name before `--`, got %d (see `polyroot list`)", len(pos))
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Resolve(cfg, pos[0])
	if err != nil {
		return nil, err
	}
	name, err := cfg.AgentFor(ws.Name, agentFlag)
	if err != nil {
		return nil, err
	}
	ad, err := agent.Lookup(cfg, name)
	if err != nil {
		return nil, err
	}
	spec, err := ad.Build(ws, rest, agent.NewRunDir(a.dirs.Cache))
	if err != nil {
		return nil, err
	}
	return &launch{cfg: cfg, ws: ws, adapter: ad, spec: spec}, nil
}

// open launches the agent for the workspace in args[0].
func (a *app) open(cmd *cobra.Command, args []string, agentFlag string) error {
	l, err := a.prepare(cmd, args, agentFlag)
	if err != nil {
		return err
	}
	agent.CleanStaleRuns(a.dirs.Cache, 7*24*time.Hour)
	if a.verbose {
		agent.Print(a.stderr, l.spec, "")
		fmt.Fprintln(a.stderr)
	} else if !a.quiet {
		fmt.Fprintf(a.stderr, "Workspace: %s\nAgent: %s\nRepositories: %d\nPrimary: %s\n",
			l.ws.Name, l.spec.Agent, len(l.ws.Repos), tilde(l.ws.Primary().Path))
		for _, w := range l.spec.Warnings {
			fmt.Fprintf(a.stderr, "Warning: %s\n", w)
		}
		fmt.Fprintf(a.stderr, "\nLaunching %s...\n", l.adapter.Info().Display)
	}
	code, err := agent.Run(l.spec)
	if err != nil {
		return err
	}
	if code != 0 {
		return ExitError{code}
	}
	return nil
}

func (a *app) openCmd() *cobra.Command {
	var agentFlag string
	cmd := &cobra.Command{
		Use:   "open <workspace> [-- agent args...]",
		Short: "Open a workspace in a coding agent",
		Long: `Open a workspace in a coding agent.

Agent selection: --agent, then the workspace's defaultAgent, then the global
defaultAgent. Arguments after -- are passed to the agent unchanged.`,
		Example: "  polyroot open payments\n  polyroot open payments --agent claude -- --model opus",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.open(cmd, args, agentFlag)
		},
	}
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "agent to launch (overrides defaultAgent)")
	return cmd
}

func (a *app) commandCmd() *cobra.Command {
	var agentFlag string
	cmd := &cobra.Command{
		Use:   "command <workspace> [-- agent args...]",
		Short: "Print the exact launch specification without launching",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.prepare(cmd, args, agentFlag)
			if err != nil {
				return err
			}
			agent.Print(a.stdout, l.spec, "")
			if a.verbose {
				for _, f := range l.spec.Files {
					fmt.Fprintf(a.stdout, "\n--- %s\n%s", f.Path, f.Content)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "agent to describe (overrides defaultAgent)")
	return cmd
}

func (a *app) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured workspaces",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(a.stdout, 0, 4, 3, ' ', 0)
			fmt.Fprintln(tw, "NAME\tPRIMARY\tREPOS\tDEFAULT AGENT")
			for _, name := range config.SortedKeys(cfg.Workspaces) {
				names, _, _ := workspace.Members(cfg, name)
				ag, err := cfg.AgentFor(name, "")
				if err != nil {
					ag = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", name, cfg.Workspaces[name].Primary, len(names), ag)
			}
			return tw.Flush()
		},
	}
}

func (a *app) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <workspace>",
		Short: "Show the fully resolved workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			names, via, err := workspace.Members(cfg, args[0])
			if err != nil {
				return err
			}
			ws := cfg.Workspaces[args[0]]
			primary := cfg.Repos[ws.Primary]
			w := a.stdout
			fmt.Fprintf(w, "Workspace: %s\n\nPrimary:\n  %s\n  %s\n\nRepositories:\n", args[0], ws.Primary, displayPath(primary))
			tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
			broken := 0
			for i, n := range names {
				r := cfg.Repos[n]
				note := ""
				if via[i] != "" {
					note = "[" + via[i] + "]"
				}
				if r.PathErr != nil {
					broken++
					note = strings.TrimSpace(note + " ERROR: " + r.PathErr.Error())
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\n", n, displayPath(r), note)
			}
			tw.Flush()
			if ws.ContextPath != "" {
				fmt.Fprintf(w, "\nContext:\n  %s\n", tilde(ws.ContextPath))
			}
			if ag, err := cfg.AgentFor(args[0], ""); err == nil {
				fmt.Fprintf(w, "\nDefault agent:\n  %s\n", ag)
			}
			if broken > 0 {
				return fmt.Errorf("%d repositories are unusable; fix their paths in %s", broken, cfg.File)
			}
			return nil
		},
	}
}

func displayPath(r *config.Repo) string {
	if r.Path != "" {
		return r.Path
	}
	return r.Raw
}

func (a *app) validateCmd() *cobra.Command {
	var agentFlag string
	cmd := &cobra.Command{
		Use:   "validate [workspace]",
		Short: "Validate the configuration and workspaces",
		Long: `Validate the configuration, every workspace (or just one), the repository
paths, and whether the effective agent can represent each workspace.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load(a.dirs.File())
			if err != nil {
				return err
			}
			if errs := cfg.Check(agent.BuiltinNames()); len(errs) > 0 {
				return fmt.Errorf("invalid config %s:\n%s", cfg.File, bullets(errs))
			}
			names := config.SortedKeys(cfg.Workspaces)
			if len(args) == 1 {
				if cfg.Workspaces[args[0]] == nil {
					return fmt.Errorf("unknown workspace %q (see `polyroot list`)", args[0])
				}
				names = args
			}
			failed := 0
			for _, name := range names {
				errs, warns := validateWorkspace(cfg, name, agentFlag, a.dirs.Cache)
				for _, w := range warns {
					if !a.quiet {
						fmt.Fprintf(a.stdout, "warn  %s: %s\n", name, w)
					}
				}
				for _, e := range errs {
					fmt.Fprintf(a.stdout, "error %s: %s\n", name, e)
				}
				if len(errs) > 0 {
					failed++
				} else if !a.quiet {
					fmt.Fprintf(a.stdout, "ok    %s\n", name)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d workspaces are invalid", failed, len(names))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "check against this agent instead of each workspace's default")
	return cmd
}

// validateWorkspace resolves a workspace and dry-builds its launch spec.
func validateWorkspace(cfg *config.Config, name, agentFlag, cacheDir string) (errs, warns []error) {
	ws, err := workspace.Resolve(cfg, name)
	if err != nil {
		return []error{err}, nil
	}
	for _, r := range ws.Repos {
		if !workspace.IsGitRepo(r.Path) {
			warns = append(warns, fmt.Errorf("%s (%s) is not a Git repository", r.Name, r.Path))
		}
	}
	agName, err := cfg.AgentFor(name, agentFlag)
	if err != nil {
		return nil, append(warns, err)
	}
	ad, err := agent.Lookup(cfg, agName)
	if err != nil {
		return []error{err}, warns
	}
	spec, err := ad.Build(ws, nil, agent.NewRunDir(cacheDir))
	if err != nil {
		return []error{fmt.Errorf("agent %s: %w", agName, err)}, warns
	}
	for _, w := range spec.Warnings {
		warns = append(warns, fmt.Errorf("agent %s: %s", agName, w))
	}
	return nil, warns
}

// tilde shortens a path under $HOME for display.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return p
}
