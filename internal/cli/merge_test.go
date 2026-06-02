package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

func TestRunValidation_NoConfig(t *testing.T) {
	// nil validate map → should return nil (no validation).
	err := runValidation(context.Background(), t.TempDir(), "myrepo", nil)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_EmptyCommand(t *testing.T) {
	// Empty string command → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": ""})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_RepoNotInMap(t *testing.T) {
	// Repo not in validate map → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "other",
		map[string]string{"myrepo": "true"})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_Success(t *testing.T) {
	// Command succeeds → should return nil.
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": "true"})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunValidation_Failure(t *testing.T) {
	// Command fails → should return error containing "validation failed".
	err := runValidation(context.Background(), t.TempDir(), "myrepo",
		map[string]string{"myrepo": "false"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestRunValidation_OutputIncluded(t *testing.T) {
	// Verify that command output is included in the error message.
	err := runValidation(context.Background(), t.TempDir(), "r",
		map[string]string{"r": "echo 'build broke' && exit 1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "build broke") {
		t.Fatalf("expected output in error, got: %s", err)
	}
}

func TestMarkTaskFailed_SetsStatusAndError(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskFailed(store, "t1", "something broke")

	tasks, _ := store.Load()
	if tasks[0].Status != task.StatusFailed {
		t.Fatalf("expected failed, got %s", tasks[0].Status)
	}
	if tasks[0].Error != "something broke" {
		t.Fatalf("expected error message, got %q", tasks[0].Error)
	}
	if tasks[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestRebaseAndMerge_FastForwardOnly(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Go back to main in the repo before creating worktree (branch is
	// already checked out in the main repo, so we must detach first).
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Merge via rebaseAndMerge.
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "main", "", "", nil); err != nil {
		t.Fatalf("rebaseAndMerge failed: %v", err)
	}

	// Verify HEAD has exactly one parent (fast-forward, not merge commit).
	parents, err := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(parents)
	if len(parts) > 2 {
		t.Fatalf("expected fast-forward (1 parent), got %d parents", len(parts)-1)
	}
}

// TestRebaseAndMerge_DivergentBaseStaysLinear is a regression test for the
// fast-forward-only merge safety. The existing TestRebaseAndMerge_FastForwardOnly
// never advances main after the feature branch is created, so the merge is a
// fast-forward by construction and the one-parent assertion can never fail. Here
// the histories actually DIVERGE: the feature branch is created, THEN main
// receives an independent commit, forcing rebaseAndMerge to rebase the feature
// work onto the new base tip before the ff-only merge. Afterwards the base HEAD
// must be linear (one parent) and contain both sides' work.
func TestRebaseAndMerge_DivergentBaseStaysLinear(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Feature branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Back to main, then advance main with a DIVERGENT commit (different file)
	// so it is no longer an ancestor of the feature branch.
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "base.txt"), []byte("base work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "divergent base commit"); err != nil {
		t.Fatal(err)
	}

	// Precondition: histories have diverged.
	if _, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("precondition failed: main is already an ancestor of feature (histories did not diverge)")
	}

	// Worktree for the feature branch.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "main", "", "", nil); err != nil {
		t.Fatalf("rebaseAndMerge failed: %v", err)
	}

	// Invariant: base HEAD is linear — exactly one parent.
	parents, err := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Fields(parents); len(parts) != 2 {
		t.Fatalf("expected base HEAD linear (1 parent) after divergent rebase+ff-merge, got %d parents: %q", len(parts)-1, parents)
	}

	// Both sides' work present on main.
	log, err := runGit(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "add feature") {
		t.Errorf("expected 'add feature' on main after merge, got:\n%s", log)
	}
	if !strings.Contains(log, "divergent base commit") {
		t.Errorf("expected 'divergent base commit' on main after merge, got:\n%s", log)
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

	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "base.txt"), []byte("base work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "divergent base commit"); err != nil {
		t.Fatal(err)
	}

	// Assert the divergence precondition: neither branch is an ancestor of the
	// other, so the merge is genuinely non-fast-forward.
	if _, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "feature", "main"); err == nil {
		t.Fatal("precondition failed: feature is an ancestor of main (not divergent)")
	}
	if _, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("precondition failed: main is an ancestor of feature (fast-forwardable)")
	}

	baseHEAD, err := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repoPath, baseHEAD
}

// TestFFMerge_RefusesDivergentBranch isolates the FIRST safeguard: `merge
// --ff-only`. rebaseAndMerge's preceding rebase normally linearizes history, so
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
	baseAfter, perr := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if perr != nil {
		t.Fatal(perr)
	}
	if baseAfter != baseBefore {
		t.Fatalf("base HEAD moved from %s to %s; --ff-only did not refuse the non-fast-forward merge", baseBefore[:7], baseAfter[:7])
	}
	parents, perr := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
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
	if _, err := runGit(ctx, repoPath, "merge", "--no-edit", "feature"); err != nil {
		t.Fatalf("test setup: forcing merge commit failed: %v", err)
	}
	parents, err := runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
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
	parents, err = runGit(ctx, repoPath, "rev-list", "--parents", "-1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Fields(parents); len(parts) > 2 {
		t.Fatalf("merge commit was NOT rolled back; base HEAD still has %d parents: %q", len(parts)-1, parents)
	}
	// ...and restored to exactly the pre-merge base commit.
	baseAfter, err := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if baseAfter != baseBefore {
		t.Fatalf("rollback did not restore base HEAD: want %s, got %s", baseBefore[:7], baseAfter[:7])
	}
}

func TestRebaseAndMerge_CustomBaseBranch(t *testing.T) {
	ctx := context.Background()
	repoPath := initTestRepo(t)

	// Create a "develop" branch from main.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "develop"); err != nil {
		t.Fatal(err)
	}
	// Add a commit to develop.
	if err := os.WriteFile(filepath.Join(repoPath, "develop.txt"), []byte("develop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "develop base"); err != nil {
		t.Fatal(err)
	}

	// Create a feature branch off develop.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Go back to develop.
	if _, err := runGit(ctx, repoPath, "checkout", "develop"); err != nil {
		t.Fatal(err)
	}

	// Create worktree for feature.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Merge feature into develop (not main).
	if err := rebaseAndMerge(ctx, repoPath, worktreePath, "feature", "develop", "", "", nil); err != nil {
		t.Fatalf("rebaseAndMerge to develop failed: %v", err)
	}

	// Verify we're on develop and it has the feature commit.
	branch, err := runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "develop" {
		t.Errorf("expected to be on 'develop', got %q", branch)
	}

	// Verify feature.txt exists on develop.
	if _, err := os.Stat(filepath.Join(repoPath, "feature.txt")); os.IsNotExist(err) {
		t.Error("feature.txt should exist on develop after merge")
	}

	// Verify main is unchanged (doesn't have feature.txt).
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, "feature.txt")); !os.IsNotExist(err) {
		t.Error("feature.txt should NOT exist on main")
	}
}

