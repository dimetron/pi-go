package tools

import (
	"regexp"
	"strings"
)

// runtimeNoisePatterns match diagnostics macOS's libmalloc writes to a
// process's stderr on its own initiative — before main() runs, or when some
// library toggles malloc debugging. They describe the allocator, not the
// command the agent asked for, so they are noise in a tool result:
//
//	msl(16169) MallocStackLogging: can't turn off malloc stack logging because it was not enabled.
//	true(15462) MallocStackLogging: stack logging disabled due to previous errors.
//	node(4821) malloc: nano zone abandoned due to inability to preallocate reserved vm space.
//
// The leading "name(pid) " prefix is what libmalloc's own reporter adds; it is
// optional here because a process that reports through a different path omits
// it.
var runtimeNoisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^(?:\S+\(\d+\) )?Malloc[A-Za-z]*: `),
	regexp.MustCompile(`^(?:\S+\(\d+\) )?malloc: nano zone abandoned `),
}

// stripRuntimeNoise removes allocator chatter from captured stderr.
//
// Dropping whole lines rather than rewriting them keeps the rest of stderr
// byte-identical, so a real error message is never reshaped on its way to the
// model. When nothing but noise was captured the result is empty rather than a
// run of blank lines, which is what makes the difference visible in the TUI:
// a command that "failed" with only a MallocStackLogging line now renders with
// no stderr block at all.
func stripRuntimeNoise(s string) string {
	// Every pattern requires the literal "alloc", so this guard skips the
	// per-line scan for the overwhelming majority of outputs.
	if !strings.Contains(s, "alloc") {
		return s
	}

	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if isRuntimeNoise(line) {
			continue
		}
		kept = append(kept, line)
	}

	out := strings.Join(kept, "\n")
	if strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

func isRuntimeNoise(line string) bool {
	for _, re := range runtimeNoisePatterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
