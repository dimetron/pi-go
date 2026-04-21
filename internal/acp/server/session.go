package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

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

	// If no logger provided, create a discard logger so we always have somewhere
	// to log diagnostics even in production (errors still go to err.log file
	// in cli.acp_server.go).
	var logger *slog.Logger
	if cfg.Logger == nil {
		logger = slog.New(slog.DiscardHandler)
	} else {
		logger = cfg.Logger
	}

	logger.Log(ctx, slog.LevelInfo, "acp-server: starting")
	start := time.Now()

	conn := acp.NewAgentSideConnection(cfg.Agent, cfg.Out, cfg.In)
	conn.SetLogger(logger)

	logger.Log(ctx, slog.LevelDebug, "acp-server: connection created")

	cfg.Agent.SetAgentConnection(conn)

	select {
	case <-conn.Done():
		logger.Log(ctx, slog.LevelInfo, "acp-server: peer disconnected", "uptime", time.Since(start))
		return nil
	case <-ctx.Done():
		logger.Log(ctx, slog.LevelInfo, "acp-server: context canceled", "uptime", time.Since(start))
		return ctx.Err()
	}
}
