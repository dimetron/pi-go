package palace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MineConversations reads conversation files from a directory and inserts
// extracted exchanges as drawers. It supports pi-go JSONL, Claude Code JSONL,
// and plain text with ">" markers.
func MineConversations(ctx context.Context, palace *Palace, dir string, cfg *MineConfig) (*MineResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("mine-convo: resolve dir: %w", err)
	}

	if cfg == nil {
		loaded, loadErr := readMempalaceYAML(absDir)
		if loadErr != nil {
			cfg = &MineConfig{Wing: filepath.Base(absDir)}
		} else {
			cfg = loaded
		}
	}

	if cfg.Wing == "" {
		cfg.Wing = filepath.Base(absDir)
	}

	result := &MineResult{}

	err = filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Errors++
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name[0] == '.' && name != "." || skipDirNames[name] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		relPath, _ := filepath.Rel(absDir, path)

		var exchanges []exchange

		switch ext {
		case ".jsonl":
			exchanges, err = parseJSONL(path)
			if err != nil {
				result.Errors++
				slog.Warn("mine-convo: parse jsonl", "path", relPath, "error", err)
				return nil
			}
		case ".txt", ".md":
			exchanges, err = parsePlainText(path)
			if err != nil {
				result.Errors++
				slog.Warn("mine-convo: parse text", "path", relPath, "error", err)
				return nil
			}
		default:
			return nil
		}

		for i, ex := range exchanges {
			result.Processed++

			room := detectRoomFromContent(ex.content, cfg.Rooms)

			_, addErr := palace.AddDrawer(ctx, DrawerInput{
				Wing:       cfg.Wing,
				Room:       room,
				Content:    ex.content,
				SourceFile: relPath,
				ChunkIndex: i,
				AddedBy:    "miner:convo",
				Importance: 4,
			})
			if addErr != nil {
				var dupErr *DuplicateError
				if errors.As(addErr, &dupErr) {
					result.Skipped++
				} else {
					result.Errors++
					slog.Warn("mine-convo: add drawer", "file", relPath, "exchange", i, "error", addErr)
				}
				continue
			}
			result.Added++
		}
		return nil
	})

	if err != nil {
		return result, fmt.Errorf("mine-convo: walk: %w", err)
	}

	return result, nil
}

// exchange represents a user-assistant exchange pair.
type exchange struct {
	content string
}

// jsonlMessage is a single message in a JSONL conversation file.
type jsonlMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Type    string `json:"type"` // pi-go uses "type" instead of "role"
}

// parseJSONL reads a JSONL file and extracts user+assistant exchange pairs.
// Supports both pi-go format (type field) and Claude Code format (role field).
func parseJSONL(path string) ([]exchange, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []jsonlMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg jsonlMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // skip malformed lines
		}
		// Normalize: pi-go uses "type" field, Claude Code uses "role".
		if msg.Role == "" && msg.Type != "" {
			msg.Role = msg.Type
		}
		if msg.Content == "" || (msg.Role != "user" && msg.Role != "assistant") {
			continue
		}
		messages = append(messages, msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return pairExchanges(messages), nil
}

// parsePlainText reads a text file where user turns begin with ">" or "User:"
// and assistant turns follow.
func parsePlainText(path string) ([]exchange, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	var messages []jsonlMessage
	var currentRole string
	var currentContent strings.Builder

	flush := func() {
		text := strings.TrimSpace(currentContent.String())
		if text != "" && currentRole != "" {
			messages = append(messages, jsonlMessage{
				Role:    currentRole,
				Content: text,
			})
		}
		currentContent.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "> ") || strings.HasPrefix(trimmed, "User:") {
			flush()
			currentRole = "user"
			text := strings.TrimPrefix(trimmed, "> ")
			text = strings.TrimPrefix(text, "User:")
			text = strings.TrimSpace(text)
			currentContent.WriteString(text)
			currentContent.WriteString("\n")
		} else if strings.HasPrefix(trimmed, "Assistant:") || strings.HasPrefix(trimmed, "AI:") {
			flush()
			currentRole = "assistant"
			text := strings.TrimPrefix(trimmed, "Assistant:")
			text = strings.TrimPrefix(text, "AI:")
			text = strings.TrimSpace(text)
			currentContent.WriteString(text)
			currentContent.WriteString("\n")
		} else if currentRole != "" {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}
	flush()

	return pairExchanges(messages), nil
}

// pairExchanges groups consecutive user+assistant messages into exchanges.
func pairExchanges(messages []jsonlMessage) []exchange {
	var exchanges []exchange
	for i := 0; i < len(messages); i++ {
		if messages[i].Role == "user" {
			var parts []string
			parts = append(parts, fmt.Sprintf("User: %s", messages[i].Content))
			// Collect following assistant messages.
			for i+1 < len(messages) && messages[i+1].Role == "assistant" {
				i++
				parts = append(parts, fmt.Sprintf("Assistant: %s", messages[i].Content))
			}
			combined := strings.Join(parts, "\n\n")
			if len(combined) > 3000 {
				combined = combined[:3000]
			}
			exchanges = append(exchanges, exchange{content: combined})
		}
	}
	return exchanges
}

// detectRoomFromContent uses keyword scoring to find the best room match.
func detectRoomFromContent(content string, rooms []RoomDef) string {
	if len(rooms) == 0 {
		return "general"
	}

	lower := strings.ToLower(content)
	bestRoom := "general"
	bestScore := 0

	for _, room := range rooms {
		score := 0
		// Check room name.
		if strings.Contains(lower, strings.ToLower(room.Name)) {
			score += 3
		}
		// Check keywords.
		for _, kw := range room.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				score += 2
			}
		}
		if score > bestScore {
			bestScore = score
			bestRoom = room.Name
		}
	}

	return bestRoom
}
