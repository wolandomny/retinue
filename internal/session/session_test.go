package session_test

import (
	"context"
	"testing"

	"github.com/wolandomny/retinue/internal/session"
)

func TestFakeManager_CreateAndExists(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	exists, err := mgr.Exists(ctx, "mySession")
	if err != nil {
		t.Fatalf("Exists before Create: unexpected error: %v", err)
	}
	if exists {
		t.Fatal("Exists before Create: expected false, got true")
	}

	if err := mgr.Create(ctx, "mySession", "/tmp", "echo hello"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	exists, err = mgr.Exists(ctx, "mySession")
	if err != nil {
		t.Fatalf("Exists after Create: unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("Exists after Create: expected true, got false")
	}
}

func TestFakeManager_CreateDuplicate(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "dup", "/tmp", "sleep 1"); err != nil {
		t.Fatalf("first Create: unexpected error: %v", err)
	}
	if err := mgr.Create(ctx, "dup", "/tmp", "sleep 1"); err == nil {
		t.Fatal("second Create with same name: expected error, got nil")
	}
}

func TestFakeManager_Kill(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "toKill", "/tmp", "sleep 10"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	if err := mgr.Kill(ctx, "toKill"); err != nil {
		t.Fatalf("Kill existing session: unexpected error: %v", err)
	}

	exists, err := mgr.Exists(ctx, "toKill")
	if err != nil {
		t.Fatalf("Exists after Kill: unexpected error: %v", err)
	}
	if exists {
		t.Fatal("Exists after Kill: expected false, got true")
	}
}

func TestFakeManager_KillNonExistent(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Killing a session that does not exist must not return an error.
	if err := mgr.Kill(ctx, "ghost"); err != nil {
		t.Fatalf("Kill non-existent session: expected nil, got: %v", err)
	}
}

func TestFakeManager_Wait(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	if err := mgr.Create(ctx, "waiter", "/tmp", "sleep 1"); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	// Wait should return immediately for the fake.
	if err := mgr.Wait(ctx, "waiter"); err != nil {
		t.Fatalf("Wait: unexpected error: %v", err)
	}
}

func TestTmuxArgs_EmptySocket(t *testing.T) {
	mgr := session.NewTmuxManager("")
	got := mgr.TmuxArgs("new-session", "-d", "-s", "test")
	expected := []string{"new-session", "-d", "-s", "test"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

func TestTmuxArgs_WithSocket(t *testing.T) {
	mgr := session.NewTmuxManager("my-socket")
	got := mgr.TmuxArgs("new-session", "-d", "-s", "test")
	expected := []string{"-L", "my-socket", "new-session", "-d", "-s", "test"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

// Ensure FakeManager satisfies the Manager interface at compile time.
var _ session.Manager = (*session.FakeManager)(nil)
