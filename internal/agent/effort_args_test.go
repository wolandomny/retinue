package agent

import (
	"reflect"
	"testing"
)

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestApplyEffortHigh(t *testing.T) {
	args := applyEffort(nil, "high")
	effortIdx := indexOf(args, "--effort")
	if effortIdx == -1 {
		t.Fatalf("expected args to contain --effort, got %v", args)
	}
	if effortIdx+1 >= len(args) || args[effortIdx+1] != "high" {
		t.Fatalf("expected --effort to be followed by high, got %v", args)
	}
}

func TestApplyEffortUltracode(t *testing.T) {
	args := applyEffort(nil, "ultracode")
	if !contains(args, "--settings") {
		t.Fatalf("expected args to contain --settings, got %v", args)
	}
	if !contains(args, `{"ultracode": true}`) {
		t.Fatalf("expected args to contain ultracode settings, got %v", args)
	}
	if contains(args, "--effort") {
		t.Fatalf("expected args to NOT contain --effort, got %v", args)
	}
}

func TestApplyEffortEmpty(t *testing.T) {
	input := []string{"--print", "--verbose"}
	got := applyEffort(input, "")
	if !reflect.DeepEqual(got, input) {
		t.Fatalf("expected input unchanged, got %v", got)
	}

	if applyEffort(nil, "") != nil {
		t.Fatalf("expected nil input to return nil")
	}
}
