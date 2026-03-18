package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var workspaceFlag string

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "retinue",
		Short:        "Multi-agent orchestration CLI for Claude Code",
		Long:         "Retinue orchestrates multiple Claude Code agents to work on tasks in parallel across repositories.",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVarP(&workspaceFlag, "workspace", "w", "", "path to retinue workspace")

	cmd.AddCommand(
		newInitCmd(),
		newAddCmd(),
		newAttachCmd(),
		newDispatchCmd(),
		newLsCmd(),
		newMCPCmd(),
		newMergeCmd(),
		newPhoneCmd(),
		newResetCmd(),
		newRunCmd(),
		newStatusCmd(),
		newTelegramCmd(),
		newVersionCmd(),
		newWolandCmd(),
	)

	// Add config reference to help command.
	cmd.InitDefaultHelpCmd()
	helpCmd, _, _ := cmd.Find([]string{"help"})
	if helpCmd != nil && helpCmd.Name() == "help" {
		helpCmd.AddCommand(newHelpConfigCmd())
	}

	return cmd
}

// Execute runs the root command.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
