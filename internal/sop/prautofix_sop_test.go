package sop

import "testing"

func TestPRAutofixSOPLints(t *testing.T) {
	def, err := LoadEmbeddedDefinition("pr-autofix")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if def.SOP != "pr-autofix" {
		t.Fatalf("sop name = %q", def.SOP)
	}
	if f := LintDefinition(def); !f.OK() {
		t.Fatalf("lint findings:\n%s", f.Format())
	}
	if _, err := Compile(def, DescribeFactory{}); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

// TestPRAutofixPushRoutesToWatch pins that a successful push loops back to
// watch — the run re-polls CI after pushing, rather than ending. The push
// stage uses routes PASS→watch (not loop_back, which needs RecheckSignal — a
// route a shell stage cannot emit) and max_cycles bounds the loop.
func TestPRAutofixPushRoutesToWatch(t *testing.T) {
	def, err := LoadEmbeddedDefinition("pr-autofix")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	push, ok := def.Stage("push")
	if !ok {
		t.Fatal("push stage not found")
	}
	if push.LoopBack != "" {
		t.Errorf("push.loop_back = %q, want empty — loop_back needs RecheckSignal, which a shell stage cannot emit", push.LoopBack)
	}
	if got := push.Routes["PASS"]; got != "watch" {
		t.Errorf("push.routes[PASS] = %q, want %q", got, "watch")
	}
	if push.MaxCycle != 5 {
		t.Errorf("push.max_cycles = %d, want 5", push.MaxCycle)
	}
}
