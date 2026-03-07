package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

// newRunCmd returns a command that dispatches, merges, and monitors
// tasks in a single autonomous loop.
func newRunCmd() *cobra.Command {
	var (
		retry      bool
		maxRetries int
		review     bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Dispatch, merge, and monitor tasks in a single loop",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())

			if retry {
				return runAllWithRetry(cmd.Context(), ws, store, cmd.OutOrStdout(), maxRetries, review)
			}
			return runAll(cmd.Context(), ws, store, cmd.OutOrStdout(), review)
		},
	}

	cmd.Flags().BoolVar(&retry, "retry", false, "enable automatic retry of failed tasks")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 2, "max retry rounds (used with --retry)")
	cmd.Flags().BoolVar(&review, "review", false, "enable AI review before merging")

	return cmd
}

// runAll runs an iterative dispatch+merge loop until all tasks are
// complete or no more progress can be made.
func runAll(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, out io.Writer, review bool) error {
	maxWorkers := ws.Config.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = workspace.DefaultMaxWorkers
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
		// === MERGE PHASE ===
		// Reload tasks from disk.
		tasks, err := store.Load()
		if err != nil {
			return fmt.Errorf("loading tasks: %w", err)
		}

		for _, t := range tasks {
			if t.Status != task.StatusDone {
				continue
			}
			if t.Branch != "" && t.Repo != "" {
				result := mergeOne(ctx, mergeOneOpts{
					ws:      ws,
					store:   store,
					t:       t,
					review:  review,
					archive: false,
					out:     out,
				})
				if result.Merged {
					fmt.Fprintf(out, "[run] Merged task %q\n", t.ID)
				} else if result.Rejected {
					fmt.Fprintf(out, "[run] Task %q rejected by review, will re-dispatch\n", t.ID)
				} else if result.Err != nil {
					fmt.Fprintf(out, "[run] Task %q merge failed\n", t.ID)
				}
			} else {
				// Repo-less task — mark merged without archive.
				markTaskMergedNoArchive(store, t.ID)
				fmt.Fprintf(out, "[run] Marked repo-less task %q as merged\n", t.ID)
			}
		}

		// === DISPATCH PHASE ===
		// Reload tasks (state may have changed from merges).
		tasks, err = store.Load()
		if err != nil {
			return fmt.Errorf("loading tasks: %w", err)
		}

		ready := task.Ready(tasks)

		// Filter out already in-flight tasks.
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

		// Launch ready tasks concurrently.
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
				fmt.Fprintf(out, "[run] Starting task %q\n", t.ID)
				mu.Unlock()

				if err := dispatchOne(ctx, ws, store, &t, io.Discard); err != nil {
					mu.Lock()
					fmt.Fprintf(out, "[run] Task %q failed: %v\n", t.ID, err)
					mu.Unlock()
				} else {
					mu.Lock()
					fmt.Fprintf(out, "[run] Task %q done\n", t.ID)
					mu.Unlock()
				}

				wdState.removeTask(t.ID)

				mu.Lock()
				delete(inFlight, t.ID)
				mu.Unlock()

				done <- t.ID
			}()
		}

		// Wait for at least one task to complete before looping.
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

	// === POST-LOOP CLEANUP ===

	// Final merge pass — merge any tasks that completed in the last iteration.
	tasks, err := store.Load()
	if err != nil {
		return fmt.Errorf("loading tasks for final merge: %w", err)
	}
	for _, t := range tasks {
		if t.Status != task.StatusDone {
			continue
		}
		if t.Branch != "" && t.Repo != "" {
			result := mergeOne(ctx, mergeOneOpts{
				ws:      ws,
				store:   store,
				t:       t,
				review:  review,
				archive: false,
				out:     out,
			})
			if result.Merged {
				fmt.Fprintf(out, "[run] Final merge: task %q merged\n", t.ID)
			}
		} else {
			markTaskMergedNoArchive(store, t.ID)
			fmt.Fprintf(out, "[run] Final merge: repo-less task %q marked merged\n", t.ID)
		}
	}

	// Archive all merged tasks.
	tasks, err = store.Load()
	if err != nil {
		return fmt.Errorf("loading tasks for archive: %w", err)
	}
	var toArchive []string
	for _, t := range tasks {
		if t.Status == task.StatusMerged {
			toArchive = append(toArchive, t.ID)
		}
	}
	for _, id := range toArchive {
		if err := store.Archive(id, ws.ArchivePath()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: archive %q: %v\n", id, err)
		}
	}

	// Print summary.
	printRunSummary(ws, store, out)

	return nil
}

// runAllWithRetry runs runAll in a loop, retrying failed tasks up to
// maxRetries times with AI re-planning.
func runAllWithRetry(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, out io.Writer, maxRetries int, review bool) error {
	for round := 0; round <= maxRetries; round++ {
		if round > 0 {
			fmt.Fprintf(out, "\n[run] === Retry round %d/%d ===\n", round, maxRetries)
		}

		if err := runAll(ctx, ws, store, out, review); err != nil {
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
				fmt.Fprintf(out, "[run] Re-plan failed for %q (%v), using mechanical retry\n", t.ID, replanErr)
				revisedPrompt = t.Prompt + "\n\n## Previous Attempt Failed\n" +
					"The previous attempt at this task failed with the following error:\n```\n" +
					errContext + "\n```\n" +
					"Please try a different approach or fix the issue described above."
			} else {
				revisedPrompt = replanRes.RevisedPrompt
				fmt.Fprintf(out, "[run] Re-planned task %q (used %s)\n", t.ID, replanRes.Usage)
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
			fmt.Fprintf(out, "[run] Reset task %q for retry (error: %s)\n", t.ID, truncate(errContext, 80))
		}

		if retried == 0 {
			fmt.Fprintf(out, "[run] No failed tasks to retry.\n")
			break
		}
	}

	return nil
}

// printRunSummary prints a summary of the run including counts
// of merged, failed, and pending tasks plus total cost.
func printRunSummary(ws *workspace.Workspace, store *task.FileStore, out io.Writer) {
	var merged, failed, pending int
	var totalCost float64

	// Count active tasks.
	tasks, _ := store.Load()
	for _, t := range tasks {
		switch t.Status {
		case task.StatusMerged:
			merged++
		case task.StatusFailed:
			failed++
		case task.StatusPending:
			pending++
		}
		if costStr, ok := t.Meta["cost_usd"]; ok {
			if c, err := strconv.ParseFloat(costStr, 64); err == nil {
				totalCost += c
			}
		}
	}

	// Count archived tasks (they were merged and archived).
	archivePath := ws.ArchivePath()
	archiveStore := task.NewFileStore(archivePath)
	archived, err := archiveStore.Load()
	if err == nil {
		for _, t := range archived {
			if t.Status == task.StatusMerged {
				merged++
			}
			if costStr, ok := t.Meta["cost_usd"]; ok {
				if c, err := strconv.ParseFloat(costStr, 64); err == nil {
					totalCost += c
				}
			}
		}
	}

	fmt.Fprintf(out, "\nRun complete. %d merged, %d failed, %d pending. Total cost: $%.2f\n", merged, failed, pending, totalCost)
}
