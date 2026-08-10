package tools

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// The subagent tool description must report the pool size, not only the
// per-call cap. They are different numbers: maxParallelTasks limits how many
// tasks one call may name, while the pool gates spawning. A model told only
// "max 8" will batch eight tasks into a process that runs one at a time, and
// the batch serializes inside a single tool call instead of overlapping.
func TestSubagentDescription_ReportsEffectiveConcurrency(t *testing.T) {
	t.Setenv(subagent.ConcurrencyEnvVar, "4")
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	desc := buildSubagentDescription(orch)

	if !strings.Contains(desc, "runs 4 subagent(s) at a time") {
		t.Errorf("description does not state the effective concurrency:\n%s", desc)
	}
	if !strings.Contains(desc, "queue rather than overlap") {
		t.Error("description does not warn that oversized batches queue")
	}
}

// At a concurrency of 1 the advice has to change outright — batching buys
// nothing and only lengthens the call.
func TestSubagentDescription_WarnsWhenParallelIsPointless(t *testing.T) {
	t.Setenv(subagent.ConcurrencyEnvVar, "1")
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	desc := buildSubagentDescription(orch)

	if !strings.Contains(desc, "no speed-up") {
		t.Errorf("description does not tell the model parallel mode is pointless here:\n%s", desc)
	}
	if strings.Contains(desc, "queue rather than overlap") {
		t.Error("description gives the batching advice that only applies above 1")
	}
}
