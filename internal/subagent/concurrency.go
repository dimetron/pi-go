package subagent

import (
	"log/slog"
	"os"
	"strconv"
)

// ConcurrencyEnvVar names the environment variable that sets how many
// subagents one process may run at once. It is on the forwarded-prefix list in
// environ.go, so a value set here reaches spawned agents — see
// childConcurrency for why it is rewritten rather than inherited unchanged.
const ConcurrencyEnvVar = "PI_SUBAGENT_CONCURRENCY"

// maxConcurrencyBudget bounds what the environment can ask for. The pool exists
// to keep concurrent token draw under the provider's per-minute limit, so a
// mistyped value must not be able to remove the ceiling entirely.
const maxConcurrencyBudget = 64

// ConcurrencyFromEnv returns the per-process subagent budget.
//
// It falls back to DefaultPoolSize when the variable is unset, unparseable or
// out of range, and never returns less than 1: a zero-sized pool would block
// the first Acquire forever, turning a misconfiguration into a hang rather
// than a slow run.
func ConcurrencyFromEnv() int {
	raw := os.Getenv(ConcurrencyEnvVar)
	if raw == "" {
		return DefaultPoolSize
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("ignoring unparseable subagent concurrency",
			"var", ConcurrencyEnvVar, "value", raw, "using", DefaultPoolSize)
		return DefaultPoolSize
	}
	if n < 1 {
		slog.Warn("ignoring non-positive subagent concurrency",
			"var", ConcurrencyEnvVar, "value", n, "using", DefaultPoolSize)
		return DefaultPoolSize
	}
	if n > maxConcurrencyBudget {
		slog.Warn("clamping subagent concurrency to the ceiling",
			"var", ConcurrencyEnvVar, "value", n, "using", maxConcurrencyBudget)
		return maxConcurrencyBudget
	}
	return n
}

// childConcurrency returns the budget a spawned agent should be given.
//
// The budget has to shrink with depth. A spawned agent is itself a pi process
// that builds its own orchestrator and its own pool, so an inherited value is
// not a shared allowance — it is a fresh one per process, and total concurrency
// becomes depth x budget. That is what put eight agents in flight against a
// per-minute token limit that only tolerated a few.
//
// Halving with a floor of 1 makes the total converge instead of grow, and
// reaches "one thing at a time" quickly for deeply nested work, which is the
// desired behavior that far from the top-level run.
func childConcurrency(parent int) int {
	if parent < 2 {
		return 1
	}
	return parent / 2
}
