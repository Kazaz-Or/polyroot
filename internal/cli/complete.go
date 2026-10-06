package cli

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kazaz-Or/polyroot/internal/agent"
	"github.com/Kazaz-Or/polyroot/internal/config"
)

type completer = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)

// completionConfig loads the config for shell completion. Completion runs
// without PersistentPreRunE, so it resolves the config location itself and
// stays silent on errors.
func completionConfig() *config.Config {
	dirs, err := config.DefaultDirs()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(dirs.File())
	if err != nil {
		return nil
	}
	return cfg
}

func withPrefix(items []string, prefix string, exclude []string) []string {
	var out []string
	for _, it := range items {
		if strings.HasPrefix(it, prefix) && !slices.Contains(exclude, it) {
			out = append(out, it)
		}
	}
	return out
}

// completeWorkspace completes a single workspace-name argument.
func completeWorkspace(_ *app) completer {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg := completionConfig()
		if cfg == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return withPrefix(config.SortedKeys(cfg.Workspaces), toComplete, nil), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeAgents completes --agent values: built-in and custom agents.
func completeAgents(_ *app) completer {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		names := agent.BuiltinNames()
		if cfg := completionConfig(); cfg != nil {
			for _, n := range config.SortedKeys(cfg.Agents) {
				if !slices.Contains(names, n) {
					names = append(names, n)
				}
			}
		}
		return withPrefix(names, toComplete, nil), cobra.ShellCompDirectiveNoFileComp
	}
}

// looksLikePath reports whether the user is typing a path, not a name.
func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "$")
}

// completeRepos completes repository references: registered repo names and
// folders in repoDirs; directories when the user is typing a path.
func completeRepos(toComplete string, exclude []string) ([]string, cobra.ShellCompDirective) {
	if looksLikePath(toComplete) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	cfg := completionConfig()
	if cfg == nil {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	return withPrefix(cfg.RepoCandidates(), toComplete, exclude), cobra.ShellCompDirectiveNoFileComp
}

// completeWorkspaceAdd: first an existing workspace name (or a new one),
// then repositories.
func completeWorkspaceAdd(a *app) completer {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeWorkspace(a)(cmd, args, toComplete)
		}
		return completeRepos(toComplete, args[1:])
	}
}
