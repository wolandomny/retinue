package agent

import "testing"

func TestBuildClaudeArgs_WithDisallowedTools(t *testing.T) {
	args := buildClaudeArgs(RunOpts{
		Prompt:          "do work",
		DisallowedTools: "AskUserQuestion",
	})

	// The deny flag must use the single-token "=" form. The two-token form
	// ("--disallowed-tools" "AskUserQuestion") is variadic/greedy in the claude
	// CLI and swallows the positional prompt, so it must NOT be emitted.
	if !contains(args, "--disallowed-tools=AskUserQuestion") {
		t.Errorf("expected --disallowed-tools=AskUserQuestion, got args: %v", args)
	}
	for _, a := range args {
		if a == "--disallowed-tools" {
			t.Errorf("bare --disallowed-tools token swallows the prompt; use the = form, got args: %v", args)
		}
	}
	// The prompt must remain the final argument.
	if len(args) == 0 || args[len(args)-1] != "do work" {
		t.Errorf("expected prompt to be the last arg, got args: %v", args)
	}
}

func TestBuildClaudeArgs_NoDisallowedTools(t *testing.T) {
	args := buildClaudeArgs(RunOpts{
		Prompt: "do work",
	})
	for _, a := range args {
		if a == "--disallowed-tools" || a == "--disallowed-tools=" {
			t.Errorf("--disallowed-tools should not be present when DisallowedTools is empty, got args: %v", args)
		}
	}
}
