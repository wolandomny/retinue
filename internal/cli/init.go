package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newInitCmd() *cobra.Command {
	var (
		name  string
		repos []string
	)

	cmd := &cobra.Command{
		Use:   "init <path>",
		Short: "Create a new retinue workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			repoMap := make(map[string]string)
			for _, r := range repos {
				parts := strings.SplitN(r, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid repo format %q, expected name=path", r)
				}
				repoMap[parts[0]] = parts[1]
			}

			cfg := workspace.Config{
				Name:  name,
				Repos: repoMap,
			}

			ws, err := workspace.Create(path, cfg)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q created at %s\n", ws.Config.Name, ws.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "workspace name (required)")
	cmd.Flags().StringArrayVar(&repos, "repo", nil, "repo mapping as name=path (repeatable)")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}
