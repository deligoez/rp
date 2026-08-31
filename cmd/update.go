package cmd

import (
	"github.com/deligoez/rp/internal/manifest"
	"github.com/spf13/cobra"
)

var updateDryRun bool

var updateCmd = &cobra.Command{
	Use:   "update [repo]",
	Short: "Run update commands defined in the manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCommandCmd(commandCmdSpec{
			name:       "update",
			verb:       "updating",
			heading:    "Updating",
			pastTense:  "updated    ",
			dryRun:     &updateDryRun,
			commandsOf: func(r manifest.RepoEntry) []string { return r.Update },
		}, cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "preview update commands without executing them")
}
