package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/httplog"
)

// newMCPCmd wires up `pi mcp`, which manages the MCP servers an agent can use
// without hand-editing JSON.
//
// The two commands that matter are `add` and `login`, and they are separate on
// purpose. `add` records where a server lives; `login` runs the OAuth
// authorization-code flow now and caches the token. Keeping them apart means a
// server that needs no credentials costs one command, while one that does never
// surprises the user mid-run: without a cached token, pi-go connects without an
// OAuth handler, and the first agent request is where a browser would otherwise
// open — or, in a headless run, where the server is simply skipped.
func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers",
		Long: `Add, list, authorize and remove Model Context Protocol servers.

Servers are stored in a standalone mcp.json, which pi-go shares with other MCP
clients. Without --project the global ~/.pi-go/mcp.json is used, so the server is
available in every project; --project writes .pi-go/mcp.json in the current tree.

A server is either remote (--url, Streamable HTTP) or local (--command, a stdio
subprocess):

    pi mcp add superlogical --url https://www.superlogical.com/rex/community-mcp
    pi mcp login superlogical
    pi mcp add filesystem --command npx --arg -y \
        --arg @modelcontextprotocol/server-filesystem --arg /tmp
    pi mcp list

` + "`login`" + ` is only for remote servers that want a bearer token. It runs the
authorization-code flow in the browser and stores the resulting token under
~/.pi-go/mcp-oauth, where every later session picks it up automatically.`,
	}

	cmd.AddCommand(newMCPAddCmd())
	cmd.AddCommand(newMCPLoginCmd())
	cmd.AddCommand(newMCPListCmd())
	cmd.AddCommand(newMCPRemoveCmd())

	return cmd
}

// mcpAddFlags holds `pi mcp add` flags. They live in a struct rather than in
// package-level vars so a test can build the command twice without one run's
// values leaking into the next.
type mcpAddFlags struct {
	url     string
	command string
	args    []string
	headers []string
	oauth   bool
	project bool
	force   bool
}

func newMCPAddCmd() *cobra.Command {
	f := &mcpAddFlags{}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an MCP server to mcp.json",
		Long: `Add an MCP server to a standalone mcp.json.

Exactly one transport is required: --url for a remote Streamable HTTP server, or
--command for a local stdio subprocess. An existing entry of the same name is
left untouched unless --force is given, so a typo cannot replace a working
server.

Writing to mcp.json rather than to config.json is deliberate — that is the file
other MCP clients manage, so a server added here is visible to them too.`,
		Example: `  pi mcp add superlogical --url https://www.superlogical.com/rex/community-mcp
  pi mcp add superlogical --url $URL --oauth      # require a bearer token
  pi mcp add tavily --url "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"
  pi mcp add fetch --command uvx --arg mcp-server-fetch
  pi mcp add fetch --command uvx --arg mcp-server-fetch --project`,
		Args: cobra.ExactArgs(1),
		// Every failure here is a diagnostic the user needs to read — an
		// existing entry it refused to replace, a malformed header — and the
		// flag list printed under it only buries that. main prints the error.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPAdd(cmd, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.url, "url", "", "remote server URL (Streamable HTTP transport)")
	cmd.Flags().StringVar(&f.command, "command", "", "local server executable (stdio transport)")
	cmd.Flags().StringArrayVar(&f.args, "arg", nil, "argument for --command (repeatable)")
	cmd.Flags().StringArrayVar(&f.headers, "header", nil, "extra HTTP header as key=value (repeatable, --url only)")
	cmd.Flags().BoolVar(&f.oauth, "oauth", false, "run the OAuth authorization-code flow on first connect")
	cmd.Flags().BoolVar(&f.project, "project", false, "write .pi-go/mcp.json in this tree instead of the global file")
	cmd.Flags().BoolVar(&f.force, "force", false, "replace an existing entry of the same name")
	return cmd
}

