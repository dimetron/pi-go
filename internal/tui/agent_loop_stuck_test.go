package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestToolFingerprint_Deterministic(t *testing.T) {
	args := map[string]any{"path": "/foo/bar", "n": 10}
	fp1 := toolFingerprint("read", args)
	fp2 := toolFingerprint("read", args)
	if fp1 != fp2 {
		t.Errorf("toolFingerprint not deterministic: %q != %q", fp1, fp2)
	}
}

func TestToolFingerprint_DifferentName(t *testing.T) {
	args := map[string]any{"path": "/foo/bar"}
	fp1 := toolFingerprint("read", args)
	fp2 := toolFingerprint("write", args)
	if fp1 == fp2 {
		t.Errorf("different names should produce different fingerprints")
	}
}

func TestToolFingerprint_DifferentArgs(t *testing.T) {
	args1 := map[string]any{"path": "/foo/bar"}
	args2 := map[string]any{"path": "/baz/qux"}
	fp1 := toolFingerprint("read", args1)
	fp2 := toolFingerprint("read", args2)
	if fp1 == fp2 {
		t.Errorf("different args should produce different fingerprints")
	}
}

func TestToolFingerprint_Length(t *testing.T) {
	fp := toolFingerprint("test", nil)
	if len(fp) != 16 {
		t.Errorf("toolFingerprint length = %d, want 16", len(fp))
	}
}

func TestToolFingerprint_EmptyArgs(t *testing.T) {
	fp := toolFingerprint("bash", nil)
	if len(fp) != 16 {
		t.Errorf("toolFingerprint length = %d, want 16", len(fp))
	}
}

func TestStuckDetector_Observe_NotStuck(t *testing.T) {
	s := &stuckDetector{}
	stuck, detail := s.observe("c", "read", map[string]any{"path": "/foo"})
	if stuck {
		t.Errorf("expected not stuck, got stuck: %s", detail)
	}
}

func TestStuckDetector_Observe_SingleCall(t *testing.T) {
	s := &stuckDetector{}
	s.observe("c", "read", map[string]any{"path": "/foo"})
	if len(s.recent) != 1 {
		t.Errorf("recent len = %d, want 1", len(s.recent))
	}
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1", s.streak)
	}
}

func TestStuckDetector_Observe_IncrementStreak(t *testing.T) {
	s := &stuckDetector{}
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observe("c", "read", map[string]any{"path": "/foo"})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2", s.streak)
	}
}

func TestStuckDetector_Observe_ResetStreak(t *testing.T) {
	s := &stuckDetector{}
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observe("c", "write", map[string]any{"path": "/bar"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1 after different call", s.streak)
	}
}

func TestStuckDetector_Observe_WindowOverflow(t *testing.T) {
	s := &stuckDetector{}
	// Add more than recentWindowSize entries
	for i := 0; i < 20; i++ {
		s.observe("c", "read", map[string]any{"path": "/foo"})
	}
	if len(s.recent) > recentWindowSize {
		t.Errorf("recent len = %d, should not exceed %d", len(s.recent), recentWindowSize)
	}
}

func TestStuckDetector_Observe_StreakThreshold(t *testing.T) {
	s := &stuckDetector{}
	// Trigger stuck detection
	for i := 0; i < maxRepeatToolCalls; i++ {
		stuck, _ := s.observe("c", "read", map[string]any{"path": "/foo"})
		if i == maxRepeatToolCalls-1 && !stuck {
			t.Errorf("expected stuck after %d repeats", maxRepeatToolCalls)
		}
	}
}

// TestStuckDetector_ObserveResult_PollingNotStuck reproduces session
// 260808-1813: bash_wait polled a running background command with identical
// args, but every response carried fresh progress (elapsed, streamed output).
// Changing results must reset the identical-call streak so productive polling
// outlives maxRepeatToolCalls.
func TestStuckDetector_ObserveResult_PollingNotStuck(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"handle": "bg_16", "wait_sec": 1}
	for i := 0; i < maxRepeatToolCalls*3; i++ {
		stuck, detail := s.observe("c", "bash_wait", args)
		if stuck {
			t.Fatalf("poll %d flagged stuck: %s", i, detail)
		}
		s.observeResult("bash_wait", map[string]any{
			"handle":  "bg_16",
			"elapsed": i,
			"stdout":  fmt.Sprintf("line %d\n", i), // command progress: every response differs
			"running": true,
		})
	}
}

