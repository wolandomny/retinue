package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

// RecoverOpts configures the behavior of RecoverStuckTasks.
type RecoverOpts struct {
	Force  bool          // reset even if tmux window is alive (kills it)
	Stale  time.Duration // only touch in_progress tasks older than this duration
	Failed bool          // also reset failed tasks to pending
	DryRun bool          // show what would happen without making changes
	TaskID string        // recover a specific task by ID (empty = all)
}

// RecoverResult describes the outcome for a single task.
type RecoverResult struct {
	TaskID string
	Action string // "reset", "done", "failed", "skipped"
	Detail string
}

func newResetCmd() *cobra.Command {
	var (
		all      bool
		taskID   string
		failed   bool
		staleStr string
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Recover tasks stuck in in_progress status",
		Long: `Reset recovers tasks whose worker processes have died (e.g., laptop
sleep, terminal close) leaving them stuck in "in_progress" status.

With no flags (or just --stale), it performs a dry run: showing what it
found and what it would do without making changes.

Use --all or --task to actually perform recovery.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())

			var stale time.Duration
			if staleStr != "" {
				stale, err = time.ParseDuration(staleStr)
				if err != nil {
					return fmt.Errorf("invalid --stale duration: %w", err)
				}
			}

			// Dry run unless --all or --task is specified.
			dryRun := !all && taskID == ""

			opts := RecoverOpts{
				Force:  force,
				Stale:  stale,
				Failed: failed,
				DryRun: dryRun,
				TaskID: taskID,
			}

			results, err := RecoverStuckTasks(cmd.Context(), ws, store, cmd.OutOrStdout(), opts)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tasks to recover.")
			}

			if dryRun && len(results) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "\nDry run — no changes made. Use --all or --task <id> to apply.")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "recover all in_progress tasks with dead workers")
	cmd.Flags().StringVar(&taskID, "task", "", "recover a specific task by ID")
	cmd.Flags().BoolVar(&failed, "failed", false, "also reset failed tasks to pending")
	cmd.Flags().StringVar(&staleStr, "stale", "", "only touch in_progress tasks older than duration (e.g. \"1h\", \"30m\")")
	cmd.Flags().BoolVar(&force, "force", false, "reset even if tmux window is alive (kills it first)")

	return cmd
}

// RecoverStuckTasks examines tasks that are stuck in "in_progress" (and
// optionally "failed") status and recovers them. It is exported so that
// other commands (e.g., retinue run) can call it directly.
func RecoverStuckTasks(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, w io.Writer, opts RecoverOpts) ([]RecoverResult, error) {
	tasks, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("loading tasks: %w", err)
	}

	socket := "retinue-" + ws.Config.Name
	tmuxMgr := session.NewTmuxManager(socket)

	var results []RecoverResult

	for _, t := range tasks {
		// If a specific task was requested, skip others.
		if opts.TaskID != "" && t.ID != opts.TaskID {
			continue
		}

		switch t.Status {
		case task.StatusInProgress:
			// Apply stale filter.
			if opts.Stale > 0 && t.StartedAt != nil {
				if time.Since(*t.StartedAt) < opts.Stale {
					continue
				}
			}

			result, err := recoverInProgressTask(ctx, ws, store, tmuxMgr, t, w, opts)
			if err != nil {
				fmt.Fprintf(w, "%s: error: %v\n", t.ID, err)
				continue
			}
			results = append(results, result)

		case task.StatusFailed:
			if !opts.Failed {
				continue
			}

			result, err := recoverFailedTask(ctx, ws, store, t, w, opts)
			if err != nil {
				fmt.Fprintf(w, "%s: error: %v\n", t.ID, err)
				continue
			}
			results = append(results, result)
		}
	}

	// If a specific task was requested but not found, report an error.
	if opts.TaskID != "" && len(results) == 0 {
		// Check if the task exists at all.
		found := false
		for _, t := range tasks {
			if t.ID == opts.TaskID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("task %q not found", opts.TaskID)
		}
	}

	return results, nil
}

// recoverInProgressTask handles recovery logic for a single in_progress task.
func recoverInProgressTask(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, tmuxMgr *session.TmuxManager, t task.Task, w io.Writer, opts RecoverOpts) (RecoverResult, error) {
	windowName := t.Meta["session"]
	if windowName == "" {
		windowName = t.ID
	}

	// Step 1: Check tmux liveness.
	alive, err := tmuxMgr.HasWindow(ctx, session.ApartmentSession, windowName)
	if err != nil {
		// If we can't check, treat as dead.
		alive = false
	}

	// Step 2: If window is alive.
	if alive {
		if !opts.Force {
			detail := "window alive → skipped (still running)"
			fmt.Fprintf(w, "%s: %s\n", t.ID, detail)
			return RecoverResult{TaskID: t.ID, Action: "skipped", Detail: detail}, nil
		}
		// --force: kill the window first.
		if !opts.DryRun {
			_ = tmuxMgr.KillWindow(ctx, session.ApartmentSession, windowName)
		}
	}

	// Step 3: Check for work on the branch.
	branch := t.Branch
	if branch == "" {
		branch = "retinue/" + t.ID
	}

	repoPath := ws.Path
	if t.Repo != "" {
		if repoCfg, ok := ws.Config.Repos[t.Repo]; ok {
			repoPath = filepath.Join(ws.Path, repoCfg.Path)
		}
	}

	baseBranch := task.ResolveBaseBranch(t, ws.Config.Repos)

	// Check if the branch exists.
	branchExists := false
	if _, err := runGit(ctx, repoPath, "rev-parse", "--verify", branch); err == nil {
		branchExists = true
	}

	commitCount := 0
	if branchExists {
		// Count commits beyond the base branch.
		logOutput, err := runGit(ctx, repoPath, "log", baseBranch+".."+branch, "--oneline")
		if err == nil && strings.TrimSpace(logOutput) != "" {
			commitCount = len(strings.Split(strings.TrimSpace(logOutput), "\n"))
		}
	}

	// Step 4: No branch or no commits — simple reset.
	if !branchExists || commitCount == 0 {
		detail := "window dead, no commits → reset to pending"
		if alive {
			detail = "window killed (--force), no commits → reset to pending"
		}
		fmt.Fprintf(w, "%s: %s\n", t.ID, detail)

		if !opts.DryRun {
			if err := resetTaskToPending(ctx, store, t, ws, repoPath, branch, branchExists); err != nil {
				return RecoverResult{}, fmt.Errorf("resetting task: %w", err)
			}
		}
		return RecoverResult{TaskID: t.ID, Action: "reset", Detail: detail}, nil
	}

	// Step 5: Branch has commits — AI assessment.
	prefix := "window dead"
	if alive {
		prefix = "window killed (--force)"
	}
	fmt.Fprintf(w, "%s: %s, %d commit(s) found → assessing...", t.ID, prefix, commitCount)

	if opts.DryRun {
		detail := fmt.Sprintf("%s, %d commit(s) found → would assess with AI", prefix, commitCount)
		fmt.Fprintf(w, " (dry run, skipped)\n")
		return RecoverResult{TaskID: t.ID, Action: "skipped", Detail: detail}, nil
	}

	verdict, explanation, err := assessBranchWork(ctx, ws, repoPath, branch, baseBranch, t)
	if err != nil {
		// On assessment failure, mark as failed so the user can decide.
		fmt.Fprintf(w, " ERROR (%v) → marked failed\n", err)
		if updateErr := store.Update(t.ID, func(tk *task.Task) {
			now := time.Now()
			tk.Status = task.StatusFailed
			tk.Error = fmt.Sprintf("reset assessment failed: %v", err)
			tk.FinishedAt = &now
			clearRuntimeMeta(tk)
		}); updateErr != nil {
			return RecoverResult{}, updateErr
		}
		detail := fmt.Sprintf("%s, %d commit(s) → assessment error → marked failed", prefix, commitCount)
		return RecoverResult{TaskID: t.ID, Action: "failed", Detail: detail}, nil
	}

	switch verdict {
	case "COMPLETE":
		fmt.Fprintf(w, " COMPLETE → marked done\n")
		if err := store.Update(t.ID, func(tk *task.Task) {
			now := time.Now()
			tk.Status = task.StatusDone
			tk.FinishedAt = &now
			tk.Result = explanation
			clearRuntimeMeta(tk)
		}); err != nil {
			return RecoverResult{}, err
		}
		detail := fmt.Sprintf("%s, %d commit(s) → COMPLETE → marked done (ready for merge)", prefix, commitCount)
		return RecoverResult{TaskID: t.ID, Action: "done", Detail: detail}, nil

	case "INCOMPLETE":
		fmt.Fprintf(w, " INCOMPLETE → marked failed\n")
		if err := store.Update(t.ID, func(tk *task.Task) {
			now := time.Now()
			tk.Status = task.StatusFailed
			tk.Error = explanation
			tk.FinishedAt = &now
			clearRuntimeMeta(tk)
		}); err != nil {
			return RecoverResult{}, err
		}
		detail := fmt.Sprintf("%s, %d commit(s) → INCOMPLETE → marked failed", prefix, commitCount)
		return RecoverResult{TaskID: t.ID, Action: "failed", Detail: detail}, nil

	default: // "BROKEN" or unknown
		fmt.Fprintf(w, " BROKEN → reset to pending\n")
		if err := resetTaskToPending(ctx, store, t, ws, repoPath, branch, branchExists); err != nil {
			return RecoverResult{}, fmt.Errorf("resetting broken task: %w", err)
		}
		detail := fmt.Sprintf("%s, %d commit(s) → BROKEN → reset to pending", prefix, commitCount)
		return RecoverResult{TaskID: t.ID, Action: "reset", Detail: detail}, nil
	}
}

// recoverFailedTask handles resetting a failed task to pending.
func recoverFailedTask(ctx context.Context, ws *workspace.Workspace, store *task.FileStore, t task.Task, w io.Writer, opts RecoverOpts) (RecoverResult, error) {
	detail := "failed → reset to pending"
	fmt.Fprintf(w, "%s: %s\n", t.ID, detail)

	if opts.DryRun {
		return RecoverResult{TaskID: t.ID, Action: "reset", Detail: detail}, nil
	}

	branch := t.Branch
	if branch == "" {
		branch = "retinue/" + t.ID
	}

	repoPath := ws.Path
	if t.Repo != "" {
		if repoCfg, ok := ws.Config.Repos[t.Repo]; ok {
			repoPath = filepath.Join(ws.Path, repoCfg.Path)
		}
	}

	branchExists := false
	if _, err := runGit(ctx, repoPath, "rev-parse", "--verify", branch); err == nil {
		branchExists = true
	}

	if err := resetTaskToPending(ctx, store, t, ws, repoPath, branch, branchExists); err != nil {
		return RecoverResult{}, fmt.Errorf("resetting failed task: %w", err)
	}

	return RecoverResult{TaskID: t.ID, Action: "reset", Detail: detail}, nil
}

// resetTaskToPending performs a full reset of a task to pending status:
// clears runtime fields, removes worktree, prunes, and deletes the branch.
func resetTaskToPending(ctx context.Context, store *task.FileStore, t task.Task, ws *workspace.Workspace, repoPath, branch string, branchExists bool) error {
	// Update task status and clear runtime fields.
	if err := store.Update(t.ID, func(tk *task.Task) {
		tk.Status = task.StatusPending
		tk.Branch = ""
		tk.StartedAt = nil
		tk.FinishedAt = nil
		tk.Result = ""
		tk.Error = ""
		clearRuntimeMeta(tk)
	}); err != nil {
		return fmt.Errorf("updating task: %w", err)
	}

	// Delete worktree directory if it exists (best-effort).
	worktreePath := filepath.Join(ws.Path, workspace.WorktreeDir, t.ID)
	if _, err := os.Stat(worktreePath); err == nil {
		_, _ = runGit(ctx, repoPath, "worktree", "remove", "--force", worktreePath)
	}

	// Prune stale worktree references (best-effort).
	_, _ = runGit(ctx, repoPath, "worktree", "prune")

	// Delete the branch if it exists (best-effort).
	if branchExists {
		_, _ = runGit(ctx, repoPath, "branch", "-D", branch)
	}

	return nil
}

// clearRuntimeMeta removes runtime-generated metadata keys while
// preserving user-defined metadata.
var runtimeMetaKeys = []string{
	"session",
	"input_tokens",
	"output_tokens",
	"cost_usd",
	"replan_input_tokens",
	"replan_output_tokens",
	"replan_cost_usd",
	"review_tokens",
}

func clearRuntimeMeta(t *task.Task) {
	if t.Meta == nil {
		return
	}
	for _, key := range runtimeMetaKeys {
		delete(t.Meta, key)
	}
	// If meta is now empty, nil it out for cleaner YAML.
	if len(t.Meta) == 0 {
		t.Meta = nil
	}
}

// assessBranchWork uses Claude to evaluate whether the work on a branch
// represents complete, incomplete, or broken work relative to the task prompt.
// Returns the verdict (COMPLETE, INCOMPLETE, or BROKEN) and a brief explanation.
func assessBranchWork(ctx context.Context, ws *workspace.Workspace, repoPath, branch, baseBranch string, t task.Task) (string, string, error) {
	// Get the diff between base and branch.
	diff, err := runGit(ctx, repoPath, "diff", baseBranch+"..."+branch)
	if err != nil {
		return "", "", fmt.Errorf("getting diff: %w", err)
	}

	// Truncate diff if too large to avoid context bloat.
	if len(diff) > 50000 {
		diff = diff[:50000] + "\n... (diff truncated)"
	}

	// Get commit messages for context.
	logOutput, _ := runGit(ctx, repoPath, "log", baseBranch+".."+branch, "--oneline")

	prompt := fmt.Sprintf(
		"A worker agent was working on a task but its orchestrator process died. "+
			"The agent may have finished, partially completed, or broken things. "+
			"Assess the work done.\n\n"+
			"## Task Prompt\n%s\n\n"+
			"## Commits\n```\n%s\n```\n\n"+
			"## Diff (base...branch)\n```\n%s\n```\n\n"+
			"## Instructions\n"+
			"Based on the task prompt and the diff, did the agent complete the task?\n"+
			"Respond with exactly one of these verdicts on the FIRST LINE, followed by "+
			"a brief explanation (1-2 sentences) on the next line:\n"+
			"- COMPLETE — the task appears fully done\n"+
			"- INCOMPLETE — meaningful progress was made but the task is not finished\n"+
			"- BROKEN — the changes are harmful or nonsensical and should be discarded\n\n"+
			"Example response:\nCOMPLETE\nAll required endpoints were implemented and tests pass.",
		t.Prompt, logOutput, diff,
	)

	logFile := filepath.Join(ws.LogsPath(), t.ID+"-reset-assess.log")

	runner := agent.NewClaudeRunner()
	result, err := runner.Run(ctx, agent.RunOpts{
		Prompt: prompt,
		SystemPrompt: "You are a code review assistant. You assess whether a task " +
			"was completed based on the diff. Be concise and precise. " +
			"Output only the verdict and explanation.",
		Model:   ws.Config.Model,
		LogFile: logFile,
	})
	if err != nil {
		return "", "", fmt.Errorf("assessment agent failed: %w", err)
	}

	output := strings.TrimSpace(result.Output)
	if output == "" {
		return "", "", fmt.Errorf("assessment returned empty result")
	}

	// Parse the verdict from the first line.
	lines := strings.SplitN(output, "\n", 2)
	verdict := strings.TrimSpace(lines[0])

	// Normalize: extract just the keyword if the line contains extra text.
	for _, v := range []string{"COMPLETE", "INCOMPLETE", "BROKEN"} {
		if strings.Contains(strings.ToUpper(verdict), v) {
			verdict = v
			break
		}
	}

	explanation := ""
	if len(lines) > 1 {
		explanation = strings.TrimSpace(lines[1])
	}

	// Validate verdict.
	switch verdict {
	case "COMPLETE", "INCOMPLETE", "BROKEN":
		return verdict, explanation, nil
	default:
		// If we can't parse the verdict, treat as INCOMPLETE to be safe.
		return "INCOMPLETE", "Could not determine verdict from assessment: " + output, nil
	}
}
