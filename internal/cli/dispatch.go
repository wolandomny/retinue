package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
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
		taskID     string
		all        bool
		retry      bool
		maxRetries int
	)

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch ready tasks to Claude Code agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			// Resolve GitHub token early so it's cached for worktree creation before any workers start.
			if _, err := ws.ResolveGitHubToken(); err != nil {
				log.Printf("warning: failed to resolve GitHub token: %v", err)
			}

			store := task.NewFileStore(ws.TasksPath())

			if all {
				if retry {
					return dispatchAllWithRetry(cmd.Context(), ws, store, cmd.OutOrStdout(), maxRetries)
				}
				return dispatchAll(cmd.Context(), ws, store, cmd.OutOrStdout(), nil)
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

			return dispatchOne(cmd.Context(), ws, store, target, cmd.OutOrStdout(), nil)
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "specific task ID to dispatch")
	cmd.Flags().BoolVar(&all, "all", false, "dispatch all ready tasks and continue until done")
	cmd.Flags().BoolVar(&retry, "retry", false, "automatically retry failed tasks with error context")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 2, "maximum retry rounds (used with --retry)")
	return cmd
}

// dispatchOne dispatches a single task to a Claude Code agent. It updates the
// task status, creates a worktree if needed, runs the agent, and records the
// result.
//
// If runner is nil, a real tmux-backed runner is constructed (the production
// path). Tests may inject a fake runner to exercise the dispatch logic without
// spawning real `claude` processes; this is the only behavioral effect of the
// parameter.
func dispatchOne(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, target *task.Task, out io.Writer, runner agent.Runner) error {
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
			"Do not leave work uncommitted.\n\n"+
			"You cannot ask the user questions; state your assumptions and proceed, or "+
			"surface blockers in your result for Woland to handle.",
		target.ID,
	)

	// Append commit style instructions if configured for this repo.
	if target.Repo != "" {
		if repoCfg, ok := ws.Config.Repos[target.Repo]; ok {
			systemPrompt += commitStylePrompt(repoCfg.CommitStyle)
		}
	}

	// Inject dependency context if this task has predecessors.
	if len(target.DependsOn) > 0 {
		if depContext := buildDependencyContext(store, target.DependsOn); depContext != "" {
			systemPrompt += "\n\n" + depContext
		}
	}

	socket := "retinue-" + ws.Config.Name
	if runner == nil {
		runner = agent.NewTmuxRunner(session.NewTmuxManager(socket))
	}
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

	// Use task-level model if set, otherwise fall back to workspace default.
	model := ws.Config.Model
	if target.Model != "" {
		model = target.Model
	}

	// Resolve the effort level and any governance env for this worker. This
	// downgrades ultracode to xhigh when the workspace hasn't opted in, and
	// disables nested dynamic workflows for non-ultracode workers so they
	// can't bypass the max_workers semaphore.
	effortLevel, govEnv := resolveWorkerLaunch(target, ws)

	// Inject GitHub token into the agent's environment if available.
	var extraEnv []string
	if token := ws.GitHubToken(); token != "" {
		extraEnv = append(extraEnv, "GH_TOKEN="+token)
	}
	extraEnv = append(extraEnv, govEnv...)

	result, err := runner.Run(ctx, agent.RunOpts{
		Prompt:           target.Prompt,
		SystemPrompt:     systemPrompt,
		WorkDir:          workDir,
		Model:            model,
		Effort:           effortLevel,
		LogFile:          logFile,
		WindowName:       windowName,
		ApartmentSession: aptSession,
		Socket:           socket,
		Env:              extraEnv,
		DisallowedTools:  "AskUserQuestion",
	})

	finishedAt := time.Now()

	// A runner-level error (e.g. tmux/worktree plumbing failed) is always a
	// failure: there is nothing to classify. Record it and leave the window for
	// inspection.
	if err != nil {
		recordWorkerFailed(store, target.ID, finishedAt, err.Error(), result.Output, effortLevel, logFile, ws.Config.TrackCosts)
		// NOTE: Intentionally NOT killing the window on failure.
		// The user can attach to inspect what went wrong.
		return fmt.Errorf("task %q failed: %w", target.ID, err)
	}

	// The runner returned without a transport error. The {"type":"result"} event
	// it parsed (carried in result.Output) is NON-authoritative for success — it
	// only supplies result TEXT. Whether the task succeeded is decided here.
	if target.Repo != "" {
		// COMMIT PRESENCE is the authority for repo tasks. A worker is successful
		// iff its task branch (retinue/<id>) has commits beyond the base branch:
		//   - commits exist -> done, even if the result event never flushed
		//     (incident 1: real work no longer discarded as a false FAILURE).
		//   - no commits    -> failed, even if it emitted output such as an error
		//     string (incident 2: phantom/errored runs never silently merged).
		baseBranch := task.ResolveBaseBranch(*target, ws.Config.Repos)
		commits, countErr := countBranchCommits(ctx, workDir, baseBranch)
		if countErr != nil {
			// We could not determine commit presence (e.g. worktree vanished).
			// Fail safe: do NOT mark done, so a phantom can never be merged.
			errMsg := fmt.Sprintf("could not verify task commits: %v", countErr)
			recordWorkerFailed(store, target.ID, finishedAt, errMsg, result.Output, effortLevel, logFile, ws.Config.TrackCosts)
			return fmt.Errorf("task %q failed: %s", target.ID, errMsg)
		}

		if commits == 0 {
			// No committed work: failure regardless of output. Preserve whatever
			// the worker emitted for inspection, but do NOT kill the window.
			errMsg := "worker produced no commits on the task branch; not marking done"
			recordWorkerFailed(store, target.ID, finishedAt, errMsg, result.Output, effortLevel, logFile, ws.Config.TrackCosts)
			return fmt.Errorf("task %q failed: worker produced no commits", target.ID)
		}

		// Commits exist: success. Prefer the captured result text; if it never
		// flushed, synthesize a note so the recovery is visible.
		resultText := result.Output
		if strings.TrimSpace(resultText) == "" {
			resultText = fmt.Sprintf("recovered: %d commit(s), no captured result event", commits)
		}
		recordWorkerDone(store, target.ID, finishedAt, resultText, effortLevel, logFile, ws.Config.TrackCosts)
	} else {
		// No repo, hence no branch to inspect: fall back to the output-based
		// empty-output guard. An empty result is treated as a fast/empty failure
		// rather than a phantom success.
		if strings.TrimSpace(result.Output) == "" {
			errMsg := "worker produced no output (possible fast failure); not marking done"
			recordWorkerFailed(store, target.ID, finishedAt, errMsg, result.Output, effortLevel, logFile, ws.Config.TrackCosts)
			// NOTE: Intentionally NOT killing the window so the empty/fast failure
			// can be inspected.
			return fmt.Errorf("task %q failed: worker produced no output", target.ID)
		}
		recordWorkerDone(store, target.ID, finishedAt, result.Output, effortLevel, logFile, ws.Config.TrackCosts)
	}

	// Auto-close the window on success.
	mgr := session.NewTmuxManager(socket)
	if killErr := mgr.KillWindow(ctx, aptSession, windowName); killErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close window %q: %v\n", windowName, killErr)
	}

	fmt.Fprintf(out, "Task %q completed successfully.\n", target.ID)
	return nil
}

