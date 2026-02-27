package cli

import (
	"context"
	"fmt"
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

const baseBranch = "main"

// newMergeCmd returns a command that merges completed task branches
// back into the base branch.
func newMergeCmd() *cobra.Command {
	var taskID string

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
				repoDirRel, ok := ws.Config.Repos[t.Repo]
				if !ok {
					markTaskFailed(store, t.ID, fmt.Sprintf("repo %q not found in config", t.Repo))
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q failed: repo %q not found in config\n", t.ID, t.Repo)
					continue
				}
				repoPath := filepath.Join(ws.Path, repoDirRel)
				worktreePath := filepath.Join(ws.Path, workspace.WorktreeDir, t.ID)

				if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
					markTaskFailed(store, t.ID, fmt.Sprintf("worktree %s not found", worktreePath))
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q failed: worktree not found\n", t.ID)
					continue
				}

				if err := rebaseAndMerge(ctx, repoPath, worktreePath, t.Branch, ws.Config.Model, ws.LogsPath()); err != nil {
					markTaskFailed(store, t.ID, err.Error())
					fmt.Fprintf(cmd.OutOrStdout(), "Task %q failed: %s\n", t.ID, err)
					continue
				}
				markTaskMerged(store, t.ID)
				fmt.Fprintf(cmd.OutOrStdout(), "Task %q merged successfully.\n", t.ID)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "specific task ID to merge")

	return cmd
}

// rebaseAndMerge rebases the task branch onto baseBranch in the
// worktree, then fast-forward merges into the main repo checkout.
// On success it removes the worktree and deletes the branch.
// If the rebase encounters conflicts, it spawns a Claude agent to
// resolve them before continuing.
func rebaseAndMerge(ctx context.Context, repoPath, worktreePath, branch, model, logsPath string) error {
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

// markTaskMerged transitions the task to merged status, logging any
// store update errors to stderr.
func markTaskMerged(store *task.FileStore, id string) {
	if err := store.Update(id, func(t *task.Task) {
		now := time.Now()
		t.Status = task.StatusMerged
		t.FinishedAt = &now
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update task %q: %v\n", id, err)
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
