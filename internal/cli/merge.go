package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

type mergeOneOpts struct {
	ws      *workspace.Workspace
	store   *task.FileStore
	t       task.Task
	review  bool
	archive bool // false during runAll to keep tasks visible for dependency resolution
	out     io.Writer
}

type mergeOneResult struct {
	Merged   bool
	Rejected bool
	Err      error
}

// mergeOne performs the merge logic for a single task.
func mergeOne(ctx context.Context, opts mergeOneOpts) mergeOneResult {
	repoCfg, ok := opts.ws.Config.Repos[opts.t.Repo]
	if !ok {
		errMsg := fmt.Sprintf("repo %q not found in config", opts.t.Repo)
		markTaskFailed(opts.store, opts.t.ID, errMsg)
		return mergeOneResult{Err: fmt.Errorf("%s", errMsg)}
	}
	repoPath := filepath.Join(opts.ws.Path, repoCfg.Path)
	worktreePath := filepath.Join(opts.ws.Path, workspace.WorktreeDir, opts.t.ID)

	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		errMsg := fmt.Sprintf("worktree %s not found", worktreePath)
		markTaskFailed(opts.store, opts.t.ID, errMsg)
		return mergeOneResult{Err: fmt.Errorf("%s", errMsg)}
	}

	baseBranch := task.ResolveBaseBranch(opts.t, opts.ws.Config.Repos)

	// Run validation before merging.
	if cmdStr, ok := opts.ws.Config.Validate[opts.t.Repo]; ok && cmdStr != "" {
		fmt.Fprintf(opts.out, "Task %q: running validation...\n", opts.t.ID)
	}
	if err := runValidation(ctx, worktreePath, opts.t.Repo, opts.ws.Config.Validate); err != nil {
		markTaskFailed(opts.store, opts.t.ID, err.Error())
		fmt.Fprintf(opts.out, "Task %q failed validation: %s\n", opts.t.ID, err)
		return mergeOneResult{Err: err}
	}

	// Optional pre-merge review.
	if opts.review {
		fmt.Fprintf(opts.out, "Task %q: reviewing diff...\n", opts.t.ID)
		verdict, reviewErr := reviewDiff(ctx, worktreePath, opts.t, baseBranch, opts.ws.Config.Model, opts.ws.LogsPath())
		if reviewErr != nil {
			fmt.Fprintf(opts.out, "Task %q: review failed: %s (proceeding anyway)\n", opts.t.ID, reviewErr)
		} else if !verdict.Approved {
			// Move task back to pending with feedback.
			if err := opts.store.Update(opts.t.ID, func(tk *task.Task) {
				tk.Status = task.StatusPending
				tk.Error = ""
				tk.Prompt = tk.Prompt + "\n\n## Review Feedback (from previous attempt)\n" + verdict.Feedback
				if verdict.Usage.InputTokens > 0 {
					tk.Meta["review_tokens"] = fmt.Sprintf("%d/%d", verdict.Usage.InputTokens, verdict.Usage.OutputTokens)
				}
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to update rejected task: %v\n", err)
			}
			fmt.Fprintf(opts.out, "Task %q rejected by review:\n%s\n\n", opts.t.ID, verdict.Feedback)
			return mergeOneResult{Rejected: true}
		} else {
			fmt.Fprintf(opts.out, "Task %q: review approved\n", opts.t.ID)
		}
	}

	if err := rebaseAndMerge(ctx, repoPath, worktreePath, opts.t.Branch, baseBranch, opts.ws.Config.Model, opts.ws.LogsPath()); err != nil {
		markTaskFailed(opts.store, opts.t.ID, err.Error())
		return mergeOneResult{Err: err}
	}

	if opts.archive {
		markTaskMerged(opts.store, opts.t.ID, opts.ws.ArchivePath())
	} else {
		markTaskMergedNoArchive(opts.store, opts.t.ID)
	}
	return mergeOneResult{Merged: true}
}

