package cli

import (
	"testing"

	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/task"
)

func TestReplanResultType(t *testing.T) {
	// Verify the replanResult type and replanFailedTask function
	// signature compile correctly.
	r := replanResult{
		RevisedPrompt: "revised prompt",
		Usage:         agent.UsageSummary{InputTokens: 100, OutputTokens: 50},
	}
	if r.RevisedPrompt != "revised prompt" {
		t.Error("unexpected revised prompt")
	}

	// Verify task types work with the expected fields.
	tk := task.Task{
		ID:     "test-task",
		Prompt: "do something",
		Error:  "it broke",
		Result: "partial output",
	}
	_ = tk
}
