package agent

import "testing"

func TestBuildClaudeArgs_WithDisallowedTools(t *testing.T) {
	args := buildClaudeArgs(RunOpts{
		Prompt:          "do work",
		DisallowedTools: "AskUserQuestion",
	})

	// Find --disallowed-tools and verify the next arg.
	found := false
	for i, a := range args {
		if a == "--disallowed-tools" {
			if i+1 >= len(args) || args[i+1] != "AskUserQuestion" {
				t.Errorf("expected --disallowed-tools AskUserQuestion, got args: %v", args)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected --disallowed-tools flag, got args: %v", args)
	}
}

func TestBuildClaudeArgs_NoDisallowedTools(t *testing.T) {
	args := buildClaudeArgs(RunOpts{
		Prompt: "do work",
	})
	for _, a := range args {
		if a == "--disallowed-tools" {
			t.Errorf("--disallowed-tools should not be present when DisallowedTools is empty, got args: %v", args)
		}
	}
}
