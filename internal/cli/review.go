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

// newReviewCmd returns a command that runs a persistent polling loop
// to review, merge, or reject completed task work.
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
				repoDirRel, ok := ws.Config.Repos[t.Repo]
				if !ok {
					markTaskFailed(store, t.ID, fmt.Sprintf("repo %q not found in config", t.Repo))
					fmt.Printf("Task %q failed: repo %q not found in config\n", t.ID, t.Repo)
					continue
				}
				repoPath := filepath.Join(ws.Path, repoDirRel)
				worktreePath := filepath.Join(ws.Path, workspace.WorktreeDir, t.ID)

				// Verify worktree exists before attempting review.
				if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
					markTaskFailed(store, t.ID, fmt.Sprintf("worktree %s not found — was it cleaned up?", worktreePath))
					fmt.Printf("Task %q failed: worktree not found\n", t.ID)
					continue
				}

				// Step 3: Get the diff.
				diff, err := runGit(ctx, repoPath, "diff", baseBranch+"..."+t.Branch)
				if err != nil {
					return fmt.Errorf("getting diff for task %q: %w", t.ID, err)
				}

				// Step 4: Review via Claude.
				prompt := fmt.Sprintf(
					"## Task: %s\n\n"+
						"### Original Requirements\n\n%s\n\n"+
						"### Worker's Result Summary\n\n%s\n\n"+
						"### Diff (for reference — you can also explore the code directly)\n\n"+
						"```diff\n%s\n```\n\n"+
						"Follow your review protocol. Build, test, then review.",
					t.ID, t.Prompt, t.Result, diff,
				)

				runner := agent.NewClaudeRunner()
				result, err := runner.Run(ctx, agent.RunOpts{
					Prompt: prompt,
					SystemPrompt: "You are Azazello, the code reviewer and verification gate for the " +
						"Retinue system. You have a shell in the task's worktree. Your job " +
						"is to verify that the work is correct, complete, and mergeable.\n\n" +
						"## Review Protocol\n\n" +
						"Follow these steps IN ORDER. Do not skip any step.\n\n" +
						"### Step 1: Build verification\n" +
						"Run `go build ./...` in the working directory.\n" +
						"If the build fails, REJECT immediately with the build errors.\n\n" +
						"### Step 2: Test verification\n" +
						"Run `go test ./...` in the working directory.\n" +
						"If any tests fail, REJECT immediately with the test output.\n\n" +
						"### Step 3: Diff review\n" +
						"Examine the diff provided below against the original task\n" +
						"requirements. Check for:\n" +
						"- Correctness: Does the code do what was asked?\n" +
						"- Completeness: Are all requirements addressed?\n" +
						"- Quality: No debug prints, no TODO/FIXME left behind, no\n" +
						"  dead code, no obvious bugs.\n" +
						"- Safety: No hardcoded secrets, no dangerous operations without\n" +
						"  error handling.\n\n" +
						"### Step 4: Verdict\n" +
						"If ALL steps pass, respond with exactly:\n" +
						"APPROVED\n\n" +
						"If ANY step fails, respond with exactly:\n" +
						"REJECTED\n" +
						"<clear, actionable description of what needs to be fixed>\n\n" +
						"Be specific in rejections. \"Code looks wrong\" is useless.\n" +
						"\"Function X doesn't handle the nil case on line Y\" is useful.",
					WorkDir: worktreePath,
					Model:   ws.Config.Model,
					LogFile: filepath.Join(ws.LogsPath(), t.ID+"-review.log"),
				})
				if err != nil {
					return fmt.Errorf("running review for task %q: %w", t.ID, err)
				}

				// Step 5: Parse the review result.
				if isApproved(result.Output) {
					if err := rebaseAndMerge(ctx, repoPath, worktreePath, t.Branch, ws.Config.Model, ws.LogsPath()); err != nil {
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
// conflicts in the given directory. It finds all conflicted files,
// builds a prompt with the conflict markers, and asks Claude to
// produce resolved versions. Returns nil if all conflicts were
// resolved and staged.
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
		SystemPrompt: "You are Azazello, resolving git merge conflicts. " +
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
