package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/spf13/cobra"
)

// Exported package-level variables so subcommands can read the resolved values.
var (
	ManifestPath string
	Concurrency  int
	NoColor      bool
	JSONOutput   bool
	Compact      bool
	Filters      []string
)

var rootCmd = &cobra.Command{
	Use:   "rp",
	Short: "Repo manager CLI — organize, sync, and bootstrap your Developer workspace",

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// 1. --manifest: CLI flag > RP_MANIFEST env > default
		if !cmd.Flags().Changed("manifest") {
			if v := os.Getenv("RP_MANIFEST"); v != "" {
				ManifestPath = v
			}
		}

		// 2. --concurrency: CLI flag > RP_CONCURRENCY env > default
		if !cmd.Flags().Changed("concurrency") {
			if v := os.Getenv("RP_CONCURRENCY"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n >= 1 {
					Concurrency = n
				}
				// invalid values are silently ignored; default remains
			}
		}

		// 3. --json: CLI flag > RP_JSON env > default
		if !cmd.Flags().Changed("json") {
			if os.Getenv("RP_JSON") != "" {
				JSONOutput = true
			}
		}
		output.SetJSON(JSONOutput)
		if JSONOutput {
			NoColor = true
		}

		// 4. --compact: wire to output package
		output.SetCompact(Compact)

		// 5. --no-color: CLI flag > NO_COLOR env > default
		if !cmd.Flags().Changed("no-color") {
			if os.Getenv("NO_COLOR") != "" {
				NoColor = true
			}
		}

		// 6. Wire --no-color to ui package
		ui.SetNoColor(NoColor)

		// 7. Validate concurrency >= 1
		if Concurrency < 1 {
			return fmt.Errorf("--concurrency must be >= 1, got %d", Concurrency)
		}

		// 8. Expand ~ in ManifestPath
		if strings.HasPrefix(ManifestPath, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("could not determine home directory: %w", err)
			}
			ManifestPath = strings.Replace(ManifestPath, "~", home, 1)
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(
		&ManifestPath,
		"manifest", "m",
		"~/.config/rp/manifest.yaml",
		"path to manifest file (env: RP_MANIFEST)",
	)

	rootCmd.PersistentFlags().IntVarP(
		&Concurrency,
		"concurrency", "c",
		4,
		"number of concurrent workers, must be >= 1 (env: RP_CONCURRENCY)",
	)

	rootCmd.PersistentFlags().BoolVar(
		&NoColor,
		"no-color",
		false,
		"disable color output (env: NO_COLOR)",
	)

	rootCmd.PersistentFlags().BoolVar(
		&JSONOutput,
		"json",
		false,
		"output results as JSON (env: RP_JSON)",
	)

	rootCmd.PersistentFlags().BoolVar(
		&Compact,
		"compact",
		false,
		"omit per-repo details from JSON output",
	)

	rootCmd.PersistentFlags().StringArrayVar(
		&Filters,
		"filter",
		nil,
		"filter repos by tag or name (repeatable)",
	)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("rp", err)
		}
		fmt.Fprintln(os.Stderr, output.FormatHumanError(err))
		os.Exit(2)
	}
}
