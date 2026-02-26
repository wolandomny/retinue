package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/task"
)

const maxDescriptionLen = 60

// newStatusCmd returns a command that displays the current status of
// all tasks in a tabular format.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of all tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())
			tasks, err := store.Load()
			if err != nil {
				return err
			}

			if len(tasks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tasks found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tREPO\tDESCRIPTION")
			fmt.Fprintln(w, "--\t------\t----\t-----------")

			for _, t := range tasks {
				desc := t.Description
				if len(desc) > maxDescriptionLen {
					desc = desc[:maxDescriptionLen-3] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Repo, desc)
			}

			return w.Flush()
		},
	}

	return cmd
}
