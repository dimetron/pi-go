# Better Completion Commands

## Objective
Improve TAB completion in pi-go's interactive TUI to support multiple matches, cycling, skill completion, spec completion for /plan and /run commands, and a better visual presentation.

## Key Requirements

1. **Better Completion Behavior** — Show all matching completions (not just first match), support cycling through options with Tab/Shift+Tab or show popup list
   
2. **Skill Completion** — Dynamically complete skill names from `~/.pi-go/skills/` and `.pi-go/skills/` directories when typing `/<skill-name>`

3. **Spec Completion** — Complete spec names from `specs/` directory when user types `/plan ` or `/run ` with arguments

4. **Better UI/UX** — Add visual indicator for selected completion, support Enter to apply

## Acceptance Criteria

### Command Completion
- Given user types `/pl`, when they press TAB, then completion shows `/plan` as ghost text
- Given user types `/`, when they press TAB, then all commands are shown

### Skill Completion  
- Given skills exist in skills directories, when user types `/skill-name`, then matching skills appear in completion
- Given no skills exist, then only built-in commands are shown

### Spec Completion
- Given spec directories exist in `specs/`, when user types `/plan `, then spec names appear in completion
- Given user types `/run my-`, then specs starting with "my-" appear

### Cycling/Multi-match
- Given multiple matches exist, when user presses TAB, then they can cycle through options
- Given pressing Enter on a selection, then input is updated to selected value

## Gates
- **build**: `go build ./cmd/pi`
- **test**: `go test ./...`
- **vet**: `go vet ./...`

## Reference
- Design: `specs/better-completion-commands/design.md`
- Plan: `specs/better-completion-commands/plan.md`
- Requirements: `specs/better-completion-commands/requirements.md`
- Research: `specs/better-completion-commands/research/`

## Constraints

- Must integrate with existing TUI using Bubble Tea framework
- Should reuse existing code: `slashCommands` list, `listAvailableSpecs()`, `extension.LoadSkills()`
- Config.WorkDir is available for spec discovery
- Skills need to be passed from CLI to TUI via Config

## Implementation Notes

1. Current completion logic is in `internal/tui/tui.go` around lines 1105-1120
2. Skills are loaded in CLI at `cli.go:216-221` but not passed to TUI
3. Spec discovery already exists in `run.go:632-656` as `listAvailableSpecs()`
4. Use bubble tea key handling for Tab/Shift+Tab cycling