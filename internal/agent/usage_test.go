package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseUsageFromLog_WithUsage(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")

	content := `{"type":"message_start","message":{}}
{"type":"content_block_start"}
{"type":"result","result":"done","total_cost_usd":0.15,"usage":{"input_tokens":50000,"output_tokens":10000}}
`
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	usage, err := ParseUsageFromLog(logFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.InputTokens != 50000 {
		t.Errorf("InputTokens = %d, want 50000", usage.InputTokens)
	}
	if usage.OutputTokens != 10000 {
		t.Errorf("OutputTokens = %d, want 10000", usage.OutputTokens)
	}
	if usage.TotalCostUSD != 0.15 {
		t.Errorf("TotalCostUSD = %f, want 0.15", usage.TotalCostUSD)
	}
}

func TestParseUsageFromLog_NoUsage(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")

	content := `{"type":"message_start","message":{}}
{"type":"result","result":"done"}
`
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	usage, err := ParseUsageFromLog(logFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", usage.InputTokens)
	}
}

func TestParseUsageFromLog_MissingFile(t *testing.T) {
	_, err := ParseUsageFromLog("/tmp/nonexistent-usage-test.log")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestUsageSummary_String(t *testing.T) {
	tests := []struct {
		name    string
		summary UsageSummary
		want    string
	}{
		{"empty", UsageSummary{}, ""},
		{"tokens only", UsageSummary{InputTokens: 50000, OutputTokens: 10000}, "50k in / 10k out"},
		{"with cost", UsageSummary{InputTokens: 50000, OutputTokens: 10000, TotalCostUSD: 0.15}, "50k in / 10k out ($0.15)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.summary.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