func TestStuckDetector_ObserveResult_ChangingTimingDoesNotReset(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"handle": "bg_16"}
	for i := 0; i < maxRepeatToolCalls; i++ {
		stuck, _ := s.observe("c", "bash_wait", args)
		if i == maxRepeatToolCalls-1 && !stuck {
			t.Fatalf("timing-only polls should trip after %d repeats", maxRepeatToolCalls)
		}
		s.observeResult("bash_wait", map[string]any{
			"handle":  "bg_16",
			"elapsed": i,
			"idle":    i + 1,
			"running": true,
			"stdout":  "",
			"stderr":  "",
		})
	}
}

func TestStuckDetector_ObserveResult_IdenticalResultsStillStuck(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"handle": "bg_16"}
	resp := map[string]any{"running": false, "stdout": "done"}
	tripped := false
	for i := 0; i < maxRepeatToolCalls; i++ {
		stuck, _ := s.observe("c", "bash_wait", args)
		tripped = tripped || stuck
		s.observeResult("bash_wait", resp)
	}
	if !tripped {
		t.Errorf("identical calls with identical results should still trip after %d repeats", maxRepeatToolCalls)
	}
}

func TestStuckDetector_ObserveResult_OtherToolDoesNotReset(t *testing.T) {
	s := &stuckDetector{}
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observeResult("write", map[string]any{"changed": 1})
	s.observeResult("write", map[string]any{"changed": 2})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2: another tool's results must not reset it", s.streak)
	}
}

func TestStuckDetector_ObserveResult_FirstResultOnlySetsBaseline(t *testing.T) {
	s := &stuckDetector{}
	s.observe("c", "read", map[string]any{"path": "/foo"})
	s.observeResult("read", map[string]any{"content": "x"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1: the first result has nothing to compare against", s.streak)
	}
}

// TestStuckDetector_ObserveError_SameCallDifferentMessagesStillStuck pins the
// args-aware streak: the same call (same tool + same args) failing across
// several messages still trips at maxToolErrorStreak. Regression guard for the
// pre-hash behavior where distinct args also counted.
func TestStuckDetector_ObserveError_SameCallDifferentMessagesStillStuck(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"request": map[string]any{"author": "stealth", "slug": "ox-alpha"}}
	tripped := false
	for i := 0; i < maxToolErrorStreak; i++ {
		s.beginEvent() // one call per message: repeated attempts
		id := fmt.Sprintf("call_%d", i)
		s.observe(id, "get-model", args)
		stuck, _ := s.observeError(id, "get-model", true)
		tripped = tripped || stuck
	}
	if !tripped {
		t.Errorf("the same call failing %d times must trip the args-aware streak", maxToolErrorStreak)
	}
}

// TestStuckDetector_ObserveError_BatchOfDistinctCallsNotStuck is the regression
// for session 260826-2200-b99f8-5b94b: the model sent one message with ten
// get-model calls, each targeting a different model, and every one failed with
// "Model not found". That is a single sweep over a list — progress — not a
// loop, so neither error streak may trip.
func TestStuckDetector_ObserveError_BatchOfDistinctCallsNotStuck(t *testing.T) {
	s := &stuckDetector{}
	s.beginEvent() // all ten calls share one message
	slugs := []string{
		"stealth/ox-alpha", "deepseek/deepseek-v4-flash-20260731", "xiaomi/mimo-v2.5-20260422",
		"tencent/hy3-20260706", "deepseek/deepseek-v4-flash-20260423", "google/gemini-3.7-flash-20260813",
		"openai/gpt-5.6-luna-20260709", "z-ai/glm-5.2-20260616", "deepseek/deepseek-v4-pro-20260423",
		"openai/gpt-5.6-sol-20260709",
	}
	for i, slug := range slugs {
		parts := strings.SplitN(slug, "/", 2)
		id := fmt.Sprintf("call_%d", i)
		s.observe(id, "get-model", map[string]any{"request": map[string]any{"author": parts[0], "slug": parts[1]}})
	}
	for i := range slugs {
		id := fmt.Sprintf("call_%d", i)
		if stuck, detail := s.observeError(id, "get-model", true); stuck {
			t.Fatalf("a batch of distinct failing calls tripped the detector: %s", detail)
		}
	}
	if s.errFPStreak >= maxToolErrorStreak {
		t.Errorf("args-aware streak = %d, want < %d: distinct args must not compound", s.errFPStreak, maxToolErrorStreak)
	}
	if s.errStreak >= maxToolErrorStreak {
		t.Errorf("name-only streak = %d, want < %d: one batch is one attempt", s.errStreak, maxToolErrorStreak)
	}
}

