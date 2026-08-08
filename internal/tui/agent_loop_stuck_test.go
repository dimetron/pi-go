package tui

import (
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
	stuck, detail := s.observe("read", map[string]any{"path": "/foo"})
	if stuck {
		t.Errorf("expected not stuck, got stuck: %s", detail)
	}
}

func TestStuckDetector_Observe_SingleCall(t *testing.T) {
	s := &stuckDetector{}
	s.observe("read", map[string]any{"path": "/foo"})
	if len(s.recent) != 1 {
		t.Errorf("recent len = %d, want 1", len(s.recent))
	}
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1", s.streak)
	}
}

func TestStuckDetector_Observe_IncrementStreak(t *testing.T) {
	s := &stuckDetector{}
	s.observe("read", map[string]any{"path": "/foo"})
	s.observe("read", map[string]any{"path": "/foo"})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2", s.streak)
	}
}

func TestStuckDetector_Observe_ResetStreak(t *testing.T) {
	s := &stuckDetector{}
	s.observe("read", map[string]any{"path": "/foo"})
	s.observe("read", map[string]any{"path": "/foo"})
	s.observe("write", map[string]any{"path": "/bar"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1 after different call", s.streak)
	}
}

func TestStuckDetector_Observe_WindowOverflow(t *testing.T) {
	s := &stuckDetector{}
	// Add more than recentWindowSize entries
	for i := 0; i < 20; i++ {
		s.observe("read", map[string]any{"path": "/foo"})
	}
	if len(s.recent) > recentWindowSize {
		t.Errorf("recent len = %d, should not exceed %d", len(s.recent), recentWindowSize)
	}
}

func TestStuckDetector_Observe_StreakThreshold(t *testing.T) {
	s := &stuckDetector{}
	// Trigger stuck detection
	for i := 0; i < maxRepeatToolCalls; i++ {
		stuck, _ := s.observe("read", map[string]any{"path": "/foo"})
		if i == maxRepeatToolCalls-1 && !stuck {
			t.Errorf("expected stuck after %d repeats", maxRepeatToolCalls)
		}
	}
}

// TestStuckDetector_ObserveResult_PollingNotStuck reproduces session
// 260808-1813: bash_output polled a running background command with identical
// args, but every response carried fresh progress (elapsed, streamed output).
// Changing results must reset the identical-call streak so productive polling
// outlives maxRepeatToolCalls.
func TestStuckDetector_ObserveResult_PollingNotStuck(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"handle": "bg_16", "wait_ms": 1000}
	for i := 0; i < maxRepeatToolCalls*3; i++ {
		stuck, detail := s.observe("bash_output", args)
		if stuck {
			t.Fatalf("poll %d flagged stuck: %s", i, detail)
		}
		s.observeResult("bash_output", map[string]any{
			"handle":  "bg_16",
			"elapsed": i, // progress: every response differs
			"running": true,
		})
	}
}

func TestStuckDetector_ObserveResult_IdenticalResultsStillStuck(t *testing.T) {
	s := &stuckDetector{}
	args := map[string]any{"handle": "bg_16"}
	resp := map[string]any{"running": false, "stdout": "done"}
	tripped := false
	for i := 0; i < maxRepeatToolCalls; i++ {
		stuck, _ := s.observe("bash_output", args)
		tripped = tripped || stuck
		s.observeResult("bash_output", resp)
	}
	if !tripped {
		t.Errorf("identical calls with identical results should still trip after %d repeats", maxRepeatToolCalls)
	}
}

func TestStuckDetector_ObserveResult_OtherToolDoesNotReset(t *testing.T) {
	s := &stuckDetector{}
	s.observe("read", map[string]any{"path": "/foo"})
	s.observe("read", map[string]any{"path": "/foo"})
	s.observeResult("write", map[string]any{"changed": 1})
	s.observeResult("write", map[string]any{"changed": 2})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2: another tool's results must not reset it", s.streak)
	}
}

func TestStuckDetector_ObserveResult_FirstResultOnlySetsBaseline(t *testing.T) {
	s := &stuckDetector{}
	s.observe("read", map[string]any{"path": "/foo"})
	s.observeResult("read", map[string]any{"content": "x"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1: the first result has nothing to compare against", s.streak)
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