// newMergeCmd returns a command that merges completed task branches
// back into the base branch.
func newMergeCmd() *cobra.Command {
	var (
		taskID string
		review bool
	)

	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge completed task branches into the base branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			store := task.NewFileStore(ws.TasksPath())

			tasks, err := store.Load()
			if err != nil {
				return fmt.Errorf("loading tasks: %w", err)
			}

			var targets []task.Task
			if taskID != "" {
				for _, t := range tasks {
					if t.ID == taskID {
						targets = append(targets, t)
						break
					}
				}
				if len(targets) == 0 {
					return fmt.Errorf("task %q not found", taskID)
				}
				if targets[0].Status != task.StatusDone {
					return fmt.Errorf("task %q is %s, not done", taskID, targets[0].Status)
				}
			} else {
				for _, t := range tasks {
					if t.Status == task.StatusDone && t.Branch != "" && t.Repo != "" {
						targets = append(targets, t)
					}
				}
			}

			if len(targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tasks ready to merge.")
				return nil
			}

			for _, t := range targets {
				result := mergeOne(ctx, mergeOneOpts{
					ws:      ws,
					store:   store,
					t:       t,
					review:  review,
					archive: true,
					out:     cmd.OutOrStdout(),
				})
				if result.Err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q failed: %s\n", t.ID, result.Err)
				} else if result.Rejected {
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q rejected by review\n", t.ID)
				} else if result.Merged {
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q merged successfully.\n", t.ID)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "specific task ID to merge")
	cmd.Flags().BoolVar(&review, "review", false, "run AI review of diff before merging")

	return cmd
}

// rebaseAndMerge rebases the task branch onto baseBranch in the
// worktree, then fast-forward merges into the main repo checkout.
// On success it removes the worktree and deletes the branch.
// If the rebase encounters conflicts, it spawns a Claude agent to
// resolve them before continuing.
func rebaseAndMerge(ctx context.Context, repoPath, worktreePath, branch, baseBranch, model, logsPath string) error {
	// Rebase in the worktree.
	if _, rebaseErr := runGit(ctx, worktreePath, "rebase", baseBranch); rebaseErr != nil {
		// Rebase failed — attempt to resolve conflicts.
		fmt.Printf("Rebase conflict detected for branch %q, attempting resolution...\n", branch)

		if err := attemptRebaseWithResolution(ctx, worktreePath, branch, model, logsPath, rebaseErr); err != nil {
			return err
		}
	}

	// Checkout base branch in the repo.
	if _, err := runGit(ctx, repoPath, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	// Fast-forward merge.
	if _, err := runGit(ctx, repoPath, "merge", "--ff-only", branch); err != nil {
		return fmt.Errorf("ff-merge: %w", err)
	}

	// Verify the merge was truly a fast-forward (HEAD should have exactly one parent).
	parents, err := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		return fmt.Errorf("verifying merge: %w", err)
	}
	// Output format: "<commit> <parent1> [<parent2> ...]"
	// A fast-forward results in exactly one parent. Two or more means a merge commit.
	if parts := strings.Fields(parents); len(parts) > 2 {
		// This should never happen with --ff-only, but roll back if it does.
		if _, resetErr := runGit(ctx, repoPath, "reset", "--hard", "HEAD~1"); resetErr != nil {
			return fmt.Errorf("merge created merge commit and rollback failed: %w", resetErr)
		}
		return fmt.Errorf("merge of %s created a merge commit (expected fast-forward); rolled back", branch)
	}

	// Clean up worktree and branch (best-effort).
	_, _ = runGit(ctx, repoPath, "worktree", "remove", worktreePath)
	_, _ = runGit(ctx, repoPath, "branch", "-d", branch)

	return nil
}

// attemptRebaseWithResolution tries to resolve rebase conflicts using Claude,
// handling multiple conflicting commits in a loop. Returns nil on success.
func attemptRebaseWithResolution(ctx context.Context, worktreePath, branch, model, logsPath string, originalErr error) error {
	const maxResolveAttempts = 5

	for attempt := 0; attempt < maxResolveAttempts; attempt++ {
		if resolveErr := resolveConflicts(ctx, worktreePath, model, logsPath, branch); resolveErr != nil {
			_, _ = runGit(ctx, worktreePath, "rebase", "--abort")
			return fmt.Errorf("rebase conflict (resolution failed on attempt %d: %v): %w", attempt+1, resolveErr, originalErr)
		}

		_, continueErr := runGitWithEnv(ctx, worktreePath, []string{"GIT_EDITOR=true"}, "rebase", "--continue")
		if continueErr == nil {
			// Rebase completed successfully.
			return nil
		}

		// Check if the continue resulted in another conflict.
		conflictCheck, _ := runGit(ctx, worktreePath, "diff", "--name-only", "--diff-filter=U")
		if strings.TrimSpace(conflictCheck) == "" {
			// No more conflicts, but continue failed for another reason.
			_, _ = runGit(ctx, worktreePath, "rebase", "--abort")
			return fmt.Errorf("rebase --continue failed (no conflicts): %w", continueErr)
		}
		fmt.Printf("Additional conflict on attempt %d, resolving...\n", attempt+1)
	}

	_, _ = runGit(ctx, worktreePath, "rebase", "--abort")
	return fmt.Errorf("rebase conflict: exceeded %d resolution attempts", maxResolveAttempts)
}

// resolveConflicts spawns a Claude agent to resolve git merge/rebase
// conflicts in the given directory.
func resolveConflicts(ctx context.Context, dir, model, logsPath, taskID string) error {
	// 1. Get the list of conflicted files.
	out, _ := runGit(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	conflictedFiles := strings.Split(strings.TrimSpace(out), "\n")
	if len(conflictedFiles) == 0 || (len(conflictedFiles) == 1 && conflictedFiles[0] == "") {
		return fmt.Errorf("no conflicted files found")
	}

	// 2. Read each conflicted file's contents.
	var filesContent strings.Builder
	for _, f := range conflictedFiles {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return fmt.Errorf("reading conflicted file %s: %w", f, err)
		}
		fmt.Fprintf(&filesContent, "### File: %s\n```\n%s\n```\n\n", f, string(data))
	}

	// 3. Spawn Claude to resolve.
	prompt := fmt.Sprintf(
		"The following files have git merge/rebase conflicts (indicated by "+
			"<<<<<<< / ======= / >>>>>>> markers). Resolve each conflict by "+
			"keeping the intent of BOTH sides — the incoming changes and the "+
			"existing changes. Write the resolved files.\n\n%s"+
			"For each file, resolve the conflicts and write the complete "+
			"resolved file using the Write tool. Do not leave any conflict "+
			"markers. Make sure the result compiles.",
		filesContent.String(),
	)

	runner := agent.NewClaudeRunner()
	_, err := runner.Run(ctx, agent.RunOpts{
		Prompt: prompt,
		SystemPrompt: "You are resolving git merge conflicts. " +
			"You have access to the working directory. Read the conflicted files, " +
			"understand both sides of each conflict, and write resolved versions " +
			"that preserve the intent of both changes. After resolving, stage " +
			"each file with `git add <file>`.",
		WorkDir: dir,
		Model:   model,
		LogFile: filepath.Join(logsPath, taskID+"-conflict.log"),
	})
	if err != nil {
		return fmt.Errorf("claude conflict resolution failed: %w", err)
	}

	// 4. Verify no conflict markers remain.
	for _, f := range conflictedFiles {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return fmt.Errorf("reading resolved file %s: %w", f, err)
		}
		content := string(data)
		if strings.Contains(content, "<<<<<<<") ||
			strings.Contains(content, "=======") ||
			strings.Contains(content, ">>>>>>>") {
			return fmt.Errorf("conflict markers remain in %s", f)
		}
	}

	// 5. Stage all resolved files.
	for _, f := range conflictedFiles {
		if _, err := runGit(ctx, dir, "add", f); err != nil {
			return fmt.Errorf("staging resolved file %s: %w", f, err)
		}
	}

	return nil
}