func TestMarkTaskMerged_SetsStatus(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskMerged(store, "t1", archivePath)

	// Task should be removed from the main file (archived).
	tasks, _ := store.Load()
	if len(tasks) != 0 {
		t.Fatalf("expected 0 remaining tasks, got %d", len(tasks))
	}

	// Task should be in the archive with merged status.
	archiveStore := task.NewFileStore(archivePath)
	archived, _ := archiveStore.Load()
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived task, got %d", len(archived))
	}
	if archived[0].Status != task.StatusMerged {
		t.Fatalf("expected merged, got %s", archived[0].Status)
	}
	if archived[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestMergeOne_NoArchiveKeepsTask(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	// Create a branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
		},
	}

	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
		review:  false,
		archive: false,
		out:     io.Discard,
	})

	if result.Err != nil {
		t.Fatalf("mergeOne failed: %v", result.Err)
	}
	if !result.Merged {
		t.Fatal("expected Merged=true")
	}

	// Task should still be in tasks.yaml (not archived).
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task still in file, got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusMerged {
		t.Fatalf("expected merged status, got %s", tasks[0].Status)
	}
}

// TestMergeOne_ValidationRunsOnRebasedState is the load-bearing regression
// test for the validation-ordering bug. The feature branch is created FIRST,
// then an independent commit is added to the base branch (adding base-only.txt)
// AFTER the branch diverged. The validation command asserts base-only.txt is
// present in the worktree.
//
// That file does NOT exist on the isolated feature branch, only on base. So:
//   - validate-BEFORE-rebase  => validation runs on the isolated branch where
//     base-only.txt is absent => `test -f` fails => mergeOne returns an error.
//   - validate-AFTER-rebase   => the worktree has been rebased onto base, so
//     base-only.txt is present => validation passes => merge succeeds.
//
// Asserting success therefore proves validation saw the combined/rebased state.
func TestMergeOne_ValidationRunsOnRebasedState(t *testing.T) {
	ctx := context.Background()

	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	// Create the feature branch (diverges from the initial commit) and give it
	// its own commit on a DIFFERENT file so the later base commit does not
	// conflict (the rebase must be clean).
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// THEN add a commit to the base branch AFTER the feature branch diverged.
	// base-only.txt exists only on main, not on the isolated feature branch.
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "base-only.txt"), []byte("from base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add base-only file"); err != nil {
		t.Fatal(err)
	}

	// Precondition: histories diverged (the base commit is not yet on feature).
	if _, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("precondition failed: main is already an ancestor of feature (histories did not diverge)")
	}

	worktreeDir := filepath.Join(aptDir, ".worktrees")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			// Validation passes only if base-only.txt is visible — i.e. only if
			// validation runs AFTER the rebase onto base.
			Validate: map[string]string{"myrepo": "test -f base-only.txt"},
		},
	}

	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
		review:  false,
		archive: false,
		out:     io.Discard,
	})

	if result.Err != nil {
		t.Fatalf("mergeOne failed: %v (validation did not see the rebased/combined state)", result.Err)
	}
	if !result.Merged {
		t.Fatal("expected Merged=true; validation must have run on the rebased worktree")
	}
}

