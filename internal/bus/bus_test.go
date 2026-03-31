package bus

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempBusPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "messages.jsonl")
}

func TestAppendAndReadRecentRoundTrip(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	msgs := []*Message{
		NewMessage("azazello", TypeChat, "hello"),
		NewMessage("user", TypeUser, "hi there"),
		NewMessage("system", TypeSystem, "koroviev has joined"),
	}

	for _, m := range msgs {
		if err := b.Append(m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := b.ReadRecent(10)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	for i, m := range got {
		if m.ID != msgs[i].ID {
			t.Errorf("message %d: expected ID %q, got %q", i, msgs[i].ID, m.ID)
		}
		if m.Text != msgs[i].Text {
			t.Errorf("message %d: expected Text %q, got %q", i, msgs[i].Text, m.Text)
		}
		if m.Name != msgs[i].Name {
			t.Errorf("message %d: expected Name %q, got %q", i, msgs[i].Name, m.Name)
		}
		if m.Type != msgs[i].Type {
			t.Errorf("message %d: expected Type %q, got %q", i, msgs[i].Type, m.Type)
		}
	}
}

func TestReadRecentNonexistentFile(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "nonexistent.jsonl"))

	got, err := b.ReadRecent(10)
	if err != nil {
		t.Fatalf("expected no error for nonexistent file, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d messages", len(got))
	}
}

func TestReadRecentReturnsLastN(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	for i := 0; i < 10; i++ {
		m := NewMessage("agent", TypeChat, "msg")
		if err := b.Append(m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := b.ReadRecent(3)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

func TestReadRecentNGreaterThanTotal(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	m := NewMessage("agent", TypeChat, "only one")
	if err := b.Append(m); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := b.ReadRecent(100)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Text != "only one" {
		t.Fatalf("expected text %q, got %q", "only one", got[0].Text)
	}
}

func TestTailReceivesNewMessages(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	// Create the file with one initial message so tail has something to start from.
	initial := NewMessage("system", TypeSystem, "bus started")
	if err := b.Append(initial); err != nil {
		t.Fatalf("append initial: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := b.Tail(ctx)

	// Wait a moment for the tail goroutine to read past existing content.
	time.Sleep(700 * time.Millisecond)

	// Append a new message after tail has started.
	newMsg := NewMessage("azazello", TypeChat, "live message")
	if err := b.Append(newMsg); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Collect messages from the tail channel.
	var received []*Message
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				goto done
			}
			received = append(received, msg)
			// We expect to see at least the initial message and the new one.
			if len(received) >= 2 {
				goto done
			}
		case <-deadline:
			goto done
		}
	}
done:

	// Check that we received the live message.
	found := false
	for _, m := range received {
		if m.ID == newMsg.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not receive the live message; got %d messages", len(received))
	}
}

func TestTailHandlesFileNotExistingInitially(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")
	b := New(path)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := b.Tail(ctx)

	// Wait a moment, then create the file and write a message.
	time.Sleep(700 * time.Millisecond)

	msg := NewMessage("koroviev", TypeChat, "appeared!")
	if err := b.Append(msg); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Collect the message.
	deadline := time.After(3 * time.Second)
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving message")
		}
		if got.ID != msg.ID {
			t.Fatalf("expected ID %q, got %q", msg.ID, got.ID)
		}
	case <-deadline:
		t.Fatal("timed out waiting for message from tail")
	}
}

func TestConcurrentAppendSafety(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	const goroutines = 20
	const msgsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < msgsPerGoroutine; i++ {
				msg := NewMessage("agent", TypeChat, "concurrent")
				if err := b.Append(msg); err != nil {
					t.Errorf("goroutine %d, msg %d: append: %v", id, i, err)
				}
			}
		}(g)
	}

	wg.Wait()

	got, err := b.ReadRecent(goroutines * msgsPerGoroutine * 2)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	expected := goroutines * msgsPerGoroutine
	if len(got) != expected {
		t.Fatalf("expected %d messages, got %d", expected, len(got))
	}

	// Verify all IDs are unique.
	ids := make(map[string]bool)
	for _, m := range got {
		if ids[m.ID] {
			t.Fatalf("duplicate message ID: %q", m.ID)
		}
		ids[m.ID] = true
	}
}

func TestTailChannelClosedOnCancel(t *testing.T) {
	path := tempBusPath(t)
	b := New(path)

	// Create the file.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ch := b.Tail(ctx)

	cancel()

	// The channel should eventually be closed.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // success — channel closed
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel to close after cancel")
		}
	}
}
