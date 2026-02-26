package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/wolandomny/retinue/internal/task"
)

// newAttachCmd returns a command that attaches the user's terminal to
// a running task's tmux session.
func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <task-id>",
		Short: "Attach to the tmux session of a running task",
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

			sessionName := t.Meta["session"]
			if sessionName == "" {
				return fmt.Errorf("task %q has no active session", taskID)
			}

			socket := "retinue-" + ws.Config.Name

			tmuxBin, err := exec.LookPath("tmux")
			if err != nil {
				return fmt.Errorf("tmux not found in PATH: %w", err)
			}

			return syscall.Exec(tmuxBin, []string{"tmux", "-L", socket, "attach-session", "-t", sessionName}, os.Environ())
		},
	}

	return cmd
}
