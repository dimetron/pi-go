package diagram

import (
	"regexp"
	"strings"
)

// ── Orientation Control ──────────────────────────────────────────────────────
//
// Orientation is authorial intent ("lay this out vertically"), distinct from
// terminal width, which is environmental ("the terminal is this wide"). They
// are resolved independently.
//
// Two inputs, in precedence order:
//  1. CLI override (--orientation), set via SetOrientationOverride. Wins.
//  2. An in-diagram `direction TB|LR|...` directive (Mermaid already uses
//     `direction` for flowcharts/state; here it is a port-wide convention
//     usable by every diagram type).
//
// If neither is present, each renderer falls back to its own natural default.
//
// This deliberately does NOT consider terminal width: a width-triggered
// auto-switch conflates the two axes and cannot fire reliably when terminal
// detection is unavailable (e.g. inside some embedded shells).

// orientationOverride is the CLI-forced orientation: "" (none), "v", or "h".
var orientationOverride string

var reDirectionStmt = regexp.MustCompile(`(?i)^direction\s+(TB|TD|BT|LR|RL)$`)

// SetOrientationOverride forces orientation from the CLI, overriding any
// in-diagram directive. Accepts Mermaid direction tokens (TB/TD/BT/LR/RL,
// case-insensitive). Reports whether the token was recognized; an empty or
// unrecognized value clears the override and returns false.
func SetOrientationOverride(token string) bool {
	orientationOverride = directionToken(token)
	return orientationOverride != ""
}

// directionToken maps a Mermaid direction token to "v" (vertical), "h"
// (horizontal), or "" (unrecognized).
func directionToken(token string) string {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "TB", "TD", "BT":
		return "v"
	case "LR", "RL":
		return "h"
	default:
		return ""
	}
}

// resolveVertical reports whether a diagram should render vertically.
// Precedence: CLI override > in-diagram `direction` directive > defaultVertical.
func resolveVertical(source string, defaultVertical bool) bool {
	if orientationOverride != "" {
		return orientationOverride == "v"
	}
	if v := topLevelDirection(source); v != "" {
		return v == "v"
	}
	return defaultVertical
}

// topLevelDirection returns the "v"/"h" of the first `direction` statement
// that applies to the whole diagram — i.e. one at nesting depth 0.
//
// This matches Mermaid's own scoping: a `direction` inside a `subgraph`
// (flowchart) or a composite-state `{ ... }` block applies to that block, not
// the whole diagram, so it must not flip overall orientation. Returns "" when
// there is no governing top-level directive.
func topLevelDirection(source string) string {
	depth := 0
	for _, raw := range strings.Split(source, "\n") {
		line := raw
		if i := strings.Index(line, "%%"); i >= 0 {
			line = line[:i]
		}
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)

		// Close a block before evaluating, so `end` / `}` themselves are
		// never mistaken for content at the depth they close into.
		if lower == "end" || strings.HasPrefix(lower, "end ") || strings.HasSuffix(t, "}") {
			if depth > 0 {
				depth--
			}
			continue
		}

		if depth == 0 {
			if m := reDirectionStmt.FindStringSubmatch(t); m != nil {
				return directionToken(m[1])
			}
		}

		// Open a block scope: flowchart subgraphs and brace-delimited
		// composite states are where Mermaid legally nests `direction`.
		if strings.HasPrefix(lower, "subgraph") || strings.HasSuffix(t, "{") {
			depth++
		}
	}
	return ""
}
