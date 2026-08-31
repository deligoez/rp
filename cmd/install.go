package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/spf13/cobra"
)

var installDryRun bool

var installCmd = &cobra.Command{
	Use:   "install [repo]",
	Short: "Run install commands defined in the manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCommandCmd(commandCmdSpec{
			name:       "install",
			verb:       "installing",
			heading:    "Installing",
			pastTense:  "installed  ",
			dryRun:     &installDryRun,
			commandsOf: func(r manifest.RepoEntry) []string { return r.Install },
		}, args)
	},
}

// depsCmd is a hidden migration stub for the removed "rp deps" command.
var depsCmd = &cobra.Command{
	Use:    "deps",
	Short:  "Removed: use 'rp install' or 'rp update' instead",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stderr, `"rp deps" has been replaced by "rp install" and "rp update".`)
		os.Exit(2)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().BoolVar(&installDryRun, "dry-run", false, "preview install commands without executing them")

	rootCmd.AddCommand(depsCmd)
}
