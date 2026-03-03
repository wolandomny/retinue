package cli

import (
	"strings"
	"testing"
)

func TestReviewVerdict_ParseApproved(t *testing.T) {
	output := "APPROVED\nLooks good, all changes match the task."
	approved := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(output)), "APPROVED")
	if !approved {
		t.Error("expected approved")
	}
}

func TestReviewVerdict_ParseRejected(t *testing.T) {
	output := "REJECTED\nMissing error handling in the new endpoint."
	approved := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(output)), "APPROVED")
	if approved {
		t.Error("expected rejected")
	}
}