func runMCPAdd(cmd *cobra.Command, name string, f *mcpAddFlags) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("server name is required")
	}
	if f.url == "" && f.command == "" {
		return errors.New("a transport is required: pass --url for a remote server or --command for a local one")
	}
	if f.url != "" && f.command != "" {
		return errors.New("--url and --command are mutually exclusive: a server is either remote or local")
	}
	if f.command != "" && len(f.headers) > 0 {
		return errors.New("--header applies to --url servers only; a stdio server takes its configuration from --arg")
	}
	if f.url != "" {
		if err := validateMCPURL(f.url); err != nil {
			return err
		}
	}

	headers, err := parseMCPHeaders(f.headers)
	if err != nil {
		return err
	}

	srv := config.MCPServer{
		Name:    name,
		URL:     f.url,
		Command: f.command,
		Args:    f.args,
		Headers: headers,
		OAuth:   f.oauth,
	}

	path, err := config.MCPStoreFile(f.project)
	if err != nil {
		return err
	}
	if err := config.AddMCPServer(path, srv, f.force); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Added MCP server %q to %s\n", name, path)
	if !f.project {
		warnAboutProjectShadowing(out, path)
	}
	if srv.URL != "" && (srv.OAuth || f.oauth) {
		fmt.Fprintf(out, "\nRun `pi mcp login %s` to authorize it now; otherwise the first\n"+
			"request opens the browser instead.\n", name)
	}
	return nil
}

// warnAboutProjectShadowing tells the user when a project mcp.json would hide
// the global entry that was just written.
//
// LoadMCPServersFrom lets a project mcp.json replace the global server list
// outright, so from inside such a tree the entry added here does not exist. That
// is a deliberately loud warning: "Added MCP server" followed by a session that
// cannot find it is the worst version of this, and the fix — --project — is one
// flag away.
func warnAboutProjectShadowing(out io.Writer, globalPath string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	projectPath := config.NearestProjectMCPFile(cwd)
	if projectPath == "" || projectPath == globalPath {
		return
	}
	fmt.Fprintf(out, "\nNote: %s replaces the global server list in this tree, so this entry\n"+
		"will not be visible from here. Re-run with --project to add it there instead.\n", projectPath)
}

// validateMCPURL rejects a URL the Streamable HTTP transport cannot dial before
// it reaches mcp.json, where it would surface only as a confusing connect error
// on the next agent run.
func validateMCPURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid --url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid --url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid --url %q: no host", raw)
	}
	return nil
}

// parseMCPHeaders turns repeated key=value flags into the header map mcp.json
// stores. A malformed pair is an error rather than a skipped entry: silently
// dropping an Authorization header would produce a server that connects
// unauthenticated and looks broken.
func parseMCPHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(raw))
	for _, h := range raw {
		k, v, ok := strings.Cut(h, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid --header %q: expected key=value", h)
		}
		headers[strings.TrimSpace(k)] = v
	}
	return headers, nil
}

func newMCPLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Authorize a remote MCP server and cache its token",
		Long: `Authorize a remote MCP server now, ahead of any session.

The OAuth authorization-code flow runs against the server, the browser opens for
approval, and the resulting token is cached under ~/.pi-go/mcp-oauth. Later
sessions find that token on disk and present it from the start, so an agent run
never has to stop and ask for a browser.

A server that already has working credentials is reported as authorized without
opening the browser.

The server must be configured first — see ` + "`pi mcp add`" + `.`,
		Example: `  pi mcp login superlogical
  pi mcp login cloudflare-api`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPLogin(cmd, args[0])
		},
	}
	return cmd
}

// mcpLoginFn runs the authorization flow. It is a variable so a test can drive
// all three outcomes — cached credentials reused, freshly authorized, failed —
// without an authorization server to talk to. Production leaves it as
// extension.LoginMCP.
var mcpLoginFn = extension.LoginMCP

