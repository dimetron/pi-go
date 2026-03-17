# Research: Better Completion Commands

## Overview
Research findings for improving TAB completion in pi-go's interactive TUI.

## Current Implementation

### Completion Flow
- **Entry point**: `tui.handleKey()` at line ~410 in `tui.go` handles `tea.KeyTab`
- **Current behavior**: 
  - If input is just `/`, shows command list via `showCommandList()`
  - Otherwise applies single match from `completeSlashCommand(m.input)`
- **Ghost text**: Stored in `model.completion` field, rendered inline after cursor

### Key Files
- `internal/tui/tui.go` - Main TUI with completion logic
- `internal/tui/run.go` - Has `listAvailableSpecs()` for spec discovery
- `internal/extension/skills.go` - Has `LoadSkills()` for skill discovery

### Current Code Locations
| Component | Location | Notes |
|-----------|----------|-------|
| Slash commands list | `tui.go:1089` | Hardcoded `[]string` |
| Single match completion | `tui.go:1107-1118` | Returns only first match |
| Show command list | `tui.go:1136` | Prints all as message |
| Spec discovery | `run.go:632-656` | `listAvailableSpecs(workDir)` |
| Skill loading | `extension/skills.go:26` | `LoadSkills(dirs...)` |

### Config Availability
- `Config.WorkDir` - Contains current working directory (for spec completion)
- Skills loaded in CLI at `cli.go:216-221` but not passed to TUI

## Findings

### 1. Better Completion Behavior
**Current**: Single match, no cycling, no popup
**Needed**: Multi-match support, cycling (Tab/Shift+Tab), or popup list

### 2. Skill Completion
**Current**: Not available
**Needed**: 
- Access to skill names from `~/.pi-go/skills/` and `.pi-go/skills/`
- Skills should be dynamically loaded and included in completion list

### 3. Spec Completion
**Current**: `listAvailableSpecs()` exists in `run.go`
**Needed**: 
- Integrate into completion for `/plan ` and `/run ` arguments
- Should complete spec names (subdirectories with PROMPT.md)

### 4. Better UI/UX
**Current**: Ghost text after cursor, no list
**Needed**: Consider popup/dropdown for multiple matches

## Build Commands
- Build: `go build ./cmd/pi`
- Test: `go test ./...`
- Lint: `go vet ./...`

## Dependencies
- Uses Bubble Tea (`github.com/charmbracelet/bubbletea`)
- Uses Bubble Key (`github.com/charmbracelet/bubbles/key`)
- Uses Glamour for markdown rendering