package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate the manifest file structure",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate(_ *cobra.Command, args []string) error {
	ui.SetNoColor(NoColor)

	path := ManifestPath
	if len(args) == 1 {
		path = args[0]
	}

	m, err := manifest.Load(path)
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("validate", err)
		}
		fmt.Fprintln(os.Stderr, output.FormatHumanError(err))
		os.Exit(2)
	}

	repos := m.Repos()
	owners := m.Owners()

	reposCount := len(repos)
	ownersCount := len(owners)

	categoriesCount := 0
	for _, ow := range owners {
		if ow.IsFlat {
			continue
		}
		seen := map[string]bool{}
		for _, r := range ow.Repos {
			if !seen[r.Category] {
				seen[r.Category] = true
				categoriesCount++
			}
		}
	}

	installCmds := 0
	updateCmds := 0
	for _, r := range repos {
		installCmds += len(r.Install)
		updateCmds += len(r.Update)
	}

	if output.IsJSON() {
		output.PrintAndExit(output.SuccessResult{
			Command:  "validate",
			ExitCode: 0,
			Summary: map[string]interface{}{
				"valid":            true,
				"repos":            reposCount,
				"owners":           ownersCount,
				"categories":       categoriesCount,
				"install_commands": installCmds,
				"update_commands":  updateCmds,
			},
			Repos: []interface{}{},
		})
	}

	fmt.Printf("%s %s valid\n", ui.SymbolOK(), ui.Plural(reposCount, "repo"))
	return nil
}
