package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// helper: send one or more JSON-RPC lines and collect all responses.
func exchange(t *testing.T, srv *Server, lines ...string) []Response {
	t.Helper()
	input := strings.Join(lines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	ctx := context.Background()
	if err := srv.Run(ctx, in, &out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var responses []Response
	dec := json.NewDecoder(&out)
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			break
		}
		responses = append(responses, resp)
	}
	return responses
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestInitializeHandshake(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")

	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Re-marshal the result to inspect it.
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if v, ok := result["protocolVersion"]; !ok || v != "2024-11-05" {
		t.Errorf("unexpected protocolVersion: %v", v)
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("missing serverInfo")
	}
	if serverInfo["name"] != "test-server" {
		t.Errorf("unexpected server name: %v", serverInfo["name"])
	}
	if serverInfo["version"] != "1.0.0" {
		t.Errorf("unexpected server version: %v", serverInfo["version"])
	}
}

func TestToolsList(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")
	srv.AddTool(ToolDef{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"message": {Type: "string", Description: "the message"},
			},
			Required: []string{"message"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		return fmt.Sprintf("%v", args["message"]), nil
	})

	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "echo" {
		t.Errorf("unexpected tool name: %s", result.Tools[0].Name)
	}
	if result.Tools[0].Description != "echoes input" {
		t.Errorf("unexpected description: %s", result.Tools[0].Description)
	}
}

func TestToolsCallDispatch(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")
	srv.AddTool(ToolDef{
		Name:        "greet",
		Description: "greets a person",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"name": {Type: "string", Description: "person name"},
			},
			Required: []string{"name"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		return fmt.Sprintf("hello, %s!", args["name"]), nil
	})

	params := mustMarshal(t, map[string]any{
		"name":      "greet",
		"arguments": map[string]any{"name": "world"},
	})
	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.IsError {
		t.Errorf("expected isError=false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "hello, world!" {
		t.Errorf("unexpected text: %s", result.Content[0].Text)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")

	params := mustMarshal(t, map[string]any{
		"name":      "nonexistent",
		"arguments": map[string]any{},
	})
	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	// Unknown tool returns a ToolResult with isError=true, not a JSON-RPC error.
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}

	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result.IsError {
		t.Errorf("expected isError=true for unknown tool")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected content in error result")
	}
	if !strings.Contains(result.Content[0].Text, "nonexistent") {
		t.Errorf("error message should mention tool name, got: %s", result.Content[0].Text)
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")

	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "bogus/method",
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error == nil {
		t.Fatalf("expected error response")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")

	// A notification has no "id" field. We send it as raw JSON to
	// ensure the id key is truly absent rather than null.
	notification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`

	responses := exchange(t, srv, notification)
	if len(responses) != 0 {
		t.Errorf("expected 0 responses for notification, got %d", len(responses))
	}
}

func TestContextCancellation(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")

	// Use a pipe so the reader blocks indefinitely.
	pr, pw := io.Pipe()
	defer pw.Close()

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, pr, &out)
	}()

	// Give the goroutine a moment to start reading.
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestToolHandlerError(t *testing.T) {
	srv := NewServer("test-server", "1.0.0")
	srv.AddTool(ToolDef{
		Name:        "fail",
		Description: "always fails",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
		},
	}, func(_ context.Context, _ map[string]any) (string, error) {
		return "", fmt.Errorf("something went wrong")
	})

	params := mustMarshal(t, map[string]any{
		"name":      "fail",
		"arguments": map[string]any{},
	})
	req := mustMarshal(t, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	})

	responses := exchange(t, srv, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result.IsError {
		t.Errorf("expected isError=true for handler error")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "something went wrong") {
		t.Errorf("expected error message in content")
	}
}
