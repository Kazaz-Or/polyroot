package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Kazaz-Or/polyroot/internal/update"
)

func (a *app) updateCmd(version string) *cobra.Command {
	var check, force bool
	var want string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update polyroot to the latest release",
		Long: `Download the latest polyroot release from GitHub and replace this binary.

The archive for your OS and CPU is verified against the release's
checksums.txt before anything is replaced. This is the only command that
uses the network.`,
		Example: "  polyroot update\n  polyroot update --check\n  polyroot update --version v0.2.0",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				exe = real
			}
			releases := update.DefaultReleases
			if v := os.Getenv("POLYROOT_RELEASES_URL"); v != "" { // for testing against a mirror
				releases = v
			}
			o := update.Options{Releases: releases, Current: version, Target: exe}

			tag := want
			if tag == "" {
				if tag, err = update.Latest(ctx, o); err != nil {
					return err
				}
			} else if tag[0] != 'v' {
				tag = "v" + tag
			}
			if check {
				fmt.Fprintf(a.stdout, "current: %s\nlatest:  %s\n", version, tag)
				if update.IsRelease(version) && update.Newer(tag, version) {
					fmt.Fprintln(a.stdout, "\nRun `polyroot update` to upgrade.")
				}
				return nil
			}
			if !update.IsRelease(version) && !force {
				return fmt.Errorf("this is a development build (version %s); pass --force to replace %s with release %s", version, exe, tag)
			}
			if want == "" && !force && !update.Newer(tag, version) {
				fmt.Fprintf(a.stdout, "polyroot %s is up to date.\n", version)
				return nil
			}
			if !a.quiet {
				fmt.Fprintf(a.stdout, "Updating polyroot %s → %s ...\n", version, tag)
			}
			if err := update.Install(ctx, o, tag); err != nil {
				return errors.Join(errors.New("update failed"), err)
			}
			fmt.Fprintf(a.stdout, "Updated %s to %s.\n", exe, tag)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report the current and latest versions")
	cmd.Flags().StringVar(&want, "version", "", "install this release tag instead of the latest (also allows downgrades)")
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even if up to date, or replace a development build")
	return cmd
}
