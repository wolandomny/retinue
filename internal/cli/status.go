package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/task"
)

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

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tREPO\tDESCRIPTION")
			fmt.Fprintln(w, "--\t------\t----\t-----------")

			for _, t := range tasks {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Repo, desc)
			}

			return w.Flush()
		},
	}

	return cmd
}
