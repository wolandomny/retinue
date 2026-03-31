package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/shell"
	"github.com/wolandomny/retinue/internal/standing"
	"github.com/wolandomny/retinue/internal/workspace"
)

const agentWindowPrefix = "agent-"

// agentWindowName returns the tmux window name for a standing agent.
func agentWindowName(id string) string {
	return agentWindowPrefix + id
}

// newAgentCmd returns the parent command for managing standing agents.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage standing agents",
	}

	cmd.AddCommand(
		newAgentListCmd(),
		newAgentStartCmd(),
		newAgentStopCmd(),
	)

	return cmd
}

// newAgentListCmd returns a command that lists all defined standing agents
// and their current running status.
func newAgentListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all standing agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := standing.NewFileStore(ws.AgentsPath())
			agents, err := store.Load()
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No agents defined.")
				return nil
			}

			if len(agents) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No agents defined.")
				return nil
			}

			socket := "retinue-" + ws.Config.Name
			mgr := session.NewTmuxManager(socket)
			ctx := context.Background()

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tROLE\tSTATUS")
			fmt.Fprintln(w, "--\t----\t----\t------")

			for _, a := range agents {
				status := "stopped"
				if !a.Enabled {
					status = "disabled"
				} else {
					running, err := mgr.HasWindow(ctx, session.ApartmentSession, agentWindowName(a.ID))
					if err == nil && running {
						status = "running"
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Name, a.Role, status)
			}

			return w.Flush()
		},
	}

	return cmd
}

// newAgentStartCmd returns a command that starts a standing agent by
// creating a Claude session in a tmux window.
func newAgentStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <id>",
		Short: "Start a standing agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]

			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := standing.NewFileStore(ws.AgentsPath())
			agent, err := store.Get(agentID)
			if err != nil {
				return fmt.Errorf("agent %q not found in agents.yaml", agentID)
			}

			if !agent.Enabled {
				return fmt.Errorf("agent %q is disabled; set enabled: true in agents.yaml to start it", agentID)
			}

			socket := "retinue-" + ws.Config.Name
			mgr := session.NewTmuxManager(socket)
			ctx := context.Background()
			windowName := agentWindowName(agentID)

			running, err := mgr.HasWindow(ctx, session.ApartmentSession, windowName)
			if err != nil {
				return fmt.Errorf("checking tmux window: %w", err)
			}
			if running {
				return fmt.Errorf("agent %q is already running", agentID)
			}

			systemPrompt := buildAgentSystemPrompt(ws, agent)

			model := agent.Model
			if model == "" {
				model = ws.Config.Model
			}

			claudeArgs := []string{
				"--dangerously-skip-permissions",
				"--system-prompt", systemPrompt,
			}
			if model != "" {
				claudeArgs = append(claudeArgs, "--model", model)
			}

			claudeCmd := "claude " + shell.Join(claudeArgs)

			if err := mgr.CreateWindow(ctx, session.ApartmentSession, windowName, ws.Path, claudeCmd); err != nil {
				return fmt.Errorf("creating tmux window: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Agent %q (%s) started.\n", agentID, agent.Name)
			return nil
		},
	}

	return cmd
}

// newAgentStopCmd returns a command that stops a running standing agent.
func newAgentStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a running standing agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]

			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			store := standing.NewFileStore(ws.AgentsPath())
			agent, err := store.Get(agentID)
			if err != nil {
				return fmt.Errorf("agent %q not found in agents.yaml", agentID)
			}

			socket := "retinue-" + ws.Config.Name
			mgr := session.NewTmuxManager(socket)
			ctx := context.Background()
			windowName := agentWindowName(agentID)

			running, err := mgr.HasWindow(ctx, session.ApartmentSession, windowName)
			if err != nil {
				return fmt.Errorf("checking tmux window: %w", err)
			}
			if !running {
				return fmt.Errorf("agent %q is not running", agentID)
			}

			if err := mgr.KillWindow(ctx, session.ApartmentSession, windowName); err != nil {
				return fmt.Errorf("stopping agent: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Agent %q (%s) stopped.\n", agentID, agent.Name)
			return nil
		},
	}

	return cmd
}

// buildAgentSystemPrompt constructs a system prompt for a standing agent
// that includes its identity, mandate, and workspace context.
func buildAgentSystemPrompt(ws *workspace.Workspace, agent *standing.Agent) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are %s — a standing agent in the Retinue system.\n\n", agent.Name)
	fmt.Fprintf(&b, "## Role\n%s\n\n", agent.Role)
	fmt.Fprintf(&b, "## Mandate\n%s\n\n", agent.Prompt)

	if len(agent.Repos) > 0 {
		fmt.Fprintf(&b, "## Repositories\nYou have access to the following repositories:\n")
		for _, repoName := range agent.Repos {
			if rc, ok := ws.Config.Repos[repoName]; ok {
				absPath := filepath.Join(ws.Path, rc.Path)
				fmt.Fprintf(&b, "- %s: %s\n", repoName, absPath)
			} else {
				fmt.Fprintf(&b, "- %s: (not found in workspace config)\n", repoName)
			}
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "## Context\n")
	fmt.Fprintf(&b, "You are a standing agent — a long-lived Claude session with a specific mandate. ")
	fmt.Fprintf(&b, "Unlike ephemeral task workers that complete a single task and exit, you persist ")
	fmt.Fprintf(&b, "and continuously fulfill your role. You run as a tmux window alongside other ")
	fmt.Fprintf(&b, "agents in the Retinue apartment at: %s\n", ws.Path)

	return b.String()
}
