package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/drellahq/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

var taskListAll bool
var taskListJSON bool

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks in the output directory",
	RunE:  runTaskList,
}

func init() {
	taskListCmd.Flags().BoolVar(&taskListAll, "all", false, "include directories without state.json")
	taskListCmd.Flags().BoolVar(&taskListJSON, "json", false, "output JSON")
	taskCmd.AddCommand(taskListCmd)
}

func runTaskList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	summaries, err := task.List(cfg.OutputDir, taskListAll)
	if err != nil {
		return err
	}

	if taskListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summaries)
	}

	if len(summaries) == 0 {
		fmt.Println("No tasks found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tSANDBOX\tOPEN_PRS\tCREATED")
	for _, s := range summaries {
		sandbox := "active"
		if s.SandboxDestroyed {
			sandbox = "destroyed"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			s.Name,
			emptyDash(s.Status),
			sandbox,
			s.OpenPRCount,
			emptyDash(s.CreatedAt),
		)
	}
	return w.Flush()
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
