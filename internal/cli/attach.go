package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
)

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <task-id>",
		Short: "Attach to the tmux window of a running task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]

			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())
			t, err := store.Get(taskID)
			if err != nil {
				return fmt.Errorf("task %q not found", taskID)
			}

			if t.Status != task.StatusInProgress {
				fmt.Fprintf(cmd.OutOrStdout(), "task %q is not running (status: %s)\n", taskID, t.Status)
				return nil
			}

			windowName := t.Meta["session"]
			if windowName == "" {
				return fmt.Errorf("task %q has no active window", taskID)
			}

			socket := "retinue-" + ws.Config.Name
			aptSession := session.ApartmentSession

			tmuxBin, err := exec.LookPath("tmux")
			if err != nil {
				return fmt.Errorf("tmux not found in PATH: %w", err)
			}

			// Attach to the apartment session, targeting the task's window.
			target := aptSession + ":" + windowName
			return syscall.Exec(tmuxBin,
				[]string{"tmux", "-L", socket, "attach-session", "-t", target},
				os.Environ())
		},
	}

	return cmd
}