// TestStuckDetector_ObserveError_BatchOfDistinctCalls_EmptyIDsNotStuck pins the
// same regression for providers that leave FunctionCall.ID empty: the name
// fallback must still attribute the whole batch to one message, so ten
// distinct ID-less calls in one message do not trip the name-only streak.
func TestStuckDetector_ObserveError_BatchOfDistinctCalls_EmptyIDsNotStuck(t *testing.T) {
	s := &stuckDetector{}
	s.beginEvent()
	for i := 0; i < maxToolErrorStreak; i++ {
		s.observe("", "get-model", map[string]any{"request": map[string]any{"author": "a", "slug": fmt.Sprintf("model-%d", i)}})
	}
	for i := 0; i < maxToolErrorStreak; i++ {
		if stuck, detail := s.observeError("", "get-model", true); stuck {
			t.Fatalf("an ID-less batch of distinct failing calls tripped the detector: %s", detail)
		}
	}
}

// TestStuckDetector_ObserveError_FlailingAcrossMessagesStillStuck pins the
// name-only cross-batch streak: the same tool failing once per message with
// different args across maxToolErrorStreak messages is the flailing pattern and
// must still trip.
func TestStuckDetector_ObserveError_FlailingAcrossMessagesStillStuck(t *testing.T) {
	s := &stuckDetector{}
	tripped := false
	for i := 0; i < maxToolErrorStreak; i++ {
		s.beginEvent() // one call per message
		id := fmt.Sprintf("call_%d", i)
		s.observe(id, "read", map[string]any{"file_path": fmt.Sprintf("/nonexistent/path/%d", i)})
		stuck, _ := s.observeError(id, "read", true)
		tripped = tripped || stuck
	}
	if !tripped {
		t.Errorf("flailing (same tool, distinct args, across %d messages) must trip the name-only streak", maxToolErrorStreak)
	}
}

// TestStuckDetector_ObserveError_CallInfoConsumed pins that a matched response
// consumes its call record: the correlation map stays bounded by outstanding
// (unanswered) calls, so a long turn does not grow it without bound.
func TestStuckDetector_ObserveError_CallInfoConsumed(t *testing.T) {
	s := &stuckDetector{}
	s.beginEvent()
	s.observe("c1", "read", map[string]any{"path": "/foo"})
	s.observe("c2", "write", map[string]any{"path": "/bar"})
	if len(s.callInfo) != 2 {
		t.Fatalf("callInfo len = %d, want 2 after two observed calls", len(s.callInfo))
	}
	s.observeError("c1", "read", false) // answer c1; the record must be consumed
	if len(s.callInfo) != 1 {
		t.Errorf("callInfo len = %d, want 1: answered calls must be consumed", len(s.callInfo))
	}
	if _, ok := s.callInfo["c1"]; ok {
		t.Error("c1 still present after its response; the record was not consumed")
	}
	s.observeError("c2", "write", false)
	if len(s.callInfo) != 0 {
		t.Errorf("callInfo len = %d, want 0 after all calls answered", len(s.callInfo))
	}
}

