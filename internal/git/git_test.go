package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/agent"
)

// fakeRunner is a test double for agent.Runner used in git tests.
type fakeRunner struct {
	runFunc func(ctx context.Context, opts agent.RunOpts) (agent.Result, error)
	calls   []agent.RunOpts
}

func (f *fakeRunner) Run(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
	f.calls = append(f.calls, opts)
	if f.runFunc != nil {
		return f.runFunc(ctx, opts)
	}
	return agent.Result{Output: "fake output", ExitCode: 0}, nil
}

// initTestRepo creates a bare-minimum git repo in a temp directory with one
// commit on main. It returns the repo path and a cleanup function.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		if _, err := Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	// Create an initial commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestRun_Success(t *testing.T) {
	out, err := Run(context.Background(), ".", "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRunWithEnv_Success(t *testing.T) {
	out, err := RunWithEnv(context.Background(), ".", []string{"GIT_AUTHOR_NAME=TestAuthor"}, "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRun_Failure(t *testing.T) {
	_, err := Run(context.Background(), ".", "nonexistent-subcommand")
	if err == nil {
		t.Fatal("expected error for invalid git subcommand")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestRebaseAndMerge(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "feature-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a commit on the feature branch inside the worktree.
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Run RebaseAndMerge (nil runner — no conflicts expected so runner is unused).
	if err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", "", "", nil); err != nil {
		t.Fatalf("RebaseAndMerge failed: %v", err)
	}

	// Verify: the feature commit is now on main.
	log, err := Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "add feature") {
		t.Errorf("expected 'add feature' commit on main, got log:\n%s", log)
	}

	// Verify: the worktree directory is removed.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree directory %q to be removed", worktreePath)
	}

	// Verify: the branch is deleted.
	branches, err := Run(ctx, repoPath, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(branches, branch) {
		t.Errorf("expected branch %q to be deleted, got branches:\n%s", branch, branches)
	}
}

// TestRebaseAndMerge_DivergentBaseStaysLinear is a regression test for the
// fast-forward-only merge safety. The existing TestRebaseAndMerge never
// advances main after the feature branch is created, so the merge is a
// fast-forward by construction and the one-parent assertion can never fail.
// Here the histories actually DIVERGE: the feature branch is created, THEN main
// receives an independent commit. This forces RebaseAndMerge to rebase the
// feature work onto the new base tip before the ff-only merge, and asserts the
// invariant the guard protects: afterwards the base HEAD is linear (exactly one
// parent — NOT a 2-parent merge commit) and contains both the divergent base
// work and the rebased feature work.
func TestRebaseAndMerge_DivergentBaseStaysLinear(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	branch := "feature-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Feature work in the worktree.
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// CRITICAL: advance main AFTER the feature branch exists, on a different
	// file so it diverges without conflicting. main is now no longer an ancestor
	// of the feature branch.
	if err := os.WriteFile(filepath.Join(repoPath, "base.txt"), []byte("base work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "divergent base commit"); err != nil {
		t.Fatal(err)
	}

	// Precondition: the histories really have diverged.
	if _, err := Run(ctx, repoPath, "merge-base", "--is-ancestor", "main", branch); err == nil {
		t.Fatal("precondition failed: main is already an ancestor of the feature branch (histories did not diverge)")
	}

	if err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", "", "", nil); err != nil {
		t.Fatalf("RebaseAndMerge failed: %v", err)
	}

	// Invariant: base HEAD is linear — exactly one parent.
	parents, err := Run(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Fields(parents); len(parts) != 2 {
		t.Fatalf("expected base HEAD linear (1 parent) after divergent rebase+ff-merge, got %d parents: %q", len(parts)-1, parents)
	}

	// Both the divergent base work and the rebased feature work are present.
	log, err := Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "add feature") {
		t.Errorf("expected 'add feature' on base after merge, got:\n%s", log)
	}
	if !strings.Contains(log, "divergent base commit") {
		t.Errorf("expected 'divergent base commit' on base after merge, got:\n%s", log)
	}
}

// divergentRepo builds a repo where "feature" and "main" have genuinely
// diverged (each has a commit the other lacks, on different files so they do not
// conflict), leaves "main" checked out, and returns the repo path plus main's
// HEAD sha. In this state a `merge feature` from main is a non-fast-forward that
// would create a 2-parent merge commit; `merge --ff-only feature` is refused.
func divergentRepo(t *testing.T) (repoPath, baseHEAD string) {
	t.Helper()
	ctx := context.Background()
	repoPath = initTestRepo(t)

	if _, err := Run(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "base.txt"), []byte("base work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "divergent base commit"); err != nil {
		t.Fatal(err)
	}

	// Assert the divergence precondition: neither branch is an ancestor of the
	// other, so the merge is genuinely non-fast-forward.
	if _, err := Run(ctx, repoPath, "merge-base", "--is-ancestor", "feature", "main"); err == nil {
		t.Fatal("precondition failed: feature is an ancestor of main (not divergent)")
	}
	if _, err := Run(ctx, repoPath, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("precondition failed: main is an ancestor of feature (fast-forwardable)")
	}

	baseHEAD, err := Run(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repoPath, baseHEAD
}

// TestFFMerge_RefusesDivergentBranch isolates the FIRST safeguard: `merge
// --ff-only`. RebaseAndMerge's preceding rebase normally linearizes history, so
// the merge step only ever sees fast-forwardable branches and --ff-only is never
// observably exercised end-to-end — which is exactly why dropping it survived
// the mutation audit. Here we hand the production ffMerge helper a genuinely
// divergent (non-ff) branch: with --ff-only the merge MUST be refused outright,
// creating NO merge commit and leaving the base HEAD untouched. Dropping
// --ff-only makes git create a 2-parent merge commit instead, which these
// assertions detect (base HEAD moves / becomes a merge commit and no error).
func TestFFMerge_RefusesDivergentBranch(t *testing.T) {
	ctx := context.Background()
	repoPath, baseBefore := divergentRepo(t)

	// main is checked out; drive only the --ff-only merge safeguard.
	err := ffMerge(ctx, repoPath, "feature", nil)

	// A non-ff merge must be refused with an error...
	if err == nil {
		t.Fatal("expected ffMerge to refuse the non-fast-forward branch, got nil error")
	}
	// ...and no merge commit may exist: base HEAD must be unchanged and linear.
	baseAfter, perr := Run(ctx, repoPath, "rev-parse", "HEAD")
	if perr != nil {
		t.Fatal(perr)
	}
	if baseAfter != baseBefore {
		t.Fatalf("base HEAD moved from %s to %s; --ff-only did not refuse the non-fast-forward merge", baseBefore[:7], baseAfter[:7])
	}
	parents, perr := Run(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if perr != nil {
		t.Fatal(perr)
	}
	if parts := strings.Fields(parents); len(parts) > 2 {
		t.Fatalf("base HEAD became a %d-parent merge commit; --ff-only safeguard failed (parents: %q)", len(parts)-1, parents)
	}
}

// TestVerifyLinearOrRollback_UndoesMergeCommit isolates the SECOND safeguard:
// the parent-count check + reset --hard rollback. The rollback only matters when
// a merge commit has already been created on the base (e.g. if --ff-only were
// ever bypassed). With --ff-only present that never happens, so the rollback is
// unreachable end-to-end and disabling it survived the mutation audit. Here we
// deliberately create a real 2-parent merge commit on the base (a plain,
// non-ff merge in the TEST setup — NOT production code), then drive the
// production verifyLinearOrRollback helper and assert it detects the merge
// commit and rolls the base back to a linear, one-parent HEAD. Disabling the
// `len(parts) > 2` rollback leaves the 2-parent merge commit in place, which
// these assertions detect.
func TestVerifyLinearOrRollback_UndoesMergeCommit(t *testing.T) {
	ctx := context.Background()
	repoPath, baseBefore := divergentRepo(t)

	// TEST SETUP ONLY: force a real 2-parent merge commit onto main by doing a
	// plain (non-ff) merge. This simulates the dangerous state a bypassed
	// --ff-only would produce, so the rollback safeguard has something to undo.
	if _, err := Run(ctx, repoPath, "merge", "--no-edit", "feature"); err != nil {
		t.Fatalf("test setup: forcing merge commit failed: %v", err)
	}
	parents, err := Run(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Fields(parents); len(parts) <= 2 {
		t.Fatalf("test setup: expected a 2-parent merge commit, got %d parents: %q", len(parts)-1, parents)
	}

	// Drive the production rollback safeguard against the merge-commit state.
	rbErr := verifyLinearOrRollback(ctx, repoPath, "feature", nil)

	// The rollback must report the problem...
	if rbErr == nil {
		t.Fatal("expected verifyLinearOrRollback to report the merge commit, got nil error")
	}
	// ...and undo it: base HEAD must be linear (one parent) again...
	parents, err = Run(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Fields(parents); len(parts) > 2 {
		t.Fatalf("merge commit was NOT rolled back; base HEAD still has %d parents: %q", len(parts)-1, parents)
	}
	// ...and restored to exactly the pre-merge base commit.
	baseAfter, err := Run(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if baseAfter != baseBefore {
		t.Fatalf("rollback did not restore base HEAD: want %s, got %s", baseBefore[:7], baseAfter[:7])
	}
}

func TestRebaseAndMerge_Conflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "conflict-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a conflicting change on main.
	if err := os.WriteFile(filepath.Join(repoPath, "conflict.txt"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "main change"); err != nil {
		t.Fatal(err)
	}

	// Add a conflicting change on the feature branch.
	if err := os.WriteFile(filepath.Join(worktreePath, "conflict.txt"), []byte("branch version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "branch change"); err != nil {
		t.Fatal(err)
	}

	// Run RebaseAndMerge — may fail with a conflict error or succeed
	// if a Claude agent is available and resolves the conflict.
	logsDir := t.TempDir()
	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", nil)
	if err != nil {
		// When Claude is not available, we expect a rebase conflict error.
		if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
			t.Errorf("expected error about rebase conflict or resolution failure, got: %v", err)
		}
	}
	// If err == nil, Claude resolved the conflict — that's also acceptable.
}

func TestRebaseAndMerge_ConflictWithMockRunner_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "conflict-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a conflicting change on main.
	if err := os.WriteFile(filepath.Join(repoPath, "conflict.txt"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "main change"); err != nil {
		t.Fatal(err)
	}

	// Add a conflicting change on the feature branch.
	if err := os.WriteFile(filepath.Join(worktreePath, "conflict.txt"), []byte("branch version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "branch change"); err != nil {
		t.Fatal(err)
	}

	// Create a fake runner that resolves conflicts by writing merged content
	// and staging the file, simulating what a real Claude agent would do.
	logsDir := t.TempDir()
	fake := &fakeRunner{
		runFunc: func(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
			// Simulate Claude resolving the conflict: write merged content
			// and stage the file.
			resolved := "main version\nbranch version\n"
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "conflict.txt"), []byte(resolved), 0o644); err != nil {
				return agent.Result{}, err
			}
			if _, err := Run(ctx, opts.WorkDir, "add", "conflict.txt"); err != nil {
				return agent.Result{}, err
			}
			return agent.Result{Output: "resolved", ExitCode: 0}, nil
		},
	}

	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", fake)
	if err != nil {
		t.Fatalf("RebaseAndMerge with mock runner failed: %v", err)
	}

	// Verify the runner was called.
	if len(fake.calls) == 0 {
		t.Fatal("expected fake runner to be called at least once")
	}

	// Verify the merge result: both versions should be present.
	log, err := Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "branch change") {
		t.Errorf("expected 'branch change' commit on main, got log:\n%s", log)
	}

	// Verify the resolved file content.
	data, err := os.ReadFile(filepath.Join(repoPath, "conflict.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "main version") || !strings.Contains(content, "branch version") {
		t.Errorf("expected merged content with both versions, got: %s", content)
	}
}

func TestRebaseAndMerge_ConflictWithMockRunner_Failure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repoPath := initTestRepo(t)

	// Create a worktree on a feature branch.
	branch := "conflict-branch"
	worktreePath := filepath.Join(t.TempDir(), "wt")

	if _, err := Run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Add a conflicting change on main.
	if err := os.WriteFile(filepath.Join(repoPath, "conflict.txt"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, repoPath, "commit", "-m", "main change"); err != nil {
		t.Fatal(err)
	}

	// Add a conflicting change on the feature branch.
	if err := os.WriteFile(filepath.Join(worktreePath, "conflict.txt"), []byte("branch version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, worktreePath, "commit", "-m", "branch change"); err != nil {
		t.Fatal(err)
	}

	// Create a fake runner that always fails.
	logsDir := t.TempDir()
	fake := &fakeRunner{
		runFunc: func(ctx context.Context, opts agent.RunOpts) (agent.Result, error) {
			return agent.Result{}, fmt.Errorf("mock: agent unavailable")
		},
	}

	err := RebaseAndMerge(ctx, repoPath, worktreePath, branch, "main", "", logsDir, "", fake)
	if err == nil {
		t.Fatal("expected error when mock runner fails, got nil")
	}
	if !strings.Contains(err.Error(), "rebase conflict") && !strings.Contains(err.Error(), "resolution failed") {
		t.Errorf("expected rebase conflict or resolution failed error, got: %v", err)
	}

	// Verify the runner was called.
	if len(fake.calls) == 0 {
		t.Fatal("expected fake runner to be called at least once")
	}
}
