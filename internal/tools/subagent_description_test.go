package tools

import (
	"encoding/json"
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

// TestSubagentInput_BaseRoundTrips guards the JSON tag, which is the only
// interface the model has to this field: the tool call arrives as JSON, so a
// misspelled tag would make base silently unusable while the Go code still
// compiled. It also pins that base keeps the call in single mode.
func TestSubagentInput_BaseRoundTrips(t *testing.T) {
	raw := `{"agent":"code-reviewer","task":"review","base":"origin/main~1"}`
	var in SubagentInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.Base != "origin/main~1" {
		t.Errorf("Base = %q, want %q", in.Base, "origin/main~1")
	}
	if got := detectMode(in); got != "single" {
		t.Errorf("detectMode = %q, want single", got)
	}
}

// TestSubagentDescription_MentionsBase: the description is the only place the
// model learns that committed work needs base. If the section is dropped, the
// model goes back to reviewing an empty diff and nothing else would report it.
func TestSubagentDescription_MentionsBase(t *testing.T) {
	// A real orchestrator, not nil: buildSubagentDescription walks the agent
	// registry, so a nil one panics before any assertion runs.
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	desc := buildSubagentDescription(orch)
	for _, want := range []string{"base", "empty diff"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description does not mention %q; a reviewer of committed work would read nothing", want)
		}
	}
}
