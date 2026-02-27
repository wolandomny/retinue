package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Create a new retinue apartment",
		Long:  "Create a new retinue apartment. If no path is given, initializes in the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) == 1 {
				path = args[0]
			} else {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getting working directory: %w", err)
				}
				path = cwd
			}

			absPath, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolving path: %w", err)
			}

			cfg := workspace.Config{
				Name:        filepath.Base(absPath),
				ReviewModel: "claude-opus-4-6",
			}

			ws, err := workspace.Create(absPath, cfg)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Apartment created at %s\n", ws.Path)
			return nil
		},
	}

	return cmd
}
