package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// UsageSummary holds aggregated token usage from a run.
type UsageSummary struct {
	InputTokens  int
	OutputTokens int
	TotalCostUSD float64
}

// String returns a human-readable summary.
func (u UsageSummary) String() string {
	total := u.InputTokens + u.OutputTokens
	if total == 0 {
		return ""
	}
	if u.TotalCostUSD > 0 {
		return fmt.Sprintf("%dk in / %dk out ($%.2f)",
			u.InputTokens/1000, u.OutputTokens/1000, u.TotalCostUSD)
	}
	return fmt.Sprintf("%dk in / %dk out",
		u.InputTokens/1000, u.OutputTokens/1000)
}

// ParseUsageFromLog reads a stream-json log file and extracts
// the final usage statistics from result events.
func ParseUsageFromLog(logFile string) (UsageSummary, error) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return UsageSummary{}, err
	}

	var summary UsageSummary
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "result" {
			if event.Usage != nil {
				summary.InputTokens = event.Usage.InputTokens
				summary.OutputTokens = event.Usage.OutputTokens
			}
			summary.TotalCostUSD = event.TotalCost
		}
	}

	return summary, nil
}
