package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <path>",
		Short: "Create a new retinue apartment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			cfg := workspace.Config{
				Name: filepath.Base(path),
			}

			ws, err := workspace.Create(path, cfg)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Apartment created at %s\n", ws.Path)
			return nil
		},
	}

	return cmd
}