// TestMergeOne_ValidationFailureAfterRebaseMarksFailed proves that a validation
// FAILURE occurring after a successful rebase marks the task failed. The rebase
// is clean (feature and base touch different files), so the only way to reach a
// failure is the post-rebase validation step.
func TestMergeOne_ValidationFailureAfterRebaseMarksFailed(t *testing.T) {
	ctx := context.Background()

	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	// Feature branch with its own commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}

	// Advance base with a divergent, non-conflicting commit so a real rebase
	// happens before validation runs.
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "divergent base commit"); err != nil {
		t.Fatal(err)
	}

	worktreeDir := filepath.Join(aptDir, ".worktrees")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	if err := store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
	}); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos:    map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			Validate: map[string]string{"myrepo": "exit 1"}, // always fails post-rebase
		},
	}

	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature"},
		review:  false,
		archive: false,
		out:     io.Discard,
	})

	if result.Err == nil {
		t.Fatal("expected error from post-rebase validation failure, got nil")
	}
	if result.Merged {
		t.Fatal("expected Merged=false on validation failure")
	}
	if !strings.Contains(result.Err.Error(), "validation failed") {
		t.Fatalf("expected validation failure error, got: %s", result.Err)
	}

	// The rebase must have actually happened (proving validation ran post-rebase):
	// the base commit is now an ancestor of the feature branch tip.
	if _, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "main", "feature"); err != nil {
		t.Fatalf("expected feature to have been rebased onto main before validation: %v", err)
	}

	// And the task must be marked failed.
	tasks, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusFailed {
		t.Fatalf("expected failed status, got %s", tasks[0].Status)
	}
}

func TestMarkTaskMergedNoArchive(t *testing.T) {
	store := writeTasks(t, []task.Task{
		{ID: "t1", Status: task.StatusDone},
	})
	markTaskMergedNoArchive(store, "t1")

	tasks, _ := store.Load()
	if len(tasks) != 1 {
		t.Fatalf("expected task to remain, got %d tasks", len(tasks))
	}
	if tasks[0].Status != task.StatusMerged {
		t.Fatalf("expected merged, got %s", tasks[0].Status)
	}
	if tasks[0].FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestMergeOne_SkipValidation(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	// Create a branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: true},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos:    map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			Validate: map[string]string{"myrepo": "false"}, // Validation command that would fail
		},
	}

	var output strings.Builder
	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: true},
		review:  false,
		archive: false,
		out:     &output,
	})

	// Should succeed despite failing validation command because skip_validate=true
	if result.Err != nil {
		t.Fatalf("mergeOne failed: %v", result.Err)
	}
	if !result.Merged {
		t.Fatal("expected Merged=true")
	}

	// Verify the skip message appeared in output
	if !strings.Contains(output.String(), "skipping validation (skip_validate=true)") {
		t.Errorf("expected skip validation message in output, got: %s", output.String())
	}
}

func TestMergeOne_ValidationStillRuns(t *testing.T) {
	ctx := context.Background()

	// Create the apartment directory with a repo inside it.
	aptDir := t.TempDir()
	repoRelPath := "repos/myrepo"
	repoPath := filepath.Join(aptDir, repoRelPath)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Initialize the git repo.
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(ctx, repoPath, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	// Create a branch with a commit.
	if _, err := runGit(ctx, repoPath, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "commit", "-m", "add feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoPath, "checkout", "main"); err != nil {
		t.Fatal(err)
	}

	// Create a worktree for the branch inside the apartment's .worktrees dir.
	worktreeDir := filepath.Join(aptDir, ".worktrees")
	os.MkdirAll(worktreeDir, 0o755)
	worktreePath := filepath.Join(worktreeDir, "t1")
	if _, err := runGit(ctx, repoPath, "worktree", "add", worktreePath, "feature"); err != nil {
		t.Fatal(err)
	}

	// Set up workspace and store.
	tasksPath := filepath.Join(aptDir, "tasks.yaml")
	store := task.NewFileStore(tasksPath)
	store.Save([]task.Task{
		{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: false},
	})

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Repos:    map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
			Validate: map[string]string{"myrepo": "false"}, // Validation command that will fail
		},
	}

	var output strings.Builder
	result := mergeOne(ctx, mergeOneOpts{
		ws:      ws,
		store:   store,
		t:       task.Task{ID: "t1", Status: task.StatusDone, Repo: "myrepo", Branch: "feature", SkipValidate: false},
		review:  false,
		archive: false,
		out:     &output,
	})

	// Should fail due to validation failure since skip_validate=false
	if result.Err == nil {
		t.Fatal("expected mergeOne to fail due to validation failure")
	}
	if result.Merged {
		t.Fatal("expected Merged=false due to validation failure")
	}

	// Verify the validation failed message appeared in output
	if !strings.Contains(output.String(), "failed validation") {
		t.Errorf("expected validation failure message in output, got: %s", output.String())
	}
}