func runMCPLogin(cmd *cobra.Command, name string) error {
	srv, err := lookupMCPServer(name)
	if err != nil {
		return err
	}
	if srv.URL == "" {
		return fmt.Errorf("MCP server %q is a stdio server (command %q); there is no token to authorize",
			srv.Name, srv.Command)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Authorizing MCP server %q at %s\n", srv.Name, srv.URL)

	err = mcpLoginFn(cmd.Context(), srv)
	switch {
	case errors.Is(err, extension.ErrMCPLoginReusedToken):
		fmt.Fprintf(out, "Already authorized — cached credentials for %q are still valid.\n", srv.Name)
		return nil
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "Authorized %q. Cached credentials will be used by every later session.\n", srv.Name)
	return nil
}

// lookupMCPServer resolves a configured server by name from the merged config —
// config.json plus both mcp.json scopes — so `login` accepts a server however it
// was declared.
func lookupMCPServer(name string) (extension.MCPServerConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return extension.MCPServerConfig{}, fmt.Errorf("loading configuration: %w", err)
	}
	var names []string
	if cfg.MCP != nil {
		for _, s := range cfg.MCP.Servers {
			if s.Name == name {
				return extension.MCPServerConfig{
					Name:    s.Name,
					Command: s.Command,
					Args:    s.Args,
					URL:     s.URL,
					Headers: s.Headers,
					OAuth:   s.OAuth,
				}, nil
			}
			names = append(names, s.Name)
		}
	}
	hint := "no MCP servers are configured; add one with `pi mcp add <name> --url <url>`"
	if len(names) > 0 {
		sort.Strings(names)
		hint = "configured servers: " + strings.Join(names, ", ")
	}
	return extension.MCPServerConfig{}, fmt.Errorf("no MCP server named %q (%s)", name, hint)
}

func newMCPListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers",
		Long: `List the MCP servers pi-go would load, from config.json and from both
mcp.json scopes, with each server's transport and whether cached credentials are
already stored for it.

Use ` + "`pi mcp status`" + ` inside a session for live connection state.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPList(cmd)
		},
	}
	return cmd
}

func runMCPList(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	out := cmd.OutOrStdout()
	if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
		fmt.Fprintln(out, "No MCP servers configured.")
		fmt.Fprintln(out, "\nAdd one with `pi mcp add <name> --url <url>` or `pi mcp add <name> --command <cmd>`.")
		return nil
	}

	servers := append([]config.MCPServer(nil), cfg.MCP.Servers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	fmt.Fprintf(out, "%-24s %-9s %-10s %s\n", "NAME", "TRANSPORT", "AUTH", "TARGET")
	for _, s := range servers {
		transport, target := "stdio", s.Command
		if s.URL != "" {
			transport, target = "http", httplog.RedactURL(s.URL)
		}
		auth := "none"
		switch {
		case s.URL == "":
			auth = "-"
		case extension.HasCachedMCPToken(s.Name, s.URL):
			auth = "cached"
		case s.OAuth:
			auth = "oauth"
		}
		fmt.Fprintf(out, "%-24s %-9s %-10s %s\n", s.Name, transport, auth, target)
	}
	return nil
}

func newMCPRemoveCmd() *cobra.Command {
	var project, global bool
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove an MCP server from mcp.json",
		Long: `Remove a server from the standalone mcp.json.

Only mcp.json is edited. A server declared in config.json under "mcp.servers" is
not touched — remove that entry by hand, since config.json also holds unrelated
settings this command has no business rewriting.

The scope defaults to the global file; --project removes from this tree instead.
Removing from the wrong scope fails loudly rather than doing nothing, naming the
file it looked in.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project && global {
				return errors.New("--project and --global are mutually exclusive")
			}
			return runMCPRemove(cmd, args[0], project)
		},
	}
	cmd.Flags().BoolVar(&project, "project", false, "remove from .pi-go/mcp.json in this tree")
	cmd.Flags().BoolVar(&global, "global", false, "remove from the global ~/.pi-go/mcp.json (default)")
	return cmd
}

func runMCPRemove(cmd *cobra.Command, name string, project bool) error {
	path, err := config.MCPStoreFile(project)
	if err != nil {
		return err
	}
	if err := config.RemoveMCPServer(path, name); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed MCP server %q from %s\n", name, path)
	return nil
}
