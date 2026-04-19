package cli

import (
	"fmt"
	"os"
	"os/signal"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

func newACPServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "acp-server",
		Short: "Run pi as an ACP agent over stdio",
		Long: `Run pi as an ACP (Agent Client Protocol) agent that communicates with an
external ACP client over stdin/stdout. Use this when another tool drives pi
through the ACP protocol. The server returns when the peer disconnects or the
process receives SIGINT.`,
		Args: cobra.NoArgs,
		RunE: runACPServer,
	}
}

func runACPServer(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	agent := &acpserver.Agent{
		AgentInfo: acp.Implementation{Name: "pi-go", Version: Version},
	}
	if err := acpserver.Serve(ctx, acpserver.ServeConfig{
		Agent: agent,
		In:    os.Stdin,
		Out:   os.Stdout,
	}); err != nil {
		return fmt.Errorf("acp server: %w", err)
	}
	return nil
}
