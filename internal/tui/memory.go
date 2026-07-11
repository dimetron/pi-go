package tui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/palace"
)

// memoryTickInterval is how often the sidebar memory status refreshes.
const memoryTickInterval = 30 * time.Second

// memoryTickMsg carries an updated palace status for the sidebar.
type memoryTickMsg struct {
	status *palace.PalaceStatus
}

// memoryTickCmd returns a command that queries the palace DB and returns
// a memoryTickMsg with the result. If no palace DB exists, it returns nil
// status (the sidebar section is hidden).
func memoryTickCmd(workDir string) tea.Cmd {
	return func() tea.Msg {
		dbPath := filepath.Join(workDir, ".pi-go", "palace.db")
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			return memoryTickMsg{status: nil}
		}

		p, err := palace.New(palace.WithDBPath(dbPath))
		if err != nil {
			return memoryTickMsg{status: nil}
		}
		defer p.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		status, err := p.Status(ctx)
		if err != nil {
			return memoryTickMsg{status: nil}
		}
		return memoryTickMsg{status: status}
	}
}
