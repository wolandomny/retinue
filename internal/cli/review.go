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

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run a persistent polling loop to review and merge completed task work",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			store := task.NewFileStore(ws.TasksPath())
			idleMessageShown := false

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				tasks, err := store.Load()
				if err != nil {
					return fmt.Errorf("loading tasks: %w", err)
				}

				t := findReviewable(tasks)
				if t == nil {
					if !idleMessageShown {
						fmt.Println("No tasks ready for review. Waiting...")
						idleMessageShown = true
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(10 * time.Second):
					}
					continue
				}

				idleMessageShown = false

				// Step 1: Transition to review.
				if err := store.Update(t.ID, func(t *task.Task) {
					t.Status = task.StatusReview
				}); err != nil {
					return fmt.Errorf("updating task to review: %w", err)
				}

				// Step 2: Resolve paths.
				repoPath := filepath.Join(ws.Path, ws.Config.Repos[t.Repo])
				worktreePath := filepath.Join(ws.Path, workspace.WorktreeDir, t.ID)

				// Step 3: Get the diff.
				diff, err := runGit(ctx, repoPath, "diff", baseBranch+"..."+t.Branch)
				if err != nil {
					return fmt.Errorf("getting diff for task %q: %w", t.ID, err)
				}

				// Step 4: Review via Claude.
				prompt := fmt.Sprintf(
					"## Task: %s\n\n### Original Requirements\n\n%s\n\n"+
						"### Task Result Summary\n\n%s\n\n"+
						"### Diff\n\n```diff\n%s\n```\n\n"+
						"Review this diff against the original requirements.",
					t.ID, t.Prompt, t.Result, diff,
				)

				runner := agent.NewClaudeRunner()
				result, err := runner.Run(ctx, agent.RunOpts{
					Prompt: prompt,
					SystemPrompt: "You are Azazello, the code reviewer for the Retinue system. " +
						"Review the diff against the original task requirements. " +
						"If the work satisfies the task, respond with exactly: APPROVED\n" +
						"If the work has issues, respond with: REJECTED\n<description of issues>",
					WorkDir: repoPath,
					Model:   ws.Config.Model,
					LogFile: filepath.Join(ws.LogsPath(), t.ID+"-review.log"),
				})
				if err != nil {
					return fmt.Errorf("running review for task %q: %w", t.ID, err)
				}

				// Step 5: Parse the review result.
				if isApproved(result.Output) {
					if err := rebaseAndMerge(ctx, repoPath, worktreePath, t.Branch); err != nil {
						markTaskFailed(store, t.ID, err.Error())
						fmt.Printf("Task %q failed: %s\n", t.ID, err)
						continue
					}
					markTaskMerged(store, t.ID)
					fmt.Printf("Task %q merged successfully.\n", t.ID)
				} else {
					markTaskFailed(store, t.ID, result.Output)
					fmt.Printf("Task %q failed review: %s\n", t.ID, result.Output)
				}
			}
		},
	}

	return cmd
}

// rebaseAndMerge rebases the task branch onto baseBranch in the
// worktree, then fast-forward merges into the main repo checkout.
// On success it removes the worktree and deletes the branch.
func rebaseAndMerge(ctx context.Context, repoPath, worktreePath, branch string) error {
	// Rebase in the worktree.
	if _, err := runGit(ctx, worktreePath, "rebase", baseBranch); err != nil {
		_, _ = runGit(ctx, worktreePath, "rebase", "--abort")
		return fmt.Errorf("rebase conflict: %w", err)
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

// findReviewable returns the first task with status "done" and a non-empty Branch field.
func findReviewable(tasks []task.Task) *task.Task {
	for i := range tasks {
		if tasks[i].Status == task.StatusDone && tasks[i].Branch != "" && tasks[i].Repo != "" {
			return &tasks[i]
		}
	}
	return nil
}

// isApproved checks if the review output starts with "APPROVED".
func isApproved(output string) bool {
	return strings.HasPrefix(strings.TrimSpace(output), "APPROVED")
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
