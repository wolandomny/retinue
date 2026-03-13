package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// These variables will be set via ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// newVersionCmd returns a command that displays version information.
func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "retinue %s (%s) built %s\n", version, commit, date)
		},
	}

	return cmd
}