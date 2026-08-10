package subagent

import (
	"strconv"
	"strings"
	"testing"
)

func TestConcurrencyFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		val  string
		want int
	}{
		{"unset falls back", false, "", DefaultPoolSize},
		{"empty falls back", true, "", DefaultPoolSize},
		{"valid value honored", true, "2", 2},
		{"one is allowed", true, "1", 1},
		{"zero falls back rather than deadlocking", true, "0", DefaultPoolSize},
		{"negative falls back", true, "-3", DefaultPoolSize},
		{"garbage falls back", true, "many", DefaultPoolSize},
		{"absurd value is clamped", true, "100000", maxConcurrencyBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(ConcurrencyEnvVar, tc.val)
			}
			if got := ConcurrencyFromEnv(); got != tc.want {
				t.Errorf("ConcurrencyFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

// A pool of zero blocks the first Acquire forever, so no input may produce one.
func TestConcurrencyFromEnv_NeverZero(t *testing.T) {
	for _, v := range []string{"", "0", "-1", "abc", "0.5", " "} {
		t.Setenv(ConcurrencyEnvVar, v)
		if got := ConcurrencyFromEnv(); got < 1 {
			t.Errorf("ConcurrencyFromEnv() = %d for %q; a zero-sized pool hangs", got, v)
		}
	}
}

func TestChildConcurrency_HalvesWithAFloor(t *testing.T) {
	for _, tc := range []struct{ parent, want int }{
		{8, 4}, {5, 2}, {4, 2}, {3, 1}, {2, 1}, {1, 1}, {0, 1}, {-4, 1},
	} {
		if got := childConcurrency(tc.parent); got != tc.want {
			t.Errorf("childConcurrency(%d) = %d, want %d", tc.parent, got, tc.want)
		}
	}
}

// The budget must converge as runs nest. Repeated application should reach the
// floor and stay there, never grow.
func TestChildConcurrency_ConvergesWithDepth(t *testing.T) {
	budget := 16
	seen := []int{budget}
	for range 10 {
		next := childConcurrency(budget)
		if next > budget {
			t.Fatalf("budget grew with depth: %v -> %d", seen, next)
		}
		budget = next
		seen = append(seen, budget)
	}
	if budget != 1 {
		t.Errorf("budget settled at %d, want 1 (chain %v)", budget, seen)
	}
}

// The regression this exists to prevent: a child inheriting the parent's
// budget verbatim, so each nesting level gets a fresh full-size pool.
func TestChildEnv_ReplacesInheritedBudget(t *testing.T) {
	t.Setenv(ConcurrencyEnvVar, "8")

	env := ChildEnv(ConcurrencyFromEnv())

	var values []string
	for _, e := range env {
		if strings.HasPrefix(e, ConcurrencyEnvVar+"=") {
			values = append(values, strings.TrimPrefix(e, ConcurrencyEnvVar+"="))
		}
	}
	if len(values) != 1 {
		t.Fatalf("%s appears %d times in the child env, want exactly 1: %v",
			ConcurrencyEnvVar, len(values), values)
	}
	got, err := strconv.Atoi(values[0])
	if err != nil {
		t.Fatalf("child budget %q is not a number: %v", values[0], err)
	}
	if got != 4 {
		t.Errorf("child budget = %d, want 4 (half of 8) — an inherited 8 would "+
			"give every nesting level a fresh full-size pool", got)
	}
}

// With no budget set, the child still gets an explicit one derived from the
// default rather than nothing at all.
func TestChildEnv_SetsBudgetEvenWhenParentHasNone(t *testing.T) {
	env := ChildEnv(ConcurrencyFromEnv())

	found := false
	for _, e := range env {
		if strings.HasPrefix(e, ConcurrencyEnvVar+"=") {
			found = true
			v := strings.TrimPrefix(e, ConcurrencyEnvVar+"=")
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				t.Errorf("child budget %q is not a positive number", v)
			}
		}
	}
	if !found {
		t.Errorf("child env carries no %s", ConcurrencyEnvVar)
	}
}

// A parent already at the floor must still hand its child a usable budget.
func TestChildEnv_FloorIsUsable(t *testing.T) {
	t.Setenv(ConcurrencyEnvVar, "1")

	env := ChildEnv(ConcurrencyFromEnv())

	for _, e := range env {
		if strings.HasPrefix(e, ConcurrencyEnvVar+"=") {
			if got := strings.TrimPrefix(e, ConcurrencyEnvVar+"="); got != "1" {
				t.Errorf("child budget = %s, want 1", got)
			}
		}
	}
	// And that budget must build a working pool.
	t.Setenv(ConcurrencyEnvVar, "1")
	if p := NewPool(ConcurrencyFromEnv()); p.Size() < 1 {
		t.Errorf("pool size = %d, want at least 1", p.Size())
	}
}

// The orchestrator must actually use the configured budget.
func TestOrchestrator_UsesConfiguredConcurrency(t *testing.T) {
	t.Setenv(ConcurrencyEnvVar, "2")

	if got := ConcurrencyFromEnv(); got != 2 {
		t.Fatalf("ConcurrencyFromEnv() = %d, want 2", got)
	}
	if p := NewPool(ConcurrencyFromEnv()); p.Size() != 2 {
		t.Errorf("pool size = %d, want 2", p.Size())
	}
}

// The budget is per process and processes nest, so what matters is the product
// across depth, not any single level. This documents the resulting topology so
// a future change to the default or the halving rule has to confront it.
func TestConcurrencyTopology_TotalIsBounded(t *testing.T) {
	// leaves at depth d = product of the budget at each level above.
	total := func(root, depth int) int {
		n, budget := 1, root
		for range depth {
			n *= budget
			budget = childConcurrency(budget)
		}
		return n
	}

	// With the shipped default, a /run tree is: TUI -> coordinators -> workers.
	if got := total(DefaultPoolSize, 2); got > 8 {
		t.Errorf("depth-2 agents = %d with default %d; the observed peak of 8 "+
			"is what produced a 39%% rate-limit failure rate", got, DefaultPoolSize)
	}

	// Raising the knob must widen it, or the knob is useless.
	if total(8, 2) <= total(DefaultPoolSize, 2) {
		t.Error("raising PI_SUBAGENT_CONCURRENCY does not widen concurrency")
	}

	// And it must never diverge with depth: past the floor, each extra level
	// multiplies by 1.
	if total(DefaultPoolSize, 6) != total(DefaultPoolSize, 3) {
		t.Errorf("concurrency still growing at depth 6 (%d) vs depth 3 (%d)",
			total(DefaultPoolSize, 6), total(DefaultPoolSize, 3))
	}
}
