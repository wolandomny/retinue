package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupResetWorkspace creates a temporary workspace directory with a git repo,
// task store, and workspace configuration. It returns the workspace, store,
// and the repo path for making git operations.
func setupResetWorkspace(t *testing.T) (*workspace.Workspace, *task.FileStore, string) {
	t.Helper()
	aptDir := t.TempDir()

	repoRelPath := "repos/myrepo"
	repoDir := filepath.Join(aptDir, repoRelPath)
	initTestRepoAt(t, repoDir)

	// Create logs directory so assessBranchWork can write log files.
	os.MkdirAll(filepath.Join(aptDir, "logs"), 0o755)

	ws := &workspace.Workspace{
		Path: aptDir,
		Config: workspace.Config{
			Name:  "test",
			Repos: map[string]workspace.RepoConfig{"myrepo": {Path: repoRelPath}},
		},
	}

	tasksPath := ws.TasksPath()
	store := task.NewFileStore(tasksPath)

	return ws, store, repoDir
}

// createBranchWithCommit creates a branch off the current HEAD in repoDir
// with one commit, then switches back to main. Returns the branch name.
func createBranchWithCommit(t *testing.T, ctx context.Context, repoDir, branchName, fileName string) {
	t.Helper()

	if _, err := runGit(ctx, repoDir, "checkout", "-b", branchName); err != nil {
		t.Fatalf("creating branch %s: %v", branchName, err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, fileName), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "commit", "-m", "add "+fileName); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
}

// mockAssess returns a function that replaces assessBranchWorkFunc for testing.
// It returns the given verdict and explanation.
func mockAssess(verdict, explanation string) func(context.Context, *workspace.Workspace, string, string, string, task.Task) (string, string, error) {
	return func(_ context.Context, _ *workspace.Workspace, _, _, _ string, _ task.Task) (string, string, error) {
		return verdict, explanation, nil
	}
}

// ---------------------------------------------------------------------------
// 1. Task scanning
// ---------------------------------------------------------------------------

