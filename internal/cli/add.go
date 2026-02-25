package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/workspace"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <project-name> <git-url>",
		Short: "Clone a repo into the apartment and register it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			gitURL := args[1]

			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			dest := filepath.Join(ws.Path, workspace.ReposDir, name)

			gitCmd := exec.Command("git", "clone", gitURL, dest)
			gitCmd.Stdout = cmd.OutOrStdout()
			gitCmd.Stderr = cmd.ErrOrStderr()
			if err := gitCmd.Run(); err != nil {
				return fmt.Errorf("cloning repo: %w", err)
			}

			if ws.Config.Repos == nil {
				ws.Config.Repos = make(map[string]string)
			}
			ws.Config.Repos[name] = filepath.Join(workspace.ReposDir, name)

			if err := ws.SaveConfig(); err != nil {
				return fmt.Errorf("updating config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added %q to apartment\n", name)
			return nil
		},
	}

	return cmd
}
