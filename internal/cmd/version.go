package cmd

import (
	"fmt"
	"os"

	"github.com/drellabot/orchestrator/internal/version"
	"github.com/spf13/cobra"
)

var (
	versionJSON   bool
	versionOutput string
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE:  runVersion,
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "output as JSON")
	versionCmd.Flags().StringVarP(&versionOutput, "output", "o", "", "write JSON to file (implies --json)")
}

func runVersion(cmd *cobra.Command, args []string) error {
	info := version.Get()

	if versionOutput != "" {
		data, err := info.JSON()
		if err != nil {
			return fmt.Errorf("marshaling version info: %w", err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(versionOutput, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", versionOutput, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", versionOutput)
		return nil
	}

	if versionJSON {
		data, err := info.JSON()
		if err != nil {
			return fmt.Errorf("marshaling version info: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}

	for name, comp := range info.Components {
		commit := comp.Commit
		if commit == "" {
			commit = "dev"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, commit)
	}
	return nil
}