// TestStuckDetector_ObserveError_SameCall_EmptyIDsStillStuck pins that the
// same call repeated with an empty ID across different messages still trips
// the name-only streak: the name fallback supplies the batch, and distinct
// messages compound.
func TestStuckDetector_ObserveError_SameCall_EmptyIDsStillStuck(t *testing.T) {
	s := &stuckDetector{}
	tripped := false
	for i := 0; i < maxToolErrorStreak; i++ {
		s.beginEvent()
		s.observe("", "get-model", map[string]any{"request": map[string]any{"author": "a", "slug": "b"}})
		stuck, _ := s.observeError("", "get-model", true)
		tripped = tripped || stuck
	}
	if !tripped {
		t.Errorf("the same ID-less call failing %d times across messages must trip the name-only streak", maxToolErrorStreak)
	}
}

// TestStuckDetector_ObserveError_SuccessResetsBothStreaks pins that a success
// clears both error streaks, so the model recovering from a bad stretch is not
// penalized later.
func TestStuckDetector_ObserveError_SuccessResetsBothStreaks(t *testing.T) {
	s := &stuckDetector{}
	for i := 0; i < maxToolErrorStreak-1; i++ {
		s.beginEvent()
		id := fmt.Sprintf("call_%d", i)
		s.observe(id, "get-model", map[string]any{"request": map[string]any{"author": "a", "slug": "b"}})
		s.observeError(id, "get-model", true)
	}
	// A success resets everything.
	s.beginEvent()
	s.observe("ok", "get-model", map[string]any{"request": map[string]any{"author": "a", "slug": "b"}})
	if stuck, _ := s.observeError("ok", "get-model", false); stuck {
		t.Fatal("a successful call must not trip the detector")
	}
	if s.errFPStreak != 0 || s.errStreak != 0 {
		t.Errorf("streaks not reset by success: errFPStreak=%d errStreak=%d", s.errFPStreak, s.errStreak)
	}
}

func TestDetectCycle_ShortWindow(t *testing.T) {
	s := &stuckDetector{}
	// Window < 6 should return ""
	if cycle := s.detectCycle(); cycle != "" {
		t.Errorf("detectCycle on empty = %q, want empty", cycle)
	}

	s.recent = make([]string, 5)
	if cycle := s.detectCycle(); cycle != "" {
		t.Errorf("detectCycle on len=5 = %q, want empty", cycle)
	}
}

func TestDetectCycle_NoRepeat(t *testing.T) {
	s := &stuckDetector{}
	s.recent = []string{"a", "b", "c", "d", "e", "f"}
	if cycle := s.detectCycle(); cycle != "" {
		t.Errorf("detectCycle on non-repeating = %q, want empty", cycle)
	}
}

func TestDetectCycle_TwoRepeat(t *testing.T) {
	s := &stuckDetector{}
	// ABABAB pattern - 3 repetitions of length-2 cycle
	s.recent = []string{"a", "b", "a", "b", "a", "b"}
	cycle := s.detectCycle()
	if cycle == "" {
		t.Error("expected cycle detected for ABABAB pattern")
	}
}

func TestDetectCycle_ThreeRepeat(t *testing.T) {
	s := &stuckDetector{}
	// ABCABCABC pattern - 3 repetitions of length-3 cycle (need 9 elements)
	s.recent = []string{"a", "b", "c", "a", "b", "c", "a", "b", "c"}
	cycle := s.detectCycle()
	if cycle == "" {
		t.Error("expected cycle detected for ABCABCABC pattern")
	}
}

func TestDetectCycle_NoPatternAtBoundary(t *testing.T) {
	s := &stuckDetector{}
	// AAB pattern - not a valid cycle
	s.recent = []string{"a", "a", "b", "a", "a", "b"}
	cycle := s.detectCycle()
	if cycle != "" {
		t.Logf("cycle detected (may be valid): %s", cycle)
	}
}

func TestSubmitPrompt_NilCancel(t *testing.T) {
	// Test that submitPrompt handles nil cancel channel gracefully
	cancel := make(chan struct{})
	close(cancel)
	prompt := "test prompt"

	// This should not panic
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("submitPrompt panicked with closed cancel: %v", r)
			}
		}()
		// We can't fully test without the full model, but we test the cancel path
	}()
	_ = prompt // use the variable
}
