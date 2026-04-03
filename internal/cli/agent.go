package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/bus"
	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/shell"
	"github.com/wolandomny/retinue/internal/standing"
	"github.com/wolandomny/retinue/internal/workspace"
)

const agentWindowPrefix = "agent-"

// busWatcherWindow is the tmux window name for the bus watcher daemon.
// The bus watcher is auto-started when the first agent starts and auto-stopped
// when the last agent stops.
const busWatcherWindow = "bus-watcher"

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

			// Auto-start bus watcher if one isn't already running.
			if shouldStartBusWatcher(ctx, mgr) {
				bwCmd := busWatcherCommand(ws)
				if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, ws.Path, bwCmd); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not start bus watcher: %v\n", err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Bus watcher started.\n")
				}
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

			// Give Claude CLI time to initialize and display the welcome screen.
			time.Sleep(3 * time.Second)

			// Send a kickoff message so the agent begins autonomous work
			// instead of sitting at the welcome screen waiting for input.
			if err := sendKickoff(ctx, mgr, agent.Name, windowName); err != nil {
				// Non-fatal: the window is created, the kickoff just didn't inject.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not send kickoff message: %v\n", err)
			}

			// Write session marker so the bus watcher can find this agent's session file.
			writeAgentSessionMarker(ws.Path, agentID)

			// Write a system message to the bus announcing the agent joined.
			b := bus.New(ws.BusPath())
			if err := b.Append(bus.NewMessage("system", bus.TypeSystem, agent.Name+" has joined")); err != nil {
				// Non-fatal: log but don't fail the start.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to write bus message: %v\n", err)
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

			// Write a system message to the bus before killing the window.
			b := bus.New(ws.BusPath())
			if err := b.Append(bus.NewMessage("system", bus.TypeSystem, agent.Name+" has left")); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to write bus message: %v\n", err)
			}

			if err := mgr.KillWindow(ctx, session.ApartmentSession, windowName); err != nil {
				return fmt.Errorf("stopping agent: %w", err)
			}

			// Clean up the session marker file.
			removeAgentSessionMarker(ws.Path, agentID)

			// Auto-stop bus watcher if no other agents are still running.
			if shouldStopBusWatcher(ctx, mgr, store, agentID) {
				if err := mgr.KillWindow(ctx, session.ApartmentSession, busWatcherWindow); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not stop bus watcher: %v\n", err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Bus watcher stopped (no agents running).\n")
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Agent %q (%s) stopped.\n", agentID, agent.Name)
			return nil
		},
	}

	return cmd
}

// buildAgentSystemPrompt constructs a system prompt for a standing agent
// that includes its identity, mandate, workspace context, and bus awareness.
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
	fmt.Fprintf(&b, "agents in the Retinue apartment at: %s\n\n", ws.Path)

	fmt.Fprintf(&b, "## Group Chat\n")
	fmt.Fprintf(&b, "You are part of a group chat with other agents and the user. Messages from\n")
	fmt.Fprintf(&b, "others will be injected into your session in this format:\n\n")
	fmt.Fprintf(&b, "    [AgentName] message text\n")
	fmt.Fprintf(&b, "    [User] message text\n\n")
	fmt.Fprintf(&b, "When you see these messages, you can respond naturally. Your responses will\n")
	fmt.Fprintf(&b, "be relayed to the group. Key guidelines:\n")
	fmt.Fprintf(&b, "- Only respond when the message is relevant to your role\n")
	fmt.Fprintf(&b, "- If another agent is handling something, don't duplicate their work\n")
	fmt.Fprintf(&b, "- Declare your intent before taking action: \"I'm going to fix the CI failure\"\n")
	fmt.Fprintf(&b, "- Report results after acting: \"Fixed — PR #42 opened\"\n")

	// Include recent bus history if available.
	msgBus := bus.New(ws.BusPath())
	messages, err := msgBus.ReadRecent(20)
	if err == nil && len(messages) > 0 {
		fmt.Fprintf(&b, "\n## Recent Group Chat History\n")
		for _, msg := range messages {
			fmt.Fprintln(&b, bus.FormatMessage(msg))
		}
	}

	return b.String()
}

// agentSessionMarkerName returns the marker file name for a standing agent.
func agentSessionMarkerName(agentID string) string {
	return fmt.Sprintf(".agent-%s-session", agentID)
}

// writeAgentSessionMarker finds the newest .jsonl session file in the Claude
// projects directory and writes its path to the agent's marker file. This is
// called after creating a new agent tmux window so the bus watcher knows which
// session file belongs to this agent.
func writeAgentSessionMarker(aptPath, agentID string) {
	projDir := session.ClaudeProjectDir(aptPath)
	newest := session.NewestJSONLFile(projDir)
	if newest == "" {
		return
	}
	markerPath := filepath.Join(aptPath, agentSessionMarkerName(agentID))
	_ = os.WriteFile(markerPath, []byte(newest), 0o644)
}

// removeAgentSessionMarker removes the session marker file for an agent.
func removeAgentSessionMarker(aptPath, agentID string) {
	markerPath := filepath.Join(aptPath, agentSessionMarkerName(agentID))
	os.Remove(markerPath)
}

// shouldStartBusWatcher returns true if the bus-watcher window is not already
// running in the apartment session. It accepts session.Manager so it can be
// tested with session.FakeManager.
func shouldStartBusWatcher(ctx context.Context, mgr session.Manager) bool {
	has, err := mgr.HasWindow(ctx, session.ApartmentSession, busWatcherWindow)
	if err != nil {
		return false
	}
	return !has
}

// shouldStopBusWatcher returns true if no agent windows (other than the agent
// identified by stoppedAgentID) are still running. It checks every enabled agent
// in the store. It accepts session.Manager so it can be tested with FakeManager.
func shouldStopBusWatcher(ctx context.Context, mgr session.Manager, store *standing.FileStore, stoppedAgentID string) bool {
	agents, err := store.Load()
	if err != nil {
		return false
	}
	for _, a := range agents {
		if a.ID == stoppedAgentID {
			continue
		}
		if !a.Enabled {
			continue
		}
		running, err := mgr.HasWindow(ctx, session.ApartmentSession, agentWindowName(a.ID))
		if err == nil && running {
			return false
		}
	}
	return true
}

// busWatcherCommand returns the shell command used to run the bus watcher
// in a tmux window.
func busWatcherCommand(ws *workspace.Workspace) string {
	bin, err := os.Executable()
	if err != nil {
		bin = "retinue"
	}
	return bin + " bus serve --workspace " + shell.Quote(ws.Path)
}

// sendKickoff sends an initial message to a newly created agent window so that
// the Claude CLI begins autonomous work instead of waiting at the welcome screen.
// Errors are non-fatal and returned for the caller to log.
func sendKickoff(ctx context.Context, mgr *session.TmuxManager, agentName, windowName string) error {
	kickoff := fmt.Sprintf("You are %s. Begin your work now according to your mandate.", agentName)
	escaped := shell.EscapeTmux(kickoff)
	sendTarget := session.ApartmentSession + ":" + windowName
	sendArgs := mgr.TmuxArgs("send-keys", "-t", sendTarget, "--", escaped, "Enter")
	cmd := exec.CommandContext(ctx, "tmux", sendArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}
