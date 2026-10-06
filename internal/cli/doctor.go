package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Kazaz-Or/polyroot/internal/agent"
	"github.com/Kazaz-Or/polyroot/internal/config"
)

// detectAll probes every adapter concurrently; one failure never affects another.
func detectAll(adapters []agent.Adapter) []agent.Detection {
	out := make([]agent.Detection, len(adapters))
	var wg sync.WaitGroup
	for i, ad := range adapters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = agent.Detect(context.Background(), ad.Info().Binary)
		}()
	}
	wg.Wait()
	return out
}

func (a *app) agentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "Show coding agents, their versions and capabilities",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, _ := config.Load(a.dirs.File()) // custom agents are optional here
			adapters := agent.All(cfg)
			dets := detectAll(adapters)
			tw := tabwriter.NewWriter(a.stdout, 0, 4, 3, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tINSTALLED\tVERSION\tMULTI-ROOT\tCONTEXT\tREPO INSTRUCTIONS\tSTATUS")
			for i, ad := range adapters {
				info, d := ad.Info(), dets[i]
				installed, version := "no", "-"
				if d.Installed() {
					installed = "yes"
				}
				if d.Version != "" {
					version = d.Version
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", ad.Name(), installed, version,
					info.MultiRoot, info.Context, info.RepoInstructions, agent.Status(ad, d))
			}
			tw.Flush()
			if a.verbose {
				for i, ad := range adapters {
					for _, p := range ad.Check(dets[i]) {
						fmt.Fprintf(a.stdout, "\n%s: %s", ad.Name(), p)
					}
				}
				fmt.Fprintln(a.stdout)
			}
			return nil
		},
	}
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, workspaces and installed agents",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			w := a.stdout
			problems := 0
			bad := func(format string, args ...any) {
				problems++
				fmt.Fprintf(w, "  ✗ "+format+"\n", args...)
			}
			good := func(format string, args ...any) { fmt.Fprintf(w, "  ✓ "+format+"\n", args...) }
			note := func(format string, args ...any) { fmt.Fprintf(w, "  ! "+format+"\n", args...) }

			fmt.Fprintln(w, "Configuration")
			good("config dir: %s", a.dirs.Config)
			good("cache dir:  %s", a.dirs.Cache)
			cfg, err := config.Load(a.dirs.File())
			if err != nil {
				bad("%v", err)
			} else if errs := cfg.Check(agent.BuiltinNames()); len(errs) > 0 {
				for _, e := range errs {
					bad("%v", e)
				}
				cfg = nil
			} else {
				good("%s parses and all references resolve", cfg.File)
			}

			used := map[string]bool{}
			if cfg != nil {
				fmt.Fprintln(w, "\nWorkspaces")
				if len(cfg.Workspaces) == 0 {
					note("no workspaces defined")
				}
				for _, name := range config.SortedKeys(cfg.Workspaces) {
					if ag, err := cfg.AgentFor(name, ""); err == nil {
						used[ag] = true
					}
					errs, warns := validateWorkspace(cfg, name, "", a.dirs.Cache)
					for _, e := range errs {
						bad("%s: %v", name, indent(e.Error()))
					}
					for _, e := range warns {
						note("%s: %v", name, e)
					}
					if len(errs) == 0 {
						good("%s", name)
					}
				}
			}

			fmt.Fprintln(w, "\nAgents")
			adapters := agent.All(cfg)
			for i, d := range detectAll(adapters) {
				ad := adapters[i]
				info := ad.Info()
				if !d.Installed() {
					if used[ad.Name()] {
						bad("%s: %s is not installed but is a configured default agent", ad.Name(), info.Binary)
					} else {
						note("%s: not installed (optional)", ad.Name())
					}
					continue
				}
				version := d.Version
				if version == "" {
					version = "unknown version"
				}
				probs := ad.Check(d)
				if d.Err != nil {
					probs = append(probs, d.Err.Error())
				}
				if len(probs) == 0 {
					good("%s %s (%s)", ad.Name(), version, d.Path)
					continue
				}
				note("%s %s (%s)", ad.Name(), version, d.Path)
				for _, p := range probs {
					if len(d.MissingFlags(info.RequiredFlags)) > 0 {
						bad("    %s", p)
					} else {
						note("    %s", p)
					}
				}
			}

			if problems > 0 {
				return ExitError{1}
			}
			fmt.Fprintln(w, "\nNo problems found.")
			return nil
		},
	}
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n    ") }
