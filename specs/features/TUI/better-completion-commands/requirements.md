# Requirements

## Questions & Answers

**Q1: What type of completion commands are we improving?**

A: CLI command/argument completion for the pi-go tool's interactive TUI. The user types in the chat input and presses TAB to complete `/commands`.

**Q2: What commands currently exist in pi-go that need completion support?**

A: The TUI supports slash commands: `/help`, `/clear`, `/model`, `/session`, `/branch`, `/compact`, `/agents`, `/history`, `/commit`, `/plan`, `/run`, `/exit`, `/quit`. Currently has basic single-match completion.

**Q3: What shell completions do you need? Dynamic or static?**

A: Not shell completions - this is for the interactive TUI input. Completion should be dynamic, based on current state (available skills, spec names).

**Q4: What specifically needs improvement?**

A: All four:
1. Better completion behavior (show all matches, cycle through options, popup list)
2. Add skill completion (dynamically complete skills from ~/.pi-go/skills/ and .pi-go/skills/)
3. Subcommand completion (/plan and /run should complete spec names from specs/)
4. Better UI/UX (inline completion vs popup vs dropdown)

**Q5: When user types `/plan ` or `/run ` (with space), what should complete?**

A: Just spec names (subdirectories in specs/ directory). For example, if specs/ contains `my-feature/`, typing `/plan my` should complete to `/plan my-feature`.

---

## Summary

- **Feature**: Improve TAB completion in pi-go's interactive TUI
- **Current state**: Basic single-match completion for slash commands
- **Desired state**:
  1. Show all matching completions (not just first match)
  2. Support cycling through matches or popup selection
  3. Complete skills from skills directories when typing `/<skill-name>`
  4. Complete spec names when typing `/plan ` or `/run ` with arguments
  5. Better visual presentation (popup/dropdown)