package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestBusCmdRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "bus" {
			found = true
			// Check that the "serve" subcommand exists.
			subNames := map[string]bool{}
			for _, s := range sub.Commands() {
				subNames[s.Name()] = true
			}
			if !subNames["serve"] {
				t.Error("bus subcommand 'serve' not found")
			}
			break
		}
	}
	if !found {
		t.Error("'bus' command not found under root")
	}
}

func TestBusCmdHasServeSubcommand(t *testing.T) {
	cmd := newBusCmd()

	subNames := map[string]bool{}
	for _, sub := range cmd.Commands() {
		subNames[sub.Name()] = true
	}

	if !subNames["serve"] {
		t.Error("expected 'serve' subcommand on bus command")
	}
}

func TestBusServeFailsWithoutWorkspace(t *testing.T) {
	// Point workspaceFlag at a nonexistent directory so loadWorkspace() fails.
	oldFlag := workspaceFlag
	workspaceFlag = "/nonexistent/path/that/does/not/exist"
	defer func() { workspaceFlag = oldFlag }()

	cmd := newBusServeCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when workspace does not exist")
	}
}

func TestBusCmdUseAndShort(t *testing.T) {
	cmd := newBusCmd()
	if cmd.Use != "bus" {
		t.Errorf("expected Use='bus', got %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("expected non-empty Short description for bus command")
	}
}

func TestBusServeCmdUseAndShort(t *testing.T) {
	cmd := newBusServeCmd()
	if cmd.Use != "serve" {
		t.Errorf("expected Use='serve', got %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("expected non-empty Short description for bus serve command")
	}
	if !strings.Contains(strings.ToLower(cmd.Long), "bus") {
		t.Errorf("expected Long description to mention bus, got: %q", cmd.Long)
	}
}
