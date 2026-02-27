package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wolandomny/retinue/internal/agent"
)

// Run executes a git command in the given directory and returns
// the combined output.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RunWithEnv executes a git command with additional environment
// variables.
func RunWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RebaseAndMerge rebases the task branch onto baseBranch in the
// worktree, then fast-forward merges into the main repo checkout.
// On success it removes the worktree and deletes the branch.
// If the rebase encounters conflicts, it spawns a Claude agent to
// resolve them before continuing.
func RebaseAndMerge(ctx context.Context, repoPath, worktreePath, branch, baseBranch, model, logsPath string) error {
	// Rebase in the worktree.
	if _, rebaseErr := Run(ctx, worktreePath, "rebase", baseBranch); rebaseErr != nil {
		// Rebase failed — attempt to resolve conflicts.
		fmt.Printf("Rebase conflict detected for branch %q, attempting resolution...\n", branch)

		if err := attemptRebaseWithResolution(ctx, worktreePath, branch, model, logsPath, rebaseErr); err != nil {
			return err
		}
	}

	// Checkout base branch in the repo.
	if _, err := Run(ctx, repoPath, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	// Fast-forward merge.
	if _, err := Run(ctx, repoPath, "merge", "--ff-only", branch); err != nil {
		return fmt.Errorf("ff-merge: %w", err)
	}

	// Verify the merge was truly a fast-forward (HEAD should have exactly one parent).
	parents, err := Run(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		return fmt.Errorf("verifying merge: %w", err)
	}
	// Output format: "<commit> <parent1> [<parent2> ...]"
	// A fast-forward results in exactly one parent. Two or more means a merge commit.
	if parts := strings.Fields(parents); len(parts) > 2 {
		// This should never happen with --ff-only, but roll back if it does.
		if _, resetErr := Run(ctx, repoPath, "reset", "--hard", "HEAD~1"); resetErr != nil {
			return fmt.Errorf("merge created merge commit and rollback failed: %w", resetErr)
		}
		return fmt.Errorf("merge of %s created a merge commit (expected fast-forward); rolled back", branch)
	}

	// Clean up worktree and branch (best-effort).
	_, _ = Run(ctx, repoPath, "worktree", "remove", worktreePath)
	_, _ = Run(ctx, repoPath, "branch", "-d", branch)

	return nil
}

// attemptRebaseWithResolution tries to resolve rebase conflicts using Claude,
// handling multiple conflicting commits in a loop. Returns nil on success.
func attemptRebaseWithResolution(ctx context.Context, worktreePath, branch, model, logsPath string, originalErr error) error {
	const maxResolveAttempts = 5

	for attempt := range maxResolveAttempts {
		if resolveErr := resolveConflicts(ctx, worktreePath, model, logsPath, branch); resolveErr != nil {
			_, _ = Run(ctx, worktreePath, "rebase", "--abort")
			return fmt.Errorf("rebase conflict (resolution failed on attempt %d: %v): %w", attempt+1, resolveErr, originalErr)
		}

		_, continueErr := RunWithEnv(ctx, worktreePath, []string{"GIT_EDITOR=true"}, "rebase", "--continue")
		if continueErr == nil {
			// Rebase completed successfully.
			return nil
		}

		// Check if the continue resulted in another conflict.
		conflictCheck, _ := Run(ctx, worktreePath, "diff", "--name-only", "--diff-filter=U")
		if strings.TrimSpace(conflictCheck) == "" {
			// No more conflicts, but continue failed for another reason.
			_, _ = Run(ctx, worktreePath, "rebase", "--abort")
			return fmt.Errorf("rebase --continue failed (no conflicts): %w", continueErr)
		}
		fmt.Printf("Additional conflict on attempt %d, resolving...\n", attempt+1)
	}

	_, _ = Run(ctx, worktreePath, "rebase", "--abort")
	return fmt.Errorf("rebase conflict: exceeded %d resolution attempts", maxResolveAttempts)
}

// resolveConflicts spawns a Claude agent to resolve git merge/rebase
// conflicts in the given directory. It finds all conflicted files,
// builds a prompt with the conflict markers, and asks Claude to
// produce resolved versions. Returns nil if all conflicts were
// resolved and staged.
func resolveConflicts(ctx context.Context, dir, model, logsPath, taskID string) error {
	// 1. Get the list of conflicted files.
	out, _ := Run(ctx, dir, "diff", "--name-only", "--diff-filter=U")
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
		if _, err := Run(ctx, dir, "add", f); err != nil {
			return fmt.Errorf("staging resolved file %s: %w", f, err)
		}
	}

	return nil
}