func TestReset_FindsInProgressTasks(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	if err := store.Save([]task.Task{
		{ID: "pending-1", Status: task.StatusPending},
		{ID: "inprog-1", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
		{ID: "done-1", Status: task.StatusDone},
		{ID: "failed-1", Status: task.StatusFailed},
		{ID: "inprog-2", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only find the two in_progress tasks.
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}

	ids := map[string]bool{}
	for _, r := range results {
		ids[r.TaskID] = true
	}
	if !ids["inprog-1"] || !ids["inprog-2"] {
		t.Errorf("expected inprog-1 and inprog-2, got %v", ids)
	}
}

func TestReset_StaleFilter(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	recent := time.Now().Add(-10 * time.Minute)
	old := time.Now().Add(-2 * time.Hour)

	if err := store.Save([]task.Task{
		{ID: "recent", Status: task.StatusInProgress, StartedAt: &recent, Repo: "myrepo"},
		{ID: "old", Status: task.StatusInProgress, StartedAt: &old, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		DryRun: true,
		Stale:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the old task should be included.
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].TaskID != "old" {
		t.Errorf("expected old task, got %s", results[0].TaskID)
	}
}

func TestReset_FailedFlag(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	if err := store.Save([]task.Task{
		{ID: "inprog-1", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
		{ID: "failed-1", Status: task.StatusFailed, Repo: "myrepo"},
		{ID: "failed-2", Status: task.StatusFailed, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	// Without --failed, only in_progress tasks are recovered.
	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		DryRun: true,
		Failed: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("without --failed: expected 1 result, got %d", len(results))
	}

	// With --failed, failed tasks are also included.
	buf.Reset()
	results, err = RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		DryRun: true,
		Failed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("with --failed: expected 3 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// 2. Tmux liveness detection
// ---------------------------------------------------------------------------

func TestReset_SkipsAliveWorker(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	tk := task.Task{
		ID:        "alive-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Meta:      map[string]string{"session": "alive-task"},
	}

	fakeMgr := session.NewFakeManager()
	// Simulate a live window.
	if err := fakeMgr.CreateWindow(ctx, session.ApartmentSession, "alive-task", "/tmp", "claude"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{
		Force: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "skipped" {
		t.Errorf("expected skipped, got %s", result.Action)
	}
	if !strings.Contains(buf.String(), "window alive") {
		t.Errorf("expected 'window alive' in output, got: %s", buf.String())
	}
}

func TestReset_ForceKillsAliveWorker(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	tk := task.Task{
		ID:        "force-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Meta:      map[string]string{"session": "force-task"},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	fakeMgr := session.NewFakeManager()
	// Simulate a live window.
	if err := fakeMgr.CreateWindow(ctx, session.ApartmentSession, "force-task", "/tmp", "claude"); err != nil {
		t.Fatal(err)
	}

	// Verify window exists before the call.
	alive, _ := fakeMgr.HasWindow(ctx, session.ApartmentSession, "force-task")
	if !alive {
		t.Fatal("window should be alive before force kill")
	}

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{
		Force: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have been reset (no branch exists, so simple reset).
	if result.Action != "reset" {
		t.Errorf("expected reset action, got %s", result.Action)
	}

	// Window should have been killed.
	alive, _ = fakeMgr.HasWindow(ctx, session.ApartmentSession, "force-task")
	if alive {
		t.Error("window should have been killed")
	}

	// Task should be pending.
	updated, err := store.Get("force-task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusPending {
		t.Errorf("expected pending, got %s", updated.Status)
	}

	// Verify detail mentions --force.
	if !strings.Contains(result.Detail, "--force") {
		t.Errorf("expected '--force' in detail, got: %s", result.Detail)
	}

	_ = repoDir // suppress unused variable if needed
}

// ---------------------------------------------------------------------------
// 3. Simple reset (no commits)
// ---------------------------------------------------------------------------

func TestReset_NoCommits_ResetsToPending(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	tk := task.Task{
		ID:        "reset-me",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    "retinue/reset-me",
		Error:     "some old error",
		Result:    "partial result",
		Meta: map[string]string{
			"session":       "reset-me",
			"cost_usd":      "0.50",
			"input_tokens":  "1000",
			"output_tokens": "500",
			"priority":      "high",
			"category":      "backend",
		},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "reset" {
		t.Errorf("expected reset, got %s", result.Action)
	}

	// Verify task state was fully reset.
	updated, err := store.Get("reset-me")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusPending {
		t.Errorf("expected pending, got %s", updated.Status)
	}
	if updated.Branch != "" {
		t.Errorf("expected empty branch, got %q", updated.Branch)
	}
	if updated.StartedAt != nil {
		t.Error("expected StartedAt to be nil")
	}
	if updated.FinishedAt != nil {
		t.Error("expected FinishedAt to be nil")
	}
	if updated.Result != "" {
		t.Errorf("expected empty result, got %q", updated.Result)
	}
	if updated.Error != "" {
		t.Errorf("expected empty error, got %q", updated.Error)
	}
}

func TestReset_ClearsRuntimeMeta_PreservesUserMeta(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	tk := task.Task{
		ID:        "meta-test",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Meta: map[string]string{
			"session":              "meta-test",
			"cost_usd":            "1.50",
			"input_tokens":        "5000",
			"output_tokens":       "2000",
			"replan_input_tokens":  "100",
			"replan_output_tokens": "50",
			"replan_cost_usd":     "0.10",
			"review_tokens":       "300",
			"priority":            "high",
			"category":            "backend",
			"custom_field":        "keep me",
		},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	_, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := store.Get("meta-test")
	if err != nil {
		t.Fatal(err)
	}

	// Runtime meta should be cleared.
	for _, key := range runtimeMetaKeys {
		if val, ok := updated.Meta[key]; ok {
			t.Errorf("runtime meta key %q should be cleared, but has value %q", key, val)
		}
	}

	// User meta should be preserved.
	if updated.Meta["priority"] != "high" {
		t.Errorf("expected priority=high, got %q", updated.Meta["priority"])
	}
	if updated.Meta["category"] != "backend" {
		t.Errorf("expected category=backend, got %q", updated.Meta["category"])
	}
	if updated.Meta["custom_field"] != "keep me" {
		t.Errorf("expected custom_field='keep me', got %q", updated.Meta["custom_field"])
	}
}

func TestReset_Idempotent(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	tk := task.Task{
		ID:        "idempotent",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Meta: map[string]string{
			"session":  "idempotent",
			"cost_usd": "0.50",
		},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	fakeMgr := session.NewFakeManager()

	// First reset.
	var buf bytes.Buffer
	result1, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if result1.Action != "reset" {
		t.Fatalf("first reset: expected reset action, got %s", result1.Action)
	}

	// Reload the task after first reset (it's now pending).
	updated, err := store.Get("idempotent")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusPending {
		t.Fatalf("expected pending after first reset, got %s", updated.Status)
	}

	// Make it in_progress again to simulate running reset a second time on
	// a task that was already reset.
	now2 := time.Now()
	if err := store.Update("idempotent", func(tk *task.Task) {
		tk.Status = task.StatusInProgress
		tk.StartedAt = &now2
	}); err != nil {
		t.Fatal(err)
	}

	updatedAgain, _ := store.Get("idempotent")

	// Second reset — should succeed without errors.
	buf.Reset()
	result2, err := recoverInProgressTask(ctx, ws, store, fakeMgr, *updatedAgain, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if result2.Action != "reset" {
		t.Errorf("second reset: expected reset action, got %s", result2.Action)
	}

	final, _ := store.Get("idempotent")
	if final.Status != task.StatusPending {
		t.Errorf("expected pending after second reset, got %s", final.Status)
	}
}

// ---------------------------------------------------------------------------
// 4. AI assessment (has commits)
// ---------------------------------------------------------------------------

func TestReset_WithCommits_AssessesCompletion(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	// Create a branch with commits.
	branch := "retinue/assess-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "feature.txt")

	now := time.Now()
	tk := task.Task{
		ID:        "assess-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    branch,
		Prompt:    "Add a feature file",
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	// Track whether assess was called.
	assessCalled := false
	orig := assessBranchWorkFunc
	assessBranchWorkFunc = func(_ context.Context, _ *workspace.Workspace, _, _, _ string, _ task.Task) (string, string, error) {
		assessCalled = true
		return "COMPLETE", "Task looks done", nil
	}
	defer func() { assessBranchWorkFunc = orig }()

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	_, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !assessCalled {
		t.Error("expected assessBranchWorkFunc to be called for branch with commits")
	}
}

func TestReset_Assessment_Complete(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	branch := "retinue/complete-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "complete.txt")

	now := time.Now()
	tk := task.Task{
		ID:        "complete-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    branch,
		Prompt:    "Create complete.txt",
		Meta: map[string]string{
			"session":  "complete-task",
			"cost_usd": "1.00",
		},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	orig := assessBranchWorkFunc
	assessBranchWorkFunc = mockAssess("COMPLETE", "All work is done and tests pass")
	defer func() { assessBranchWorkFunc = orig }()

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "done" {
		t.Errorf("expected action=done, got %s", result.Action)
	}

	updated, err := store.Get("complete-task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusDone {
		t.Errorf("expected done, got %s", updated.Status)
	}
	if updated.Result != "All work is done and tests pass" {
		t.Errorf("expected explanation in Result, got %q", updated.Result)
	}
	if updated.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
	// Runtime meta should be cleared.
	if _, ok := updated.Meta["cost_usd"]; ok {
		t.Error("expected cost_usd to be cleared")
	}

	if !strings.Contains(buf.String(), "COMPLETE") {
		t.Errorf("expected COMPLETE in output, got: %s", buf.String())
	}
}

func TestReset_Assessment_Incomplete(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	branch := "retinue/incomplete-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "partial.txt")

	now := time.Now()
	tk := task.Task{
		ID:        "incomplete-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    branch,
		Prompt:    "Implement full feature",
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	orig := assessBranchWorkFunc
	assessBranchWorkFunc = mockAssess("INCOMPLETE", "Only partial implementation found")
	defer func() { assessBranchWorkFunc = orig }()

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "failed" {
		t.Errorf("expected action=failed, got %s", result.Action)
	}

	updated, err := store.Get("incomplete-task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusFailed {
		t.Errorf("expected failed, got %s", updated.Status)
	}
	if updated.Error != "Only partial implementation found" {
		t.Errorf("expected explanation in Error, got %q", updated.Error)
	}
	if updated.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}

	if !strings.Contains(buf.String(), "INCOMPLETE") {
		t.Errorf("expected INCOMPLETE in output, got: %s", buf.String())
	}
}

func TestReset_Assessment_Broken(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	branch := "retinue/broken-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "broken.txt")

	now := time.Now()
	tk := task.Task{
		ID:        "broken-task",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    branch,
		Prompt:    "Fix the bug",
		Meta: map[string]string{
			"session":  "broken-task",
			"cost_usd": "2.00",
			"priority": "critical",
		},
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	orig := assessBranchWorkFunc
	assessBranchWorkFunc = mockAssess("BROKEN", "Changes are harmful")
	defer func() { assessBranchWorkFunc = orig }()

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	result, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "reset" {
		t.Errorf("expected action=reset, got %s", result.Action)
	}
	if !strings.Contains(result.Detail, "BROKEN") {
		t.Errorf("expected BROKEN in detail, got: %s", result.Detail)
	}

	updated, err := store.Get("broken-task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusPending {
		t.Errorf("expected pending after BROKEN, got %s", updated.Status)
	}
	if updated.Branch != "" {
		t.Errorf("expected empty branch after BROKEN, got %q", updated.Branch)
	}
	if updated.StartedAt != nil {
		t.Error("expected StartedAt to be nil")
	}
	// User meta should be preserved.
	if updated.Meta["priority"] != "critical" {
		t.Errorf("expected priority=critical preserved, got %q", updated.Meta["priority"])
	}
	// Runtime meta should be cleared.
	if _, ok := updated.Meta["cost_usd"]; ok {
		t.Error("expected cost_usd to be cleared")
	}

	// Branch should have been deleted.
	_, err = runGit(ctx, repoDir, "rev-parse", "--verify", branch)
	if err == nil {
		t.Error("expected branch to be deleted after BROKEN reset")
	}

	if !strings.Contains(buf.String(), "BROKEN") {
		t.Errorf("expected BROKEN in output, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// 5. Failed task reset
// ---------------------------------------------------------------------------

func TestReset_FailedTasks_ResetToPending(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	// Create a branch that would exist from a previous failed run.
	branch := "retinue/failed-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "failed-work.txt")

	now := time.Now()
	finished := time.Now()
	if err := store.Save([]task.Task{
		{
			ID:         "failed-task",
			Status:     task.StatusFailed,
			StartedAt:  &now,
			FinishedAt: &finished,
			Repo:       "myrepo",
			Branch:     branch,
			Error:      "previous failure reason",
			Result:     "partial work",
			Meta: map[string]string{
				"session":  "failed-task",
				"cost_usd": "0.75",
				"priority": "medium",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		Failed: true,
		DryRun: false,
		// Need --all for non-dry run, but Failed tasks are not filtered by DryRun
		// the same way. Let me use TaskID to target it.
		TaskID: "failed-task",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "reset" {
		t.Errorf("expected reset action, got %s", results[0].Action)
	}

	updated, err := store.Get("failed-task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != task.StatusPending {
		t.Errorf("expected pending, got %s", updated.Status)
	}
	if updated.Branch != "" {
		t.Errorf("expected empty branch, got %q", updated.Branch)
	}
	if updated.StartedAt != nil {
		t.Error("expected StartedAt to be nil")
	}
	if updated.FinishedAt != nil {
		t.Error("expected FinishedAt to be nil")
	}
	if updated.Error != "" {
		t.Errorf("expected empty error, got %q", updated.Error)
	}
	if updated.Result != "" {
		t.Errorf("expected empty result, got %q", updated.Result)
	}

	// User meta preserved, runtime cleared.
	if updated.Meta["priority"] != "medium" {
		t.Errorf("expected priority=medium, got %q", updated.Meta["priority"])
	}
	if _, ok := updated.Meta["cost_usd"]; ok {
		t.Error("expected cost_usd to be cleared")
	}

	// Branch should have been deleted.
	_, err = runGit(ctx, repoDir, "rev-parse", "--verify", branch)
	if err == nil {
		t.Error("expected branch to be deleted")
	}
}

// ---------------------------------------------------------------------------
// 6. Dry run
// ---------------------------------------------------------------------------

func TestReset_DryRun(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	// Create a branch with commits for one task.
	branch := "retinue/dry-task"
	createBranchWithCommit(t, ctx, repoDir, branch, "dry.txt")

	now := time.Now()
	if err := store.Save([]task.Task{
		{ID: "dry-task", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo", Branch: branch, Prompt: "Add dry.txt"},
		{ID: "simple-task", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	// Save a snapshot of the tasks to compare after dry run.
	tasksBefore, _ := store.Load()

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Dry run should return results but not modify anything.
	if len(results) == 0 {
		t.Fatal("expected results in dry run")
	}

	// Verify no task state was changed.
	tasksAfter, _ := store.Load()
	if len(tasksAfter) != len(tasksBefore) {
		t.Fatalf("task count changed: before=%d, after=%d", len(tasksBefore), len(tasksAfter))
	}
	for i, before := range tasksBefore {
		after := tasksAfter[i]
		if before.Status != after.Status {
			t.Errorf("task %s status changed from %s to %s", before.ID, before.Status, after.Status)
		}
		if before.Branch != after.Branch {
			t.Errorf("task %s branch changed from %q to %q", before.ID, before.Branch, after.Branch)
		}
	}

	// Branch should still exist.
	if _, err := runGit(ctx, repoDir, "rev-parse", "--verify", branch); err != nil {
		t.Error("branch should still exist after dry run")
	}

	// Output should mention dry run.
	output := buf.String()
	if !strings.Contains(output, "dry run") {
		t.Errorf("expected 'dry run' in output, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// Additional edge case tests
// ---------------------------------------------------------------------------

func TestReset_TaskNotFound(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	if err := store.Save([]task.Task{
		{ID: "existing", Status: task.StatusInProgress},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	_, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		TaskID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestReset_NoTasksToRecover(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	if err := store.Save([]task.Task{
		{ID: "pending-1", Status: task.StatusPending},
		{ID: "done-1", Status: task.StatusDone},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestReset_WorktreeCleanup(t *testing.T) {
	ws, store, repoDir := setupResetWorkspace(t)
	ctx := context.Background()

	// Create a branch and worktree.
	branch := "retinue/wt-cleanup"
	createBranchWithCommit(t, ctx, repoDir, branch, "wt.txt")

	worktreePath := filepath.Join(ws.Path, workspace.WorktreeDir, "wt-cleanup")
	os.MkdirAll(filepath.Dir(worktreePath), 0o755)
	if _, err := runGit(ctx, repoDir, "worktree", "add", worktreePath, branch); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	// Verify worktree exists.
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree should exist: %v", err)
	}

	now := time.Now()
	tk := task.Task{
		ID:        "wt-cleanup",
		Status:    task.StatusInProgress,
		StartedAt: &now,
		Repo:      "myrepo",
		Branch:    branch,
		Prompt:    "Test worktree cleanup",
	}

	if err := store.Save([]task.Task{tk}); err != nil {
		t.Fatal(err)
	}

	// Use BROKEN assessment to trigger full reset with cleanup.
	orig := assessBranchWorkFunc
	assessBranchWorkFunc = mockAssess("BROKEN", "Bad changes")
	defer func() { assessBranchWorkFunc = orig }()

	fakeMgr := session.NewFakeManager()

	var buf bytes.Buffer
	_, err := recoverInProgressTask(ctx, ws, store, fakeMgr, tk, &buf, RecoverOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Worktree directory should be cleaned up.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("worktree directory should have been removed")
	}

	// Branch should be deleted.
	_, err = runGit(ctx, repoDir, "rev-parse", "--verify", branch)
	if err == nil {
		t.Error("branch should have been deleted")
	}
}

func TestClearRuntimeMeta_NilMeta(t *testing.T) {
	tk := &task.Task{ID: "test", Meta: nil}
	clearRuntimeMeta(tk) // should not panic
	if tk.Meta != nil {
		t.Error("expected nil meta to remain nil")
	}
}

func TestClearRuntimeMeta_EmptyAfterClear(t *testing.T) {
	tk := &task.Task{
		ID: "test",
		Meta: map[string]string{
			"session":  "s1",
			"cost_usd": "1.00",
		},
	}
	clearRuntimeMeta(tk)
	// Meta should be nil when all keys were runtime keys.
	if tk.Meta != nil {
		t.Errorf("expected nil meta when all keys cleared, got %v", tk.Meta)
	}
}

func TestClearRuntimeMeta_PreservesUserKeys(t *testing.T) {
	tk := &task.Task{
		ID: "test",
		Meta: map[string]string{
			"session":      "s1",
			"cost_usd":     "1.00",
			"custom_field": "keep",
		},
	}
	clearRuntimeMeta(tk)
	if tk.Meta == nil {
		t.Fatal("meta should not be nil when user keys remain")
	}
	if tk.Meta["custom_field"] != "keep" {
		t.Errorf("expected custom_field=keep, got %q", tk.Meta["custom_field"])
	}
	if _, ok := tk.Meta["session"]; ok {
		t.Error("session should have been removed")
	}
}

func TestNewResetCmd_Flags(t *testing.T) {
	cmd := newResetCmd()

	if cmd.Use != "reset" {
		t.Errorf("expected Use 'reset', got %q", cmd.Use)
	}

	for _, flag := range []string{"all", "task", "failed", "stale", "force"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected --%s flag", flag)
		}
	}
}

func TestReset_SpecificTaskByID(t *testing.T) {
	ws, store, _ := setupResetWorkspace(t)
	ctx := context.Background()

	now := time.Now()
	if err := store.Save([]task.Task{
		{ID: "task-a", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
		{ID: "task-b", Status: task.StatusInProgress, StartedAt: &now, Repo: "myrepo"},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	results, err := RecoverStuckTasks(ctx, ws, store, &buf, RecoverOpts{
		TaskID: "task-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only task-a should be recovered.
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TaskID != "task-a" {
		t.Errorf("expected task-a, got %s", results[0].TaskID)
	}

	// task-a should be pending, task-b should still be in_progress.
	a, _ := store.Get("task-a")
	b, _ := store.Get("task-b")
	if a.Status != task.StatusPending {
		t.Errorf("task-a: expected pending, got %s", a.Status)
	}
	if b.Status != task.StatusInProgress {
		t.Errorf("task-b: expected in_progress (untouched), got %s", b.Status)
	}
}
