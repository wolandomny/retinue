package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
	"github.com/wolandomny/retinue/internal/worktree"
)

func newDispatchCmd() *cobra.Command {
	var taskID string

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch ready tasks to Claude Code agents",
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

			var target *task.Task
			if taskID != "" {
				for i := range tasks {
					if tasks[i].ID == taskID {
						target = &tasks[i]
						break
					}
				}
				if target == nil {
					return fmt.Errorf("task %q not found", taskID)
				}
				if target.Status != task.StatusPending {
					return fmt.Errorf("task %q is %s, not pending", taskID, target.Status)
				}
			} else {
				ready := task.Ready(tasks)
				if len(ready) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No tasks ready for dispatch.")
					return nil
				}
				target = &ready[0]
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Dispatching task %q...\n", target.ID)

			// Update status to in_progress.
			now := time.Now()
			if err := store.Update(target.ID, func(t *task.Task) {
				t.Status = task.StatusInProgress
				t.StartedAt = &now
			}); err != nil {
				return fmt.Errorf("updating task status: %w", err)
			}

			// Determine working directory.
			workDir, err := resolveWorkDir(cmd.Context(), ws, target)
			if err != nil {
				return fmt.Errorf("resolving work directory: %w", err)
			}

			// Record the branch name so the review process can find it.
		if target.Repo != "" {
			_ = store.Update(target.ID, func(t *task.Task) {
				t.Branch = "retinue/" + target.ID
			})
		}

		// Build system prompt.
			systemPrompt := fmt.Sprintf(
				"You are a worker agent in the Retinue system. Your task ID is %q. "+
					"Complete the following task thoroughly and report your results. "+
					"Focus only on this task.\n\n"+
					"IMPORTANT: After completing your work, you MUST commit all changes to git. "+
					"Stage your files with `git add` and create a commit with a clear, descriptive message. "+
					"Do not leave work uncommitted.",
				target.ID,
			)

			socket := "retinue-" + ws.Config.Name
			runner := agent.NewTmuxRunner(session.NewTmuxManager(socket))
			logFile := filepath.Join(ws.LogsPath(), target.ID+".log")
			sessionName := "retinue-" + target.ID

			// Persist session name to task metadata so attach/status can find it.
			_ = store.Update(target.ID, func(t *task.Task) {
				if t.Meta == nil {
					t.Meta = make(map[string]string)
				}
				t.Meta["session"] = sessionName
			})

			result, err := runner.Run(cmd.Context(), agent.RunOpts{
				Prompt:       target.Prompt,
				SystemPrompt: systemPrompt,
				WorkDir:      workDir,
				Model:        ws.Config.Model,
				LogFile:      logFile,
				SessionName:  sessionName,
				Socket:       socket,
			})

			finishedAt := time.Now()

			if err != nil {
				_ = store.Update(target.ID, func(t *task.Task) {
					t.Status = task.StatusFailed
					t.Error = err.Error()
					t.Result = result.Output
					t.FinishedAt = &finishedAt
					t.Meta["session"] = ""
				})
				return fmt.Errorf("task %q failed: %w", target.ID, err)
			}

			if err := store.Update(target.ID, func(t *task.Task) {
				t.Status = task.StatusDone
				t.Result = result.Output
				t.FinishedAt = &finishedAt
				t.Meta["session"] = ""
			}); err != nil {
				return fmt.Errorf("updating task result: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Task %q completed successfully.\n", target.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "specific task ID to dispatch")

	return cmd
}

// resolveWorkDir determines the working directory for a task. If the task has
// a Repo field, a git worktree is created so the task runs in isolation.
func resolveWorkDir(ctx context.Context, ws *workspace.Workspace, t *task.Task) (string, error) {
	if t.Repo == "" {
		return ws.Path, nil
	}

	repoPath, ok := ws.Config.Repos[t.Repo]
	if !ok {
		return ws.Path, nil
	}

	repoAbsPath := filepath.Join(ws.Path, repoPath)

	worktreeDir := filepath.Join(ws.Path, workspace.WorktreeDir)
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		return "", fmt.Errorf("creating worktrees directory: %w", err)
	}

	wtMgr := worktree.NewManager(&worktree.RealGit{}, worktreeDir)
	wtPath, err := wtMgr.Create(ctx, repoAbsPath, t.ID, "")
	if err != nil {
		return "", err
	}

	return wtPath, nil
}

func loadWorkspace() (*workspace.Workspace, error) {
	if workspaceFlag != "" {
		return workspace.Load(workspaceFlag)
	}
	return workspace.Detect()
}
