package palace

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/dimetron/pi-go/internal/memory"
)

// ObservationBridge converts memory.Observation records into palace drawers,
// connecting the existing observation pipeline to the palace system.
type ObservationBridge struct {
	palace *Palace
}

// NewObservationBridge creates a bridge that routes observations into the palace.
func NewObservationBridge(palace *Palace) *ObservationBridge {
	return &ObservationBridge{palace: palace}
}

// ConvertAndStore maps a memory.Observation to a DrawerInput and stores it.
// Errors are logged but never propagated — bridge failures must not block
// the observation pipeline.
func (b *ObservationBridge) ConvertAndStore(ctx context.Context, obs *memory.Observation) {
	if obs == nil {
		return
	}

	wing := deriveWing(obs.Project)
	room := deriveRoom(obs.SourceFiles)
	hall := hallFromObsType(obs.Type)

	input := DrawerInput{
		Wing:       wing,
		Room:       room,
		Hall:       hall,
		Content:    obs.Text,
		SourceFile: firstSourceFile(obs.SourceFiles),
		AddedBy:    obs.ToolName,
		Importance: importanceFromObsType(obs.Type),
	}

	if _, err := b.palace.AddDrawer(ctx, input); err != nil {
		// DuplicateError is expected and fine — just skip.
		var dupErr *DuplicateError
		if errors.As(err, &dupErr) {
			slog.Debug("palace bridge: skipped duplicate observation",
				"title", obs.Title,
				"wing", wing,
				"room", room,
			)
			return
		}
		slog.Warn("palace bridge: failed to store observation",
			"title", obs.Title,
			"error", err,
		)
	}
}

// deriveWing extracts a wing name from the project path.
// Uses the last path component (directory basename).
func deriveWing(project string) string {
	if project == "" {
		return "general"
	}
	base := filepath.Base(project)
	if base == "." || base == "/" {
		return "general"
	}
	return strings.ToLower(base)
}

// deriveRoom extracts a room name from the first source file's directory path.
// e.g. "internal/auth/handler.go" → "auth"
func deriveRoom(sourceFiles []string) string {
	if len(sourceFiles) == 0 {
		return "general"
	}
	dir := filepath.Dir(sourceFiles[0])
	if dir == "." || dir == "" {
		return "general"
	}
	// Use the deepest meaningful directory component.
	// e.g. "internal/auth" → "auth", "cmd/server" → "server"
	parts := strings.Split(filepath.ToSlash(dir), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p != "" && p != "." && p != "internal" && p != "cmd" && p != "pkg" {
			return strings.ToLower(p)
		}
	}
	// Fallback to last component even if it's "internal"
	if last := parts[len(parts)-1]; last != "" && last != "." {
		return strings.ToLower(last)
	}
	return "general"
}

// hallFromObsType maps a memory.ObservationType to a palace hall name.
func hallFromObsType(t memory.ObservationType) string {
	switch t {
	case memory.TypeDecision:
		return "hall_decisions"
	case memory.TypeBugfix:
		return "hall_bugs"
	case memory.TypeFeature:
		return "hall_features"
	case memory.TypeRefactor:
		return "hall_refactors"
	case memory.TypeDiscovery:
		return "hall_discoveries"
	case memory.TypeChange:
		return "hall_changes"
	default:
		return ""
	}
}

// importanceFromObsType assigns default importance based on observation type.
func importanceFromObsType(t memory.ObservationType) int {
	switch t {
	case memory.TypeDecision:
		return 8
	case memory.TypeBugfix:
		return 7
	case memory.TypeFeature:
		return 7
	case memory.TypeDiscovery:
		return 6
	case memory.TypeRefactor:
		return 5
	case memory.TypeChange:
		return 4
	default:
		return 5
	}
}

// firstSourceFile returns the first source file or empty string.
func firstSourceFile(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return files[0]
}