// runValidation runs the configured validation command for the
// given repo in the specified directory. Returns nil if no
// validation is configured or if the command succeeds.
func runValidation(ctx context.Context, dir, repo string, validate map[string]string) error {
	cmdStr, ok := validate[repo]
	if !ok || cmdStr == "" {
		return nil // no validation configured for this repo
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validation failed: %s\n%s", err, string(out))
	}
	return nil
}

// markTaskFailed transitions the task to failed status with the given
// error message, logging any store update errors to stderr.
func markTaskFailed(store *task.FileStore, id, errMsg string) {
	if err := store.Update(id, func(t *task.Task) {
		now := time.Now()
		t.Status = task.StatusFailed
		t.Error = errMsg
		t.FinishedAt = &now
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update task %q: %v\n", id, err)
	}
}

// markTaskMergedNoArchive transitions the task to merged status but
// does NOT archive it. The task stays in tasks.yaml so that
// depsResolved() can find it during runAll.
func markTaskMergedNoArchive(store *task.FileStore, id string) {
	if err := store.Update(id, func(t *task.Task) {
		now := time.Now()
		t.Status = task.StatusMerged
		t.FinishedAt = &now
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update task %q: %v\n", id, err)
	}
}

// markTaskMerged transitions the task to merged status and archives
// it from the active tasks file to the archive file.
func markTaskMerged(store *task.FileStore, id, archivePath string) {
	if err := store.Update(id, func(t *task.Task) {
		now := time.Now()
		t.Status = task.StatusMerged
		t.FinishedAt = &now
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update task %q: %v\n", id, err)
		return
	}

	if err := store.Archive(id, archivePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to archive task %q: %v\n", id, err)
	}
}

// runGit runs a git command in the given directory and returns the combined output.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGitWithEnv runs a git command with additional environment variables.
func runGitWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out)), nil
}
