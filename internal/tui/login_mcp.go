package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/extension"

	adktool "google.golang.org/adk/v2/tool"
)

// loginMCPResultMsg is sent when a /login-mcp OAuth flow completes.
type loginMCPResultMsg struct {
	name string
	err  error
}

// handleLoginMCPCommand re-runs the OAuth login for a configured MCP server.
//   - /login-mcp           — list configured MCP servers and their auth status
//   - /login-mcp <name>    — discard the cached token and run the browser flow
//
// The flow is gated on the agent being idle, matching /model: swapping a live
// toolset into the runner mid-turn would orphan an in-flight tool call.
func (m *model) handleLoginMCPCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		return m.loginMCPShowStatus()
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return m.loginMCPShowStatus()
	}

	// Find the configured server.
	var srv *extension.MCPServerConfig
	for i := range m.cfg.MCPServers {
		if m.cfg.MCPServers[i].Name == name {
			srv = &m.cfg.MCPServers[i]
			break
		}
	}
	if srv == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Unknown MCP server: `%s`. Run `/login-mcp` to list configured servers.", name),
		})
		return m, nil
	}
	if srv.URL == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("MCP server `%s` is a local (stdio) server; there is no OAuth login to re-run.", name),
		})
		return m, nil
	}
	if m.running {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Cannot re-run MCP login while a response is running. Wait for it to finish or cancel it first.",
		})
		return m, nil
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Re-running OAuth login for MCP server `%s`. A browser window will open for authorization...", name),
	})

	srvCopy := *srv
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		ts, err := extension.ReauthorizeMCP(ctx, srvCopy)
		if err != nil {
			return loginMCPResultMsg{name: name, err: err}
		}
		// Swap the rebuilt toolset into the live agent so the next turn uses it.
		if m.cfg.Agent != nil {
			if err := m.cfg.Agent.RebuildWithToolsets(replaceToolset(m.cfg.MCPToolsets, name, ts)); err != nil {
				return loginMCPResultMsg{name: name, err: fmt.Errorf("rebuilding agent: %w", err)}
			}
		}
		m.cfg.MCPToolsets = replaceToolset(m.cfg.MCPToolsets, name, ts)
		return loginMCPResultMsg{name: name}
	}
}

// replaceToolset returns a copy of toolsets with the toolset named name replaced
// by replacement, or appends replacement if no such toolset exists.
func replaceToolset(toolsets []adktool.Toolset, name string, replacement adktool.Toolset) []adktool.Toolset {
	out := make([]adktool.Toolset, 0, len(toolsets)+1)
	replaced := false
	for _, ts := range toolsets {
		if ts.Name() == name {
			out = append(out, replacement)
			replaced = true
			continue
		}
		out = append(out, ts)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

// handleLoginMCPResult processes the async /login-mcp result.
func (m *model) handleLoginMCPResult(msg loginMCPResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("MCP login for `%s` failed: %v", msg.name, msg.err),
		})
		return m, nil
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("MCP server `%s` authorized. The new connection is active for the next turn.", msg.name),
	})
	return m, nil
}

// loginMCPShowStatus lists configured MCP servers and their auth status.
func (m *model) loginMCPShowStatus() (tea.Model, tea.Cmd) {
	if len(m.cfg.MCPServers) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No MCP servers configured. Add servers under `mcp.servers` in `~/.pi-go/config.json`.",
		})
		return m, nil
	}

	statuses := extension.ToolsetStatuses(m.cfg.MCPToolsets)
	statusByName := make(map[string]extension.MCPServerStatus, len(statuses))
	for _, s := range statuses {
		statusByName[s.Name] = s
	}

	var b strings.Builder
	b.WriteString("**MCP Servers** — run `/login-mcp <name>` to re-run OAuth login\n\n")
	b.WriteString("| Server | URL | Status |\n")
	b.WriteString("|--------|-----|--------|\n")
	for _, srv := range m.cfg.MCPServers {
		status := "—"
		if st, ok := statusByName[srv.Name]; ok {
			status = st.Status
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", srv.Name, maskServerURL(srv.URL), status)
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: b.String(),
	})
	return m, nil
}