// countBranchCommits returns the number of commits on the worktree's current
// HEAD that are not reachable from baseBranch (i.e. `git rev-list --count
// <base>..HEAD`). This is the same notion of "has the worker produced work"
// that the merge path relies on. A non-nil error means commit presence could
// not be determined and callers must fail safe (never treat as a success).
func countBranchCommits(ctx context.Context, worktreePath, baseBranch string) (int, error) {
	out, err := runGit(ctx, worktreePath, "rev-list", "--count", baseBranch+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return 0, fmt.Errorf("parsing commit count %q: %w", out, convErr)
	}
	return n, nil
}

// recordWorkerDone transitions a task to done with the given result text,
// clearing the live session and recording usage/cost metadata.
func recordWorkerDone(store *task.FileStore, id string, finishedAt time.Time, resultText, effortLevel, logFile string, trackCosts bool) {
	usage, _ := agent.ParseUsageFromLog(logFile)
	if err := store.Update(id, func(t *task.Task) {
		t.Status = task.StatusDone
		t.Error = ""
		t.Result = resultText
		t.FinishedAt = &finishedAt
		applyWorkerMeta(t, effortLevel, usage, trackCosts)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update task result: %v\n", err)
	}
}

// recordWorkerFailed transitions a task to failed, preserving the worker's
// error message and any output it produced for later inspection, and recording
// usage/cost metadata. It never kills the tmux window.
func recordWorkerFailed(store *task.FileStore, id string, finishedAt time.Time, errMsg, output, effortLevel, logFile string, trackCosts bool) {
	usage, _ := agent.ParseUsageFromLog(logFile)
	if err := store.Update(id, func(t *task.Task) {
		t.Status = task.StatusFailed
		t.Error = errMsg
		t.Result = output
		t.FinishedAt = &finishedAt
		applyWorkerMeta(t, effortLevel, usage, trackCosts)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update failed task: %v\n", err)
	}
}

// applyWorkerMeta clears the live session marker and records the applied effort
// and (when cost tracking is on) token/cost usage onto a task's metadata.
func applyWorkerMeta(t *task.Task, effortLevel string, usage agent.UsageSummary, trackCosts bool) {
	if t.Meta == nil {
		t.Meta = make(map[string]string)
	}
	t.Meta["session"] = ""
	t.Meta["effort_applied"] = effortLevel
	if trackCosts {
		if usage.InputTokens > 0 {
			t.Meta["input_tokens"] = fmt.Sprintf("%d", usage.InputTokens)
			t.Meta["output_tokens"] = fmt.Sprintf("%d", usage.OutputTokens)
		}
		if usage.TotalCostUSD > 0 {
			t.Meta["cost_usd"] = fmt.Sprintf("%.4f", usage.TotalCostUSD)
		}
	}
}

// dispatchAll runs a concurrent scheduler that dispatches all ready tasks,
// waits for completions, and dispatches newly-unblocked tasks until no
// pending work remains. Respects the workspace's MaxWorkers concurrency limit.
//
// If runner is nil, each dispatched task constructs its own real tmux-backed
// runner (the production path). Tests may inject a shared fake runner to
// observe concurrency without spawning real `claude` processes.
func dispatchAll(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, out io.Writer, runner agent.Runner) error {
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
	// Ultracode workers get a dedicated, tighter concurrency cap because each
	// one can auto-launch nested dynamic workflows. This is independent of the
	// general worker semaphore.
	maxUltra := ws.Config.MaxUltracodeWorkers
	if maxUltra <= 0 {
		maxUltra = 1
	}
	ultraSem := make(chan struct{}, maxUltra)
	// done is a coalescing wake-up signal: workers send a non-blocking pulse
	// when they finish so the scheduler loop re-evaluates. Buffer 1 is
	// sufficient because the loop re-reads full state on every wake; a larger
	// buffer or a blocking send is unnecessary and was previously a deadlock
	// source when the backlog exceeded the worker cap.
	done := make(chan struct{}, 1)
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

			// Ultracode workers (when the workspace allows them) consume an
			// additional dedicated slot. Acquire ultraSem BEFORE sem and
			// release in reverse order (sem then ultraSem) to avoid deadlock.
			isUltra := ws.Config.AllowUltracode && resolveTaskEffort(&t, ws) == "ultracode"
			if isUltra {
				ultraSem <- struct{}{} // acquire ultracode slot
			}
			sem <- struct{}{} // acquire worker slot
			wg.Add(1)

			go func() {
				defer wg.Done()
				// Release the worker slots when this task finishes. Release order
				// mirrors the reverse of acquisition (sem then ultraSem) to avoid
				// lock-order inversion with the acquire path above.
				defer func() {
					<-sem
					if isUltra {
						<-ultraSem
					}
				}()

				logFile := filepath.Join(ws.LogsPath(), t.ID+".log")
				wdState.addTask(t.ID, logFile)

				mu.Lock()
				fmt.Fprintf(out, "[dispatch] Starting task %q\n", t.ID)
				mu.Unlock()

				if err := dispatchOne(ctx, ws, store, &t, io.Discard, runner); err != nil {
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

				// Wake the scheduler loop to re-evaluate. This is a coalescing
				// pulse (non-blocking send into a buffer-1 channel), NOT a
				// per-task signal: the loop re-reads full state from disk and the
				// inFlight map on each wake, so collapsing several completions
				// into one wake is correct. A blocking send here would be a bug —
				// the loop deletes us from inFlight above, so it can break and
				// reach wg.Wait() while we are still trying to send, deadlocking
				// once the backlog exceeds the worker cap.
				select {
				case done <- struct{}{}:
				default:
				}
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

	repoCfg, ok := ws.Config.Repos[t.Repo]
	if !ok {
		return ws.Path, nil
	}

	repoAbsPath := filepath.Join(ws.Path, repoCfg.Path)

	worktreeDir := filepath.Join(ws.Path, workspace.WorktreeDir)
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		return "", fmt.Errorf("creating worktrees directory: %w", err)
	}

	wtPath := filepath.Join(worktreeDir, t.ID)

	// If the worktree already exists, reuse it.
	if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
		return wtPath, nil
	}

	// Resolve which branch to create the worktree from.
	baseBranch := task.ResolveBaseBranch(*t, ws.Config.Repos)

	var gitEnv []string
	if token := ws.GitHubToken(); token != "" {
		gitEnv = append(gitEnv, "GH_TOKEN="+token)
	}
	wtMgr := worktree.NewManager(&worktree.RealGit{Env: gitEnv}, worktreeDir)
	createdPath, err := wtMgr.Create(ctx, repoAbsPath, t.ID, "", baseBranch)
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

		if err := dispatchAll(ctx, ws, store, out, nil); err != nil {
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
				if ws.Config.TrackCosts && replanErr == nil {
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

// commitStylePrompt returns a system prompt fragment for the given
// commit style. Known keywords are expanded to full instructions;
// unknown non-empty strings are passed through as-is.
func commitStylePrompt(style string) string {
	switch style {
	case "":
		return ""
	case "conventional":
		return "\n\nUse Conventional Commits for all commit messages. " +
			"Each commit message must start with a type prefix: " +
			"feat: (new feature), fix: (bug fix), refactor: (code restructuring), " +
			"test: (adding/updating tests), docs: (documentation), " +
			"chore: (maintenance/tooling), ci: (CI/CD changes), " +
			"perf: (performance improvement), build: (build system). " +
			"Format: \"type: concise imperative description\". " +
			"Examples: \"feat: add watchdog goroutine\", \"fix: nil map in store update\"."
	default:
		return "\n\nCommit message style: " + style
	}
}

// buildDependencyContext creates a system prompt section containing the
// results from completed dependency tasks, summarizing what each dependency
// produced so the worker agent knows what its predecessors accomplished.
// Each dependency's result is truncated to 4000 chars (taking the tail,
// since agent summaries appear at the end of output).
func buildDependencyContext(store *task.FileStore, deps []string) string {
	if len(deps) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Context from Completed Dependencies\n")
	b.WriteString("The following tasks have already been completed and merged:\n\n")

	wrote := false
	for _, depID := range deps {
		t, err := store.Get(depID)
		if err != nil {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n", depID))
		if t.Description != "" {
			b.WriteString(fmt.Sprintf("Description: %s\n", t.Description))
		}
		if t.Result != "" {
			result := t.Result
			const maxLen = 4000
			if len(result) > maxLen {
				result = result[len(result)-maxLen:]
			}
			b.WriteString(fmt.Sprintf("Result: %s\n", result))
		}
		b.WriteString("\n")
		wrote = true
	}

	if !wrote {
		return ""
	}

	return b.String()
}

// resolveTaskEffort returns the effort level to pass to the worker
// claude process for this task. Precedence: task.Effort > workspace.Config.Effort.
// An empty result means "do not pass --effort" (defer to model default).
func resolveTaskEffort(t *task.Task, ws *workspace.Workspace) string {
	if t != nil && t.Effort != "" {
		return t.Effort
	}
	if ws != nil {
		return ws.Config.Effort
	}
	return ""
}

// resolveWorkerLaunch computes the effort level and any extra environment
// variables for a worker launch, applying ultracode governance:
//
//   - If the resolved effort is "ultracode" but the workspace has not opted in
//     via allow_ultracode, the effort is downgraded to "xhigh" and a warning is
//     logged to stderr.
//   - For any worker whose FINAL effort is not "ultracode",
//     CLAUDE_CODE_DISABLE_WORKFLOWS=1 is added so it cannot spawn nested dynamic
//     workflows that would bypass the max_workers semaphore. Ultracode workers
//     (only reachable when allow_ultracode is true) keep workflows enabled.
//
// It is a pure function of (target, ws) aside from the stderr warning, which
// makes the governance independently unit-testable.
func resolveWorkerLaunch(target *task.Task, ws *workspace.Workspace) (effort string, env []string) {
	effort = resolveTaskEffort(target, ws)

	if effort == "ultracode" && (ws == nil || !ws.Config.AllowUltracode) {
		taskID := ""
		if target != nil {
			taskID = target.ID
		}
		fmt.Fprintf(os.Stderr, "warning: task %q requested ultracode but allow_ultracode is false; downgrading to xhigh\n", taskID)
		effort = "xhigh"
	}

	if effort != "ultracode" {
		env = append(env, "CLAUDE_CODE_DISABLE_WORKFLOWS=1")
	}

	return effort, env
}

func loadWorkspace() (*workspace.Workspace, error) {
	if workspaceFlag != "" {
		return workspace.Load(workspaceFlag)
	}
	return workspace.Detect()
}
