package cli

import (
	"context"
	"log/slog"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// openServerSessionStore opens the on-disk session store the ACP and A2A
// servers persist transcripts in — the same directory the interactive CLI
// uses, so a conversation started by an editor or a gateway can be reopened
// from the terminal, and survives the server process that started it.
//
// A store that cannot be opened is reported and skipped rather than fatal:
// the server then runs with in-memory sessions, which is how it always ran,
// instead of refusing to start over a directory it cannot write.
func openServerSessionStore(ctx context.Context, logger *slog.Logger) *pisession.FileService {
	dir, err := sessionsDir()
	if err == nil {
		var svc *pisession.FileService
		svc, err = pisession.NewFileService(dir)
		if err == nil {
			if logger != nil {
				logger.Log(ctx, slog.LevelInfo, "server: session store opened", "dir", dir)
			}
			return svc
		}
	}
	if logger != nil {
		logger.Log(ctx, slog.LevelWarn, "server: session store unavailable, sessions are in-memory only", "err", err)
	}
	return nil
}

// serverSessionStore adapts the optional on-disk store to the ACP agent's
// SessionStore seam. A nil service yields a nil store, which the agent treats
// as "in-memory sessions only".
func serverSessionStore(svc *pisession.FileService) acpserver.SessionStore {
	if svc == nil {
		return nil
	}
	return acpserver.NewFileSessionStore(svc)
}
