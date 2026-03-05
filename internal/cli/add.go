package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/workspace"
)

const githubHost = "github.com/"

// newAddCmd returns the parent command for adding resources to an apartment.
func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add resources to the apartment",
	}

	cmd.AddCommand(newAddRepoCmd())

	return cmd
}

// newAddRepoCmd returns a command that clones a repository and registers
// it in the workspace configuration.
func newAddRepoCmd() *cobra.Command {
	var nameFlag string

	cmd := &cobra.Command{
		Use:   "repo <host/owner/repo>",
		Short: "Clone a repo into the apartment and register it",
		Example: "  retinue add repo github.com/org/api",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := strings.TrimSuffix(args[0], ".git")
			gitURL := "https://" + repoPath + ".git"

			name := nameFlag
			if name == "" {
				name = repoNameFromURL(repoPath)
			}
			if name == "" {
				return fmt.Errorf("could not derive repo name from URL; use --name to specify one")
			}

			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			dest := filepath.Join(ws.Path, workspace.ReposDir, name)

			var cloneCmd *exec.Cmd
			if strings.HasPrefix(repoPath, githubHost) && ws.Config.GithubAccount != "" {
				ownerRepo := strings.TrimPrefix(repoPath, githubHost)
				cloneCmd = exec.Command("gh", "repo", "clone", ownerRepo, dest)
			} else {
				cloneCmd = exec.Command("git", "clone", gitURL, dest)
			}
			cloneCmd.Stdout = cmd.OutOrStdout()
			cloneCmd.Stderr = cmd.ErrOrStderr()
			if err := cloneCmd.Run(); err != nil {
				return fmt.Errorf("cloning repo: %w", err)
			}

			if ws.Config.Repos == nil {
				ws.Config.Repos = make(map[string]workspace.RepoConfig)
			}
			ws.Config.Repos[name] = workspace.RepoConfig{Path: filepath.Join(workspace.ReposDir, name)}

			if err := ws.SaveConfig(); err != nil {
				return fmt.Errorf("updating config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added %q to apartment\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "override the derived repo name")

	return cmd
}

// repoNameFromURL extracts a repo name from a repo path.
// e.g. "github.com/org/api" -> "api"
func repoNameFromURL(repoPath string) string {
	if i := strings.LastIndex(repoPath, "/"); i >= 0 {
		return repoPath[i+1:]
	}
	return repoPath
}
