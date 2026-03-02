package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
)

func newLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List active tmux windows in this apartment",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			socket := "retinue-" + ws.Config.Name
			mgr := session.NewTmuxManager(socket)
			ctx := context.Background()
			aptSession := session.ApartmentSession

			windows, err := mgr.ListWindows(ctx, aptSession)
			if err != nil {
				return fmt.Errorf("listing windows: %w", err)
			}

			if len(windows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No active sessions.")
				return nil
			}

			// Load tasks to cross-reference status.
			store := task.NewFileStore(ws.TasksPath())
			tasks, _ := store.Load() // ignore error, tasks are optional context
			taskStatus := make(map[string]string)
			for _, t := range tasks {
				taskStatus[t.ID] = string(t.Status)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "WINDOW\tSTATUS")
			fmt.Fprintln(w, "------\t------")

			for _, win := range windows {
				status := ""
				if win == "woland" {
					status = "planning"
				} else if s, ok := taskStatus[win]; ok {
					status = s
				} else {
					status = "active"
				}
				fmt.Fprintf(w, "%s\t%s\n", win, status)
			}

			return w.Flush()
		},
	}

	return cmd
}
