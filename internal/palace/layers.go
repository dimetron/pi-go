package palace

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// MemoryStack implements the 4-layer memory system for system prompt context
// injection. L0 = identity, L1 = essential story, L2 = on-demand recall,
// L3 = search (delegated to DrawerService.Search).
type MemoryStack struct {
	store    PalaceStore
	embedder *Embedder
	config   PalaceConfig
}

// NewMemoryStack creates a MemoryStack from the given store, embedder, and config.
func NewMemoryStack(store PalaceStore, embedder *Embedder, config PalaceConfig) *MemoryStack {
	return &MemoryStack{
		store:    store,
		embedder: embedder,
		config:   config,
	}
}

// loadIdentity reads the L0 identity file as plain text. Returns empty string
// if the file is not configured or does not exist.
func (ms *MemoryStack) loadIdentity() (string, error) {
	if ms.config.IdentityFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(ms.config.IdentityFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("palace: read identity file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// loadEssentialStory builds the L1 essential story: top drawers sorted by
// importance, grouped by room, truncated to fit within L1MaxChars.
func (ms *MemoryStack) loadEssentialStory(ctx context.Context, wing string) (string, error) {
	filter := DrawerFilter{Wing: wing}
	drawers, err := ms.store.ListDrawers(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("palace: list drawers for L1: %w", err)
	}
	if len(drawers) == 0 {
		return "", nil
	}

	// Sort by importance descending, then by created_at descending for tiebreak.
	sort.Slice(drawers, func(i, j int) bool {
		if drawers[i].Importance != drawers[j].Importance {
			return drawers[i].Importance > drawers[j].Importance
		}
		return drawers[i].CreatedAt.After(drawers[j].CreatedAt)
	})

	// Take top K.
	topK := ms.config.L1TopK
	if topK <= 0 {
		topK = 15
	}
	if len(drawers) > topK {
		drawers = drawers[:topK]
	}

	// Group by room.
	type roomGroup struct {
		room    string
		entries []string
	}
	roomOrder := []string{}
	roomMap := map[string]*roomGroup{}

	for _, d := range drawers {
		content := truncateChars(d.Content, 200)
		rg, ok := roomMap[d.Room]
		if !ok {
			rg = &roomGroup{room: d.Room}
			roomMap[d.Room] = rg
			roomOrder = append(roomOrder, d.Room)
		}
		rg.entries = append(rg.entries, content)
	}

	// Format as markdown sections, hard cap at L1MaxChars.
	maxChars := ms.config.L1MaxChars
	if maxChars <= 0 {
		maxChars = 3200
	}

	var sb strings.Builder
	for _, roomName := range roomOrder {
		rg := roomMap[roomName]
		section := fmt.Sprintf("### %s\n", rg.room)
		for _, entry := range rg.entries {
			section += fmt.Sprintf("- %s\n", entry)
		}
		section += "\n"

		if sb.Len()+len(section) > maxChars {
			break
		}
		sb.WriteString(section)
	}

	return strings.TrimSpace(sb.String()), nil
}

// Recall returns L2 on-demand context: drawers matching wing+room, up to
// L2MaxDrawers items, each truncated to L2MaxCharsPerDrawer characters.
func (ms *MemoryStack) Recall(ctx context.Context, wing, room string) (string, error) {
	filter := DrawerFilter{Wing: wing, Room: room}
	drawers, err := ms.store.ListDrawers(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("palace: recall drawers: %w", err)
	}
	if len(drawers) == 0 {
		return "", nil
	}

	maxDrawers := ms.config.L2MaxDrawers
	if maxDrawers <= 0 {
		maxDrawers = 10
	}
	maxCharsPerDrawer := ms.config.L2MaxCharsPerDrawer
	if maxCharsPerDrawer <= 0 {
		maxCharsPerDrawer = 300
	}

	if len(drawers) > maxDrawers {
		drawers = drawers[:maxDrawers]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s / %s\n\n", wing, room))
	for _, d := range drawers {
		content := truncateChars(d.Content, maxCharsPerDrawer)
		sb.WriteString(fmt.Sprintf("- %s\n", content))
	}

	return strings.TrimSpace(sb.String()), nil
}

// WakeUp returns the combined L0 + L1 context string for system prompt injection.
func (ms *MemoryStack) WakeUp(ctx context.Context, wing string) (string, error) {
	identity, err := ms.loadIdentity()
	if err != nil {
		return "", err
	}

	story, err := ms.loadEssentialStory(ctx, wing)
	if err != nil {
		return "", err
	}

	if identity == "" && story == "" {
		return "", nil
	}

	var parts []string
	if identity != "" {
		parts = append(parts, fmt.Sprintf("## Identity\n\n%s", identity))
	}
	if story != "" {
		parts = append(parts, fmt.Sprintf("## Essential Knowledge\n\n%s", story))
	}

	return strings.Join(parts, "\n\n"), nil
}

// truncateChars truncates a string to maxChars, adding "…" if truncated.
func truncateChars(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "…"
}
