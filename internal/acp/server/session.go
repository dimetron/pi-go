package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	acp "github.com/coder/acp-go-sdk"
)

// ServeConfig configures a server.Serve run.
type ServeConfig struct {
	// Agent is the pi ACP agent. Required.
	Agent *Agent
	// In is the byte stream from the ACP client. Typically os.Stdin.
	In io.Reader
	// Out is the byte stream to the ACP client. Typically os.Stdout.
	Out io.Writer
	// Logger receives diagnostic output from the ACP connection. Optional.
	Logger *slog.Logger
}

// Serve runs pi as an ACP agent over the given byte streams and returns when
// the peer disconnects or ctx is canceled. Serve does not close In or Out;
// callers own the streams.
func Serve(ctx context.Context, cfg ServeConfig) error {
	if cfg.Agent == nil {
		return fmt.Errorf("serve: agent is required")
	}
	if cfg.In == nil || cfg.Out == nil {
		return fmt.Errorf("serve: in and out are required")
	}

	conn := acp.NewAgentSideConnection(cfg.Agent, cfg.Out, cfg.In)
	if cfg.Logger != nil {
		conn.SetLogger(cfg.Logger)
	}
	cfg.Agent.SetAgentConnection(conn)

	select {
	case <-conn.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
