package server

import (
	"encoding/json"
	"os"
	"strings"

	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/extension"
)

// kagentConfigEnv carries the agent configuration the kagent compiler injects
// into every harness workload. It is the only channel through which a BYO
// runtime learns which MCP servers its AgentTemplate declared: the harness
// itself only declares an image, and the template's tools are resolved by the
// controller into this blob at runtime, not into the Harness spec.
//
// Verified against a live kagent v1alpha3 controller: the compiled config takes
// the shape
//
//	{"runtime":{"control_plane_url":...},"agent":{"http_tools":[{"params":{"url":...,
//	 "headers":{"Authorization":"KAGENT_ENV[KAGENT_CREDENTIAL_...]"}}}, ...]}}
//
// so the tools arrive as `agent.http_tools`, one entry per MCP server, and the
// credential is a placeholder that Substrate's egress gateway substitutes on the
// wire. The real secret never enters the sandbox.
const kagentConfigEnv = "KAGENT_CONFIG_JSON"

// kagentHTTPTool is one MCP server from the compiled agent config.
type kagentHTTPTool struct {
	Params struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"params"`
	// Tools narrows the server to a subset of its tools. Empty means every
	// tool the server exposes, which is what config-driven MCP servers mean
	// when they carry no filter.
	Tools []string `json:"tools"`
}

// kagentCompiledConfig is the subset of KAGENT_CONFIG_JSON this package reads.
// Unknown fields are ignored on purpose: the compiler owns this schema and adds
// to it, and a harness must not fail to start because a newer controller sent a
// key this build does not model.
type kagentCompiledConfig struct {
	Agent struct {
		HTTPTools []kagentHTTPTool `json:"http_tools"`
	} `json:"agent"`
}

// kagentMCPToolsets returns an ADK toolset per MCP server the kagent compiler
// configured for this harness, or nil when none are configured.
//
// This is the kagent-specific counterpart to buildMCPToolsetsFromCfg: config
// files are per-user and the harness image ships none, so without reading the
// compiled config a kagent harness has no MCP tools at all — the tools its
// AgentTemplate declares are resolved by the controller and never reach the
// process any other way.
func kagentMCPToolsetsFromEnv(getenv func(string) string) []adktool.Toolset {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(getenv(kagentConfigEnv))
	if raw == "" {
		return nil
	}
	var cfg kagentCompiledConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		// A malformed blob is the controller's problem, not a reason to refuse
		// to serve: the agent still has its built-in tools and the turn still
		// runs, it just runs without MCP.
		return nil
	}
	servers := make([]extension.MCPServerConfig, 0, len(cfg.Agent.HTTPTools))
	for _, t := range cfg.Agent.HTTPTools {
		url := strings.TrimSpace(t.Params.URL)
		if url == "" {
			continue
		}
		servers = append(servers, extension.MCPServerConfig{
			// The URL is the only stable identity a compiled tool carries; the
			// compiler does not emit a server name.
			Name:    kagentServerName(url),
			URL:     url,
			Headers: t.Params.Headers,
		})
	}
	if len(servers) == 0 {
		return nil
	}
	ts, _ := extension.BuildMCPToolsets(servers)
	return ts
}

// kagentServerName derives a stable, readable server name from an MCP URL, so
// the tools it contributes are grouped under a name a user can recognize.
//
// The compiler emits no name, and a full URL makes an unusable tool namespace,
// so the last meaningful path segment is used: ".../mcp/x/pull_requests/readonly"
// becomes "pull_requests", and a bare ".../mcp" falls back to the host.
func kagentServerName(rawURL string) string {
	trimmed := strings.TrimSuffix(rawURL, "/")
	parts := strings.Split(trimmed, "/")
	// Walk back past the trailing marker and any "readonly"-style scope so the
	// name describes the capability rather than the access level.
	for i := len(parts) - 1; i >= 0; i-- {
		seg := parts[i]
		switch seg {
		case "", "mcp", "readonly", "readwrite", "read":
			continue
		}
		return seg
	}
	return trimmed
}
