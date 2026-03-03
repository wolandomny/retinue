package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/task"
)

const maxDescriptionLen = 60

// newStatusCmd returns a command that displays the current status of
// all tasks in a tabular format.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of all tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := task.NewFileStore(ws.TasksPath())
			tasks, err := store.Load()
			if err != nil {
				return err
			}

			if len(tasks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tasks found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tREPO\tTOKENS\tDESCRIPTION")
			fmt.Fprintln(w, "--\t------\t----\t------\t-----------")

			for _, t := range tasks {
				desc := t.Description
				if len(desc) > maxDescriptionLen {
					desc = desc[:maxDescriptionLen-3] + "..."
				}
				tokens := formatTokens(t.Meta)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Repo, tokens, desc)
			}

			if err := w.Flush(); err != nil {
				return err
			}

			// Aggregate cost totals across all tasks.
			var totalIn, totalOut int
			var totalCost float64
			for _, t := range tasks {
				if t.Meta == nil {
					continue
				}
				totalIn += atoi(t.Meta["input_tokens"])
				totalOut += atoi(t.Meta["output_tokens"])
				totalCost += parseFloat(t.Meta["cost_usd"])
			}

			if totalIn > 0 || totalOut > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "\nTotal: %dk input / %dk output",
					totalIn/1000, totalOut/1000)
				if totalCost > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), " ($%.2f)", totalCost)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}

			return nil
		},
	}

	return cmd
}

// formatTokens returns a compact token usage string from task metadata.
func formatTokens(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	in := meta["input_tokens"]
	out := meta["output_tokens"]
	cost := meta["cost_usd"]
	if in == "" && out == "" {
		return ""
	}
	// Convert to k for readability.
	inK := atoi(in) / 1000
	outK := atoi(out) / 1000
	if cost != "" {
		return fmt.Sprintf("%dk/%dk ($%s)", inK, outK, cost)
	}
	return fmt.Sprintf("%dk/%dk", inK, outK)
}

// parseFloat parses a string as a float64, returning 0 on failure.
func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
