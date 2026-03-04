package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		taskID        string
		all           bool
		retry         bool
		maxRetries    int
		autoSerialize bool
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

			if all && autoSerialize {
				tasks, err := store.Load()
				if err != nil {
					return fmt.Errorf("loading tasks for auto-serialize: %w", err)
				}
				tasks, edgesAdded := task.AutoSerializeOverlaps(tasks)
				if edgesAdded > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "[dispatch] Auto-serialized %d overlapping task pair(s)\n", edgesAdded)
					if err := store.Save(tasks); err != nil {
						return fmt.Errorf("saving serialized tasks: %w", err)
					}
				}
			}

			if all {
				if retry {
					return dispatchAllWithRetry(cmd.Context(), ws, store, cmd.OutOrStdout(), maxRetries)
				}
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
	cmd.Flags().BoolVar(&retry, "retry", false, "automatically retry failed tasks with error context")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 2, "maximum retry rounds (used with --retry)")
	cmd.Flags().BoolVar(&autoSerialize, "auto-serialize", false, "automatically serialize tasks with overlapping artifacts")

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

	// Record the branch name so the merge process can find it.
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
	windowName := target.ID // window name = task ID
	aptSession := session.ApartmentSession

	// Persist window name to task metadata so attach/status can find it.
	if err := store.Update(target.ID, func(t *task.Task) {
		if t.Meta == nil {
			t.Meta = make(map[string]string)
		}
		t.Meta["session"] = windowName // keep key as "session" for compat
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record session: %v\n", err)
	}

	result, err := runner.Run(ctx, agent.RunOpts{
		Prompt:           target.Prompt,
		SystemPrompt:     systemPrompt,
		WorkDir:          workDir,
		Model:            ws.Config.Model,
		LogFile:          logFile,
		WindowName:       windowName,
		ApartmentSession: aptSession,
		Socket:           socket,
	})

	finishedAt := time.Now()

	if err != nil {
		// Parse usage even on failure.
		usage, _ := agent.ParseUsageFromLog(logFile)

		if updateErr := store.Update(target.ID, func(t *task.Task) {
			t.Status = task.StatusFailed
			t.Error = err.Error()
			t.Result = result.Output
			t.FinishedAt = &finishedAt
			if t.Meta == nil {
				t.Meta = make(map[string]string)
			}
			t.Meta["session"] = ""
			if usage.InputTokens > 0 {
				t.Meta["input_tokens"] = fmt.Sprintf("%d", usage.InputTokens)
				t.Meta["output_tokens"] = fmt.Sprintf("%d", usage.OutputTokens)
			}
			if usage.TotalCostUSD > 0 {
				t.Meta["cost_usd"] = fmt.Sprintf("%.4f", usage.TotalCostUSD)
			}
		}); updateErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to update failed task: %v\n", updateErr)
		}
		// NOTE: Intentionally NOT killing the window on failure.
		// The user can attach to inspect what went wrong.
		return fmt.Errorf("task %q failed: %w", target.ID, err)
	}

	// Parse usage from log file.
	usage, _ := agent.ParseUsageFromLog(logFile)

	if err := store.Update(target.ID, func(t *task.Task) {
		t.Status = task.StatusDone
		t.Result = result.Output
		t.FinishedAt = &finishedAt
		if t.Meta == nil {
			t.Meta = make(map[string]string)
		}
		t.Meta["session"] = ""
		if usage.InputTokens > 0 {
			t.Meta["input_tokens"] = fmt.Sprintf("%d", usage.InputTokens)
			t.Meta["output_tokens"] = fmt.Sprintf("%d", usage.OutputTokens)
		}
		if usage.TotalCostUSD > 0 {
			t.Meta["cost_usd"] = fmt.Sprintf("%.4f", usage.TotalCostUSD)
		}
	}); err != nil {
		return fmt.Errorf("updating task result: %w", err)
	}

	// Auto-close the window on success.
	mgr := session.NewTmuxManager(socket)
	if killErr := mgr.KillWindow(ctx, aptSession, windowName); killErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close window %q: %v\n", windowName, killErr)
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

	// Check for artifact overlaps between independent tasks.
	tasks, err := store.Load()
	if err != nil {
		return fmt.Errorf("loading tasks: %w", err)
	}
	if overlaps := task.OverlapWarnings(tasks); len(overlaps) > 0 {
		fmt.Fprintln(out, "[dispatch] ⚠ Artifact overlap warnings:")
		for _, o := range overlaps {
			fmt.Fprintf(out, "  %s is modified by independent tasks %q and %q\n", o.File, o.TaskA, o.TaskB)
		}
		fmt.Fprintln(out, "  Consider adding dependencies to serialize these tasks.")
		fmt.Fprintln(out, "")
	}

	sem := make(chan struct{}, maxWorkers)
	done := make(chan string, maxWorkers)
	inFlight := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Start Abadonna — the silent monitor for stall/loop detection.
	wdState := newAbadonnaState()
	wdCfg := defaultAbadonnaConfig()
	wdCtx, wdCancel := context.WithCancel(ctx)
	defer wdCancel()

	go func() {
		ticker := time.NewTicker(wdCfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				alerts := wdState.check(wdCfg)
				for _, alert := range alerts {
					mu.Lock()
					fmt.Fprintf(out, "[abadonna] Killing task %q: %s\n", alert.taskID, alert.reason)
					if alert.context != "" {
						fmt.Fprintf(out, "[abadonna] Context for %q:\n%s\n", alert.taskID, alert.context)
					}
					mu.Unlock()

					// Record the failure reason.
					_ = store.Update(alert.taskID, func(t *task.Task) {
						now := time.Now()
						t.Status = task.StatusFailed
						errMsg := "abadonna: " + alert.reason
						if alert.context != "" {
							errMsg += "\n\nContext:\n" + alert.context
						}
						t.Error = errMsg
						t.FinishedAt = &now
						if t.Meta == nil {
							t.Meta = make(map[string]string)
						}
						t.Meta["session"] = ""
					})

					// Kill the tmux window.
					socket := "retinue-" + ws.Config.Name
					killMgr := session.NewTmuxManager(socket)
					_ = killMgr.KillWindow(wdCtx, session.ApartmentSession, alert.taskID)

					// Remove from Abadonna's watch.
					wdState.removeTask(alert.taskID)
				}
			case <-wdCtx.Done():
				return
			}
		}
	}()

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

				logFile := filepath.Join(ws.LogsPath(), t.ID+".log")
				wdState.addTask(t.ID, logFile)

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

				wdState.removeTask(t.ID)

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
	tasks, _ = store.Load()
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

	wtPath := filepath.Join(worktreeDir, t.ID)

	// If the worktree already exists, reuse it.
	if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
		return wtPath, nil
	}

	wtMgr := worktree.NewManager(&worktree.RealGit{}, worktreeDir)
	createdPath, err := wtMgr.Create(ctx, repoAbsPath, t.ID, "")
	if err != nil {
		return "", err
	}

	return createdPath, nil
}

