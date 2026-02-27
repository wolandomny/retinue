package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
	"github.com/wolandomny/retinue/internal/worktree"
)

// newDispatchCmd returns a command that dispatches the next ready task
// (or a specific task by ID) to a Claude Code agent.
func newDispatchCmd() *cobra.Command {
	var (
		taskID string
		all    bool
	)

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch ready tasks to Claude Code agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())

			if all {
				return dispatchAll(cmd.Context(), ws, store, cmd.OutOrStdout())
			}

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

			return dispatchOne(cmd.Context(), ws, store, target, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "specific task ID to dispatch")
	cmd.Flags().BoolVar(&all, "all", false, "dispatch all ready tasks and continue until done")

	return cmd
}

// dispatchOne dispatches a single task to a Claude Code agent. It updates the
// task status, creates a worktree if needed, runs the agent, and records the
// result.
func dispatchOne(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, target *task.Task, out io.Writer) error {
	fmt.Fprintf(out, "Dispatching task %q...\n", target.ID)

	// Update status to in_progress.
	now := time.Now()
	if err := store.Update(target.ID, func(t *task.Task) {
		t.Status = task.StatusInProgress
		t.StartedAt = &now
	}); err != nil {
		return fmt.Errorf("updating task status: %w", err)
	}

	// Determine working directory.
	workDir, err := resolveWorkDir(ctx, ws, target)
	if err != nil {
		return fmt.Errorf("resolving work directory: %w", err)
	}

	// Record the branch name so the review process can find it.
	if target.Repo != "" {
		if err := store.Update(target.ID, func(t *task.Task) {
			t.Branch = "retinue/" + target.ID
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to record branch: %v\n", err)
		}
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
	if err := store.Update(target.ID, func(t *task.Task) {
		if t.Meta == nil {
			t.Meta = make(map[string]string)
		}
		t.Meta["session"] = sessionName
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record session: %v\n", err)
	}

	result, err := runner.Run(ctx, agent.RunOpts{
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
		if updateErr := store.Update(target.ID, func(t *task.Task) {
			t.Status = task.StatusFailed
			t.Error = err.Error()
			t.Result = result.Output
			t.FinishedAt = &finishedAt
			t.Meta["session"] = ""
		}); updateErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to update failed task: %v\n", updateErr)
		}
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

	fmt.Fprintf(out, "Task %q completed successfully.\n", target.ID)
	return nil
}

// dispatchAll runs a concurrent scheduler that dispatches all ready tasks,
// waits for completions, and dispatches newly-unblocked tasks until no
// pending work remains. Respects the workspace's MaxWorkers concurrency limit.
func dispatchAll(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, out io.Writer) error {
	maxWorkers := ws.Config.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = workspace.DefaultMaxWorkers
	}

	sem := make(chan struct{}, maxWorkers)
	done := make(chan string, maxWorkers)
	inFlight := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for {
		// Reload tasks from disk (authoritative state).
		tasks, err := store.Load()
		if err != nil {
			return fmt.Errorf("loading tasks: %w", err)
		}

		ready := task.Ready(tasks)

		// Filter out tasks already in-flight.
		var toDispatch []task.Task
		mu.Lock()
		for _, t := range ready {
			if !inFlight[t.ID] {
				toDispatch = append(toDispatch, t)
			}
		}
		mu.Unlock()

		// If nothing to dispatch and nothing in-flight, we're done.
		mu.Lock()
		nInFlight := len(inFlight)
		mu.Unlock()

		if len(toDispatch) == 0 && nInFlight == 0 {
			break
		}

		// Launch new tasks.
		for i := range toDispatch {
			t := toDispatch[i]

			mu.Lock()
			inFlight[t.ID] = true
			mu.Unlock()

			sem <- struct{}{} // acquire worker slot
			wg.Add(1)

			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				mu.Lock()
				fmt.Fprintf(out, "[dispatch] Starting task %q\n", t.ID)
				mu.Unlock()

				if err := dispatchOne(ctx, ws, store, &t, io.Discard); err != nil {
					mu.Lock()
					fmt.Fprintf(out, "[dispatch] Task %q failed: %v\n", t.ID, err)
					mu.Unlock()
				} else {
					mu.Lock()
					fmt.Fprintf(out, "[dispatch] Task %q done\n", t.ID)
					mu.Unlock()
				}

				mu.Lock()
				delete(inFlight, t.ID)
				mu.Unlock()

				done <- t.ID
			}()
		}

		// Wait for at least one task to complete before rechecking.
		mu.Lock()
		hasInFlight := len(inFlight) > 0
		mu.Unlock()

		if hasInFlight {
			select {
			case <-done:
			case <-ctx.Done():
				wg.Wait()
				return ctx.Err()
			}
		}
	}

	wg.Wait()

	// Drain done channel.
drainDone:
	for {
		select {
		case <-done:
		default:
			break drainDone
		}
	}

	// Print summary.
	tasks, _ := store.Load()
	var succeeded, failed, pending int
	for _, t := range tasks {
		switch t.Status {
		case task.StatusDone, task.StatusMerged:
			succeeded++
		case task.StatusFailed:
			failed++
		case task.StatusPending:
			pending++
		}
	}

	fmt.Fprintf(out, "\n[dispatch] Complete. %d succeeded, %d failed, %d still pending.\n", succeeded, failed, pending)
	return nil
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
