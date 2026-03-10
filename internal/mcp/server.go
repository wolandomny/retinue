package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// ToolHandler is a function that executes a tool call. It receives the
// parsed arguments and returns the text result or an error.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

// Server is a minimal MCP server that serves tools over JSON-RPC 2.0 on stdio.
type Server struct {
	name     string
	version  string
	tools    []ToolDef
	handlers map[string]ToolHandler
}

// NewServer creates a new MCP server with the given name and version.
func NewServer(name, version string) *Server {
	return &Server{
		name:     name,
		version:  version,
		handlers: make(map[string]ToolHandler),
	}
}

// AddTool registers a tool definition and its handler with the server.
func (s *Server) AddTool(def ToolDef, handler ToolHandler) {
	s.tools = append(s.tools, def)
	s.handlers[def.Name] = handler
}

// Run reads JSON-RPC requests from in and writes responses to out.
// It blocks until the input is exhausted or the context is cancelled.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	logger := log.New(os.Stderr, "mcp: ", log.LstdFlags)
	scanner := bufio.NewScanner(in)
	encoder := json.NewEncoder(out)

	// Use a channel to multiplex between scanner lines and context cancellation.
	type lineResult struct {
		text string
		err  error
	}
	lines := make(chan lineResult)

	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- lineResult{text: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- lineResult{err: err}:
			case <-ctx.Done():
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				// Input exhausted.
				return nil
			}
			if line.err != nil {
				return fmt.Errorf("reading input: %w", line.err)
			}

			var req Request
			if err := json.Unmarshal([]byte(line.text), &req); err != nil {
				logger.Printf("failed to decode request: %v", err)
				continue
			}

			// Notifications have no id — do not send a response.
			if req.ID == nil {
				s.handleNotification(logger, &req)
				continue
			}

			resp := s.dispatch(ctx, &req)
			if err := encoder.Encode(resp); err != nil {
				logger.Printf("failed to encode response: %v", err)
			}
		}
	}
}

// handleNotification processes a notification (no response expected).
func (s *Server) handleNotification(logger *log.Logger, req *Request) {
	switch req.Method {
	case "notifications/initialized":
		// Acknowledged — nothing to do.
	default:
		logger.Printf("unknown notification: %s", req.Method)
	}
}

// dispatch routes a request to the appropriate handler and returns a response.
func (s *Server) dispatch(ctx context.Context, req *Request) Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &Error{
				Code:    -32601,
				Message: "method not found",
			},
		}
	}
}

func (s *Server) handleInitialize(req *Request) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    s.name,
				"version": s.version,
			},
		},
	}
}

func (s *Server) handleToolsList(req *Request) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": s.tools,
		},
	}
}

func (s *Server) handleToolsCall(ctx context.Context, req *Request) Response {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &Error{
				Code:    -32602,
				Message: fmt.Sprintf("invalid params: %v", err),
			},
		}
	}

	handler, ok := s.handlers[params.Name]
	if !ok {
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", params.Name)}},
				IsError: true,
			},
		}
	}

	result, err := handler(ctx, params.Arguments)
	if err != nil {
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolResult{
				Content: []ContentBlock{{Type: "text", Text: err.Error()}},
				IsError: true,
			},
		}
	}

	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolResult{
			Content: []ContentBlock{{Type: "text", Text: result}},
		},
	}
}