// dispatchAllWithRetry runs dispatchAll, then checks for failed tasks.
// For each failed task, it resets status to pending with the error
// context appended to the prompt, then runs another dispatch round.
// Repeats up to maxRetries times.
func dispatchAllWithRetry(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, out io.Writer, maxRetries int) error {
	for round := 0; round <= maxRetries; round++ {
		if round > 0 {
			fmt.Fprintf(out, "\n[dispatch] === Retry round %d/%d ===\n", round, maxRetries)
		}

		if err := dispatchAll(ctx, ws, store, out); err != nil {
			return err
		}

		if round == maxRetries {
			break // don't retry after the last round
		}

		// Check for failed tasks that can be retried.
		tasks, err := store.Load()
		if err != nil {
			return fmt.Errorf("loading tasks for retry: %w", err)
		}

		var retried int
		for _, t := range tasks {
			if t.Status != task.StatusFailed {
				continue
			}

			// Try smart re-planning first.
			errContext := t.Error
			var revisedPrompt string
			replanRes, replanErr := replanFailedTask(ctx, t, ws.Config.Model, ws.LogsPath())

			if replanErr != nil {
				// Fall back to mechanical retry.
				fmt.Fprintf(out, "[dispatch] Re-plan failed for %q (%v), using mechanical retry\n", t.ID, replanErr)
				revisedPrompt = t.Prompt + "\n\n## Previous Attempt Failed\n" +
					"The previous attempt at this task failed with the following error:\n```\n" +
					errContext + "\n```\n" +
					"Please try a different approach or fix the issue described above."
			} else {
				revisedPrompt = replanRes.RevisedPrompt
				fmt.Fprintf(out, "[dispatch] Re-planned task %q (used %s)\n", t.ID, replanRes.Usage)
			}

			if err := store.Update(t.ID, func(tk *task.Task) {
				tk.Status = task.StatusPending
				tk.Error = ""
				tk.Result = ""
				tk.StartedAt = nil
				tk.FinishedAt = nil
				tk.Prompt = revisedPrompt
				if tk.Meta == nil {
					tk.Meta = make(map[string]string)
				}
				// Record re-plan usage in metadata.
				if replanErr == nil {
					if replanRes.Usage.InputTokens > 0 {
						tk.Meta["replan_input_tokens"] = fmt.Sprintf("%d", replanRes.Usage.InputTokens)
						tk.Meta["replan_output_tokens"] = fmt.Sprintf("%d", replanRes.Usage.OutputTokens)
					}
					if replanRes.Usage.TotalCostUSD > 0 {
						tk.Meta["replan_cost_usd"] = fmt.Sprintf("%.4f", replanRes.Usage.TotalCostUSD)
					}
				}
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to reset task %q for retry: %v\n", t.ID, err)
				continue
			}

			retried++
			fmt.Fprintf(out, "[dispatch] Reset task %q for retry (error: %s)\n", t.ID, truncate(errContext, 80))
		}

		if retried == 0 {
			fmt.Fprintf(out, "[dispatch] No failed tasks to retry.\n")
			break
		}
	}

	return nil
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func loadWorkspace() (*workspace.Workspace, error) {
	if workspaceFlag != "" {
		return workspace.Load(workspaceFlag)
	}
	return workspace.Detect()
}
