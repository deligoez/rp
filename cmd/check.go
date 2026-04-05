package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit 0 if all repos are clean and cloned, 1 otherwise",
	RunE:  runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

func runCheck(_ *cobra.Command, _ []string) error {
	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	repos := manifest.FilterRepos(m.Repos(), Filters)

	for _, entry := range repos {
		if !git.IsRepo(entry.LocalPath) {
			os.Exit(1)
		}

		s, err := git.Status(entry.LocalPath)
		if err != nil {
			os.Exit(1)
		}

		if needsAttention(s) {
			os.Exit(1)
		}
	}

	return nil
}
