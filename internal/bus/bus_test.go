package bus

import (
	"context"
	"encoding/json"
	"fmt"
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

// ---------------------------------------------------------------------------
// 1. File truncation recovery
// ---------------------------------------------------------------------------

func TestTailFileTruncationRecovery(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.Tail(ctx)

	// Write 3 initial messages.
	initial := make([]*Message, 3)
	for i := 0; i < 3; i++ {
		initial[i] = NewMessage("agent", TypeChat, fmt.Sprintf("pre-truncate-%d", i))
		if err := b.Append(initial[i]); err != nil {
			t.Fatalf("append initial[%d]: %v", i, err)
		}
	}

	// Collect the 3 initial messages.
	received := collectMessages(t, ch, 3, 5*time.Second)
	if len(received) < 3 {
		t.Fatalf("expected at least 3 messages before truncation, got %d", len(received))
	}

	// Truncate the file.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Write 2 new messages after truncation.
	post := make([]*Message, 2)
	for i := 0; i < 2; i++ {
		post[i] = NewMessage("agent", TypeChat, fmt.Sprintf("post-truncate-%d", i))
		if err := b.Append(post[i]); err != nil {
			t.Fatalf("append post[%d]: %v", i, err)
		}
	}

	// Collect the 2 post-truncation messages.
	postReceived := collectMessages(t, ch, 2, 5*time.Second)
	if len(postReceived) < 2 {
		t.Fatalf("expected at least 2 messages after truncation, got %d", len(postReceived))
	}

	// Verify both post-truncation messages were received.
	for _, want := range post {
		found := false
		for _, got := range postReceived {
			if got.ID == want.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("post-truncation message %q not received", want.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Partial line buffering
// ---------------------------------------------------------------------------

func TestTailPartialLineBuffering(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	// Create the file so Tail can start reading.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.Tail(ctx)

	// Wait for the tailer to start polling.
	time.Sleep(700 * time.Millisecond)

	// Write a partial JSON line (no trailing newline).
	msg := NewMessage("agent", TypeChat, "partial-test")
	data, _ := json.Marshal(msg)
	half1 := data[:len(data)/2]
	half2 := data[len(data)/2:]

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for partial write: %v", err)
	}
	if _, err := f.Write(half1); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	f.Close()

	// Wait for a poll cycle — nothing should arrive.
	select {
	case got := <-ch:
		t.Fatalf("received message from partial line: %+v", got)
	case <-time.After(1200 * time.Millisecond):
		// Good — no message emitted for the partial line.
	}

	// Complete the line by writing the remaining bytes + newline.
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for completing write: %v", err)
	}
	if _, err := f.Write(append(half2, '\n')); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	f.Close()

	// Now the complete message should arrive.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving completed message")
		}
		if got.ID != msg.ID {
			t.Errorf("expected ID %q, got %q", msg.ID, got.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for completed message")
	}
}

func TestTailPartialLineMultipleCompleteFollowedByPartial(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.Tail(ctx)
	time.Sleep(700 * time.Millisecond)

	// Write two complete messages and one partial message in a single write.
	msg1 := NewMessage("agent", TypeChat, "complete-1")
	msg2 := NewMessage("agent", TypeChat, "complete-2")
	msgPartial := NewMessage("agent", TypeChat, "partial")

	data1, _ := json.Marshal(msg1)
	data2, _ := json.Marshal(msg2)
	dataPartial, _ := json.Marshal(msgPartial)

	// Two complete lines followed by a partial (no newline).
	payload := append(data1, '\n')
	payload = append(payload, append(data2, '\n')...)
	payload = append(payload, dataPartial[:len(dataPartial)/2]...)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	// Should receive exactly 2 complete messages.
	received := collectMessages(t, ch, 2, 3*time.Second)
	if len(received) != 2 {
		t.Fatalf("expected 2 complete messages, got %d", len(received))
	}

	// Verify no third message arrives yet (partial is buffered).
	select {
	case got := <-ch:
		t.Fatalf("unexpected message from partial line: %+v", got)
	case <-time.After(1200 * time.Millisecond):
		// Good — partial still buffered.
	}

	// Complete the partial line.
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(append(dataPartial[len(dataPartial)/2:], '\n')); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	f.Close()

	// Now the third message should arrive.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		if got.ID != msgPartial.ID {
			t.Errorf("expected ID %q, got %q", msgPartial.ID, got.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for completed partial message")
	}
}

// ---------------------------------------------------------------------------
// 3. Malformed JSONL resilience
// ---------------------------------------------------------------------------

func TestReadRecentSkipsMalformedLines(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	// Write a valid message.
	msg1 := NewMessage("agent", TypeChat, "valid-1")
	if err := b.Append(msg1); err != nil {
		t.Fatalf("append msg1: %v", err)
	}

	// Write garbage directly to the file.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()

	// Write another valid message.
	msg2 := NewMessage("agent", TypeChat, "valid-2")
	if err := b.Append(msg2); err != nil {
		t.Fatalf("append msg2: %v", err)
	}

	got, err := b.ReadRecent(10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 valid messages, got %d", len(got))
	}
	if got[0].ID != msg1.ID {
		t.Errorf("first message: expected ID %q, got %q", msg1.ID, got[0].ID)
	}
	if got[1].ID != msg2.ID {
		t.Errorf("second message: expected ID %q, got %q", msg2.ID, got[1].ID)
	}
}

func TestTailSkipsMalformedLines(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.Tail(ctx)
	time.Sleep(700 * time.Millisecond)

	// Write valid message, garbage, then valid message.
	msg1 := NewMessage("agent", TypeChat, "tail-valid-1")
	if err := b.Append(msg1); err != nil {
		t.Fatalf("append msg1: %v", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()

	msg2 := NewMessage("agent", TypeChat, "tail-valid-2")
	if err := b.Append(msg2); err != nil {
		t.Fatalf("append msg2: %v", err)
	}

	// Collect messages — we expect the two valid ones, with garbage skipped.
	received := collectMessages(t, ch, 2, 5*time.Second)

	if len(received) != 2 {
		t.Fatalf("expected 2 messages from tail (skipping garbage), got %d", len(received))
	}

	ids := map[string]bool{received[0].ID: true, received[1].ID: true}
	if !ids[msg1.ID] {
		t.Errorf("missing msg1 (ID %q) from tail output", msg1.ID)
	}
	if !ids[msg2.ID] {
		t.Errorf("missing msg2 (ID %q) from tail output", msg2.ID)
	}
}

// ---------------------------------------------------------------------------
// 4. Concurrent append safety (stress test)
// ---------------------------------------------------------------------------

func TestConcurrentAppendSafetyStress(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	const goroutines = 20
	const msgsPerGoroutine = 10
	const totalMsgs = goroutines * msgsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)

	allIDs := make(chan string, totalMsgs)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < msgsPerGoroutine; i++ {
				msg := NewMessage(
					fmt.Sprintf("agent-%d", id),
					TypeChat,
					fmt.Sprintf("g%d-m%d", id, i),
				)
				allIDs <- msg.ID
				if err := b.Append(msg); err != nil {
					t.Errorf("goroutine %d, msg %d: append: %v", id, i, err)
				}
			}
		}(g)
	}

	wg.Wait()
	close(allIDs)

	// Collect all expected IDs.
	expectedIDs := make(map[string]bool)
	for id := range allIDs {
		expectedIDs[id] = true
	}

	got, err := b.ReadRecent(totalMsgs * 2)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}

	if len(got) != totalMsgs {
		t.Fatalf("expected %d messages, got %d", totalMsgs, len(got))
	}

	// Verify every message is valid JSON (it was deserialized by ReadRecent)
	// and all unique IDs are present.
	seenIDs := make(map[string]bool)
	for _, m := range got {
		if seenIDs[m.ID] {
			t.Fatalf("duplicate message ID: %q", m.ID)
		}
		seenIDs[m.ID] = true

		// Verify this is a valid, complete message.
		if m.Name == "" || m.Type == "" || m.Text == "" {
			t.Errorf("message appears corrupted: %+v", m)
		}
	}

	// Verify all expected IDs were found.
	for id := range expectedIDs {
		if !seenIDs[id] {
			t.Errorf("expected ID %q not found in ReadRecent output", id)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Tail on nonexistent file
// ---------------------------------------------------------------------------

func TestTailOnNonexistentFileWaitsThenReceives(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet-created.jsonl")
	b := New(path)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.Tail(ctx)

	// Wait 100ms — nothing should be received, no panic, no error.
	select {
	case got := <-ch:
		t.Fatalf("received message before file exists: %+v", got)
	case <-time.After(100 * time.Millisecond):
		// Good — nothing received.
	}

	// Now create the file and write a message.
	msg := NewMessage("agent", TypeChat, "hello from new file")
	if err := b.Append(msg); err != nil {
		t.Fatalf("append: %v", err)
	}

	// The message should arrive.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving message")
		}
		if got.ID != msg.ID {
			t.Errorf("expected ID %q, got %q", msg.ID, got.ID)
		}
		if got.Text != "hello from new file" {
			t.Errorf("expected Text %q, got %q", "hello from new file", got.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message after file creation")
	}
}

// ---------------------------------------------------------------------------
// 6. ReadRecent edge cases
// ---------------------------------------------------------------------------

func TestReadRecentZero(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	// Write some messages.
	for i := 0; i < 5; i++ {
		if err := b.Append(NewMessage("agent", TypeChat, "msg")); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := b.ReadRecent(0)
	if err != nil {
		t.Fatalf("ReadRecent(0): %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("ReadRecent(0): expected empty slice, got %d messages", len(got))
	}
}

func TestReadRecentEmptyFile(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)

	// Create an empty file.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("create empty file: %v", err)
	}

	b := New(path)
	got, err := b.ReadRecent(10)
	if err != nil {
		t.Fatalf("ReadRecent on empty file: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty slice for empty file, got %d messages", len(got))
	}
}

func TestReadRecentLargeNOnSmallFile(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	msgs := []*Message{
		NewMessage("a", TypeChat, "one"),
		NewMessage("b", TypeChat, "two"),
		NewMessage("c", TypeChat, "three"),
	}
	for _, m := range msgs {
		if err := b.Append(m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := b.ReadRecent(1000)
	if err != nil {
		t.Fatalf("ReadRecent(1000): %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	for i, m := range got {
		if m.ID != msgs[i].ID {
			t.Errorf("message %d: expected ID %q, got %q", i, msgs[i].ID, m.ID)
		}
	}
}

func TestReadRecentMalformedLinesSkipped(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	msg1 := NewMessage("agent", TypeChat, "good-1")
	if err := b.Append(msg1); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Write several kinds of garbage.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	garbage := []string{
		"this is not json\n",
		"{invalid json\n",
		"\n", // empty line
		"12345\n",
	}
	for _, g := range garbage {
		if _, err := f.Write([]byte(g)); err != nil {
			t.Fatalf("write garbage: %v", err)
		}
	}
	f.Close()

	msg2 := NewMessage("agent", TypeChat, "good-2")
	if err := b.Append(msg2); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := b.ReadRecent(100)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 valid messages (skipping garbage), got %d", len(got))
	}
	if got[0].ID != msg1.ID {
		t.Errorf("first: expected %q, got %q", msg1.ID, got[0].ID)
	}
	if got[1].ID != msg2.ID {
		t.Errorf("second: expected %q, got %q", msg2.ID, got[1].ID)
	}
}

// ---------------------------------------------------------------------------
// 7. Append to nonexistent directory
// ---------------------------------------------------------------------------

func TestAppendToNonexistentDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "does", "not", "exist", "messages.jsonl")
	b := New(path)

	msg := NewMessage("agent", TypeChat, "should fail")
	err := b.Append(msg)

	// The parent directory doesn't exist, so OpenFile should fail.
	if err == nil {
		t.Fatal("expected error when appending to nonexistent directory, got nil")
	}
}

// ---------------------------------------------------------------------------
// 8. TailFromEnd — skip existing messages
// ---------------------------------------------------------------------------

func TestTailFromEndSkipsExistingMessages(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	// Write some existing messages before calling TailFromEnd.
	for i := 0; i < 5; i++ {
		msg := NewMessage("agent", TypeChat, fmt.Sprintf("old-%d", i))
		if err := b.Append(msg); err != nil {
			t.Fatalf("append old message %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.TailFromEnd(ctx)

	// Wait for the tailer to start and observe the existing file size.
	time.Sleep(700 * time.Millisecond)

	// Verify no existing messages are emitted.
	select {
	case got := <-ch:
		t.Fatalf("received old message that should have been skipped: %+v", got)
	case <-time.After(1200 * time.Millisecond):
		// Good — no old messages emitted.
	}

	// Append a new message after TailFromEnd started.
	newMsg := NewMessage("agent", TypeChat, "new-message")
	if err := b.Append(newMsg); err != nil {
		t.Fatalf("append new message: %v", err)
	}

	// Verify the new message IS emitted.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving new message")
		}
		if got.ID != newMsg.ID {
			t.Errorf("expected ID %q, got %q", newMsg.ID, got.ID)
		}
		if got.Text != "new-message" {
			t.Errorf("expected Text %q, got %q", "new-message", got.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for new message from TailFromEnd")
	}
}

func TestTailFromEndFileNotExistYet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet.jsonl")
	b := New(path)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.TailFromEnd(ctx)

	// Wait a bit — file doesn't exist, nothing should arrive.
	select {
	case got := <-ch:
		t.Fatalf("received message before file exists: %+v", got)
	case <-time.After(100 * time.Millisecond):
		// Good — nothing received.
	}

	// Create the file with an initial message. Because Append creates the
	// file and writes atomically, TailFromEnd will see the file with content
	// already present and set offset = fileSize, skipping this message.
	initialMsg := NewMessage("agent", TypeChat, "initial-at-creation")
	if err := b.Append(initialMsg); err != nil {
		t.Fatalf("append initial: %v", err)
	}

	// Wait for TailFromEnd to discover the file and set its offset.
	time.Sleep(1200 * time.Millisecond)

	// The initial message should NOT have been emitted — it was present when
	// the file was first observed.
	select {
	case got := <-ch:
		t.Fatalf("received message that was present at file discovery: %+v", got)
	case <-time.After(1200 * time.Millisecond):
		// Good — initial message correctly skipped.
	}

	// Now append a truly new message.
	newMsg := NewMessage("agent", TypeChat, "after-discovery")
	if err := b.Append(newMsg); err != nil {
		t.Fatalf("append new: %v", err)
	}

	// The new message should arrive.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving message")
		}
		if got.ID != newMsg.ID {
			t.Errorf("expected ID %q, got %q", newMsg.ID, got.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for new message after file creation")
	}
}

func TestTailFromEndMultipleNewMessages(t *testing.T) {
	t.Parallel()

	path := tempBusPath(t)
	b := New(path)

	// Write existing messages.
	for i := 0; i < 3; i++ {
		if err := b.Append(NewMessage("agent", TypeChat, fmt.Sprintf("old-%d", i))); err != nil {
			t.Fatalf("append old: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := b.TailFromEnd(ctx)

	// Wait for tailer to initialize.
	time.Sleep(700 * time.Millisecond)

	// Append several new messages.
	newMsgs := make([]*Message, 3)
	for i := 0; i < 3; i++ {
		newMsgs[i] = NewMessage("agent", TypeChat, fmt.Sprintf("new-%d", i))
		if err := b.Append(newMsgs[i]); err != nil {
			t.Fatalf("append new[%d]: %v", i, err)
		}
	}

	// Collect exactly 3 messages.
	received := collectMessages(t, ch, 3, 5*time.Second)
	if len(received) != 3 {
		t.Fatalf("expected 3 new messages, got %d", len(received))
	}

	// Verify all received messages are the new ones, not old ones.
	for i, got := range received {
		if got.ID != newMsgs[i].ID {
			t.Errorf("message %d: expected ID %q, got %q", i, newMsgs[i].ID, got.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// collectMessages reads n messages from ch with a timeout.
// It returns whatever was collected, failing the test only if zero messages
// arrive and the timeout is reached.
func collectMessages(t *testing.T, ch <-chan *Message, n int, timeout time.Duration) []*Message {
	t.Helper()

	var received []*Message
	deadline := time.After(timeout)
	for len(received) < n {
		select {
		case msg, ok := <-ch:
			if !ok {
				return received
			}
			received = append(received, msg)
		case <-deadline:
			t.Fatalf("timed out after %v waiting for messages: got %d, wanted %d",
				timeout, len(received), n)
		}
	}
	return received
}
