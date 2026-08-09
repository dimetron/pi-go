package agent

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// runawayPhrase is long enough to exceed outputProbeBytes, so a repeat of it
// is visible to the period search.
const runawayPhrase = "Let me look at how the TUI renders messages and whether hook output surfaces. "

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
	s := &StuckDetector{}
	stuck, detail := s.Observe("read", map[string]any{"path": "/foo"})
	if stuck {
		t.Errorf("expected not stuck, got stuck: %s", detail)
	}
}

func TestStuckDetector_Observe_SingleCall(t *testing.T) {
	s := &StuckDetector{}
	s.Observe("read", map[string]any{"path": "/foo"})
	if len(s.recent) != 1 {
		t.Errorf("recent len = %d, want 1", len(s.recent))
	}
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1", s.streak)
	}
}

func TestStuckDetector_Observe_IncrementStreak(t *testing.T) {
	s := &StuckDetector{}
	s.Observe("read", map[string]any{"path": "/foo"})
	s.Observe("read", map[string]any{"path": "/foo"})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2", s.streak)
	}
}

func TestStuckDetector_Observe_ResetStreak(t *testing.T) {
	s := &StuckDetector{}
	s.Observe("read", map[string]any{"path": "/foo"})
	s.Observe("read", map[string]any{"path": "/foo"})
	s.Observe("write", map[string]any{"path": "/bar"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1 after different call", s.streak)
	}
}

func TestStuckDetector_Observe_WindowOverflow(t *testing.T) {
	s := &StuckDetector{}
	// Add more than recentWindowSize entries
	for i := 0; i < 20; i++ {
		s.Observe("read", map[string]any{"path": "/foo"})
	}
	if len(s.recent) > recentWindowSize {
		t.Errorf("recent len = %d, should not exceed %d", len(s.recent), recentWindowSize)
	}
}

func TestStuckDetector_Observe_StreakThreshold(t *testing.T) {
	s := &StuckDetector{}
	// Trigger stuck detection
	for i := 0; i < MaxRepeatToolCalls; i++ {
		stuck, _ := s.Observe("read", map[string]any{"path": "/foo"})
		if i == MaxRepeatToolCalls-1 && !stuck {
			t.Errorf("expected stuck after %d repeats", MaxRepeatToolCalls)
		}
	}
}

// TestStuckDetector_ObserveResult_PollingNotStuck reproduces session
// 260808-1813: bash_output polled a running background command with identical
// args, but every response carried fresh progress (elapsed, streamed output).
// Changing results must reset the identical-call streak so productive polling
// outlives MaxRepeatToolCalls.

// TestStuckDetector_ObserveResult_PollingNotStuck reproduces session
// 260808-1813: bash_output polled a running background command with identical
// args, but every response carried fresh progress (elapsed, streamed output).
// Changing results must reset the identical-call streak so productive polling
// outlives MaxRepeatToolCalls.
func TestStuckDetector_ObserveResult_PollingNotStuck(t *testing.T) {
	s := &StuckDetector{}
	args := map[string]any{"handle": "bg_16", "wait_ms": 1000}
	for i := 0; i < MaxRepeatToolCalls*3; i++ {
		stuck, detail := s.Observe("bash_output", args)
		if stuck {
			t.Fatalf("poll %d flagged stuck: %s", i, detail)
		}
		s.ObserveResult("bash_output", map[string]any{
			"handle":  "bg_16",
			"elapsed": i, // progress: every response differs
			"running": true,
		})
	}
}

func TestStuckDetector_ObserveResult_IdenticalResultsStillStuck(t *testing.T) {
	s := &StuckDetector{}
	args := map[string]any{"handle": "bg_16"}
	resp := map[string]any{"running": false, "stdout": "done"}
	tripped := false
	for i := 0; i < MaxRepeatToolCalls; i++ {
		stuck, _ := s.Observe("bash_output", args)
		tripped = tripped || stuck
		s.ObserveResult("bash_output", resp)
	}
	if !tripped {
		t.Errorf("identical calls with identical results should still trip after %d repeats", MaxRepeatToolCalls)
	}
}

func TestStuckDetector_ObserveResult_OtherToolDoesNotReset(t *testing.T) {
	s := &StuckDetector{}
	s.Observe("read", map[string]any{"path": "/foo"})
	s.Observe("read", map[string]any{"path": "/foo"})
	s.ObserveResult("write", map[string]any{"changed": 1})
	s.ObserveResult("write", map[string]any{"changed": 2})
	if s.streak != 2 {
		t.Errorf("streak = %d, want 2: another tool's results must not reset it", s.streak)
	}
}

func TestStuckDetector_ObserveResult_FirstResultOnlySetsBaseline(t *testing.T) {
	s := &StuckDetector{}
	s.Observe("read", map[string]any{"path": "/foo"})
	s.ObserveResult("read", map[string]any{"content": "x"})
	if s.streak != 1 {
		t.Errorf("streak = %d, want 1: the first result has nothing to compare against", s.streak)
	}
}

func TestDetectCycle_ShortWindow(t *testing.T) {
	s := &StuckDetector{}
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
	s := &StuckDetector{}
	s.recent = []string{"a", "b", "c", "d", "e", "f"}
	if cycle := s.detectCycle(); cycle != "" {
		t.Errorf("detectCycle on non-repeating = %q, want empty", cycle)
	}
}

func TestDetectCycle_TwoRepeat(t *testing.T) {
	s := &StuckDetector{}
	// ABABAB pattern - 3 repetitions of length-2 cycle
	s.recent = []string{"a", "b", "a", "b", "a", "b"}
	cycle := s.detectCycle()
	if cycle == "" {
		t.Error("expected cycle detected for ABABAB pattern")
	}
}

func TestDetectCycle_ThreeRepeat(t *testing.T) {
	s := &StuckDetector{}
	// ABCABCABC pattern - 3 repetitions of length-3 cycle (need 9 elements)
	s.recent = []string{"a", "b", "c", "a", "b", "c", "a", "b", "c"}
	cycle := s.detectCycle()
	if cycle == "" {
		t.Error("expected cycle detected for ABCABCABC pattern")
	}
}

func TestDetectCycle_NoPatternAtBoundary(t *testing.T) {
	s := &StuckDetector{}
	// AAB pattern - not a valid cycle
	s.recent = []string{"a", "a", "b", "a", "a", "b"}
	cycle := s.detectCycle()
	if cycle != "" {
		t.Logf("cycle detected (may be valid): %s", cycle)
	}
}

func TestStuckDetector_ObserveOutput_EmptyChunkIsNoop(t *testing.T) {
	s := &StuckDetector{}
	if stuck, _ := s.ObserveOutput(""); stuck {
		t.Error("an empty chunk must not trip the detector")
	}
	if s.outBuf != "" || s.outSince != 0 {
		t.Errorf("an empty chunk must not change detector state: buf=%q since=%d", s.outBuf, s.outSince)
	}
}

func TestRepeatPeriod(t *testing.T) {
	if got := repeatPeriod("short"); got != 0 {
		t.Errorf("a buffer shorter than two probes has no period, got %d", got)
	}
	// The unit must not be periodic within itself, or the probe legitimately
	// matches at the shorter inner period instead.
	unit := "the model keeps restating this one sentence, verbatim. "
	if got := repeatPeriod(strings.Repeat(unit, 4)); got != len(unit) {
		t.Errorf("repeatPeriod = %d, want %d", got, len(unit))
	}
	if got := repeatPeriod(strings.Repeat("q", 200) + "no repetition of the tail whatsoever here!"); got != 0 {
		t.Errorf("a tail that never recurs has no period, got %d", got)
	}
}

func TestIsPeriodic(t *testing.T) {
	unit := "the same phrase again. "
	if !isPeriodic(strings.Repeat(unit, 12), len(unit), 12) {
		t.Error("12 back-to-back copies must read as periodic")
	}
	if isPeriodic(strings.Repeat(unit, 12)+"different tail", len(unit), 12) {
		t.Error("a broken tail must not read as periodic")
	}
	if isPeriodic(unit, len(unit), 12) {
		t.Error("a buffer shorter than period*repeats cannot be periodic")
	}
	if isPeriodic(strings.Repeat(unit, 12), 0, 12) {
		t.Error("a zero period must be rejected")
	}
}

func TestHasVariety(t *testing.T) {
	if hasVariety(strings.Repeat("-", 80)) {
		t.Error("a rule of dashes is filler, not a phrase")
	}
	if !hasVariety("a sentence with plenty of distinct letters") {
		t.Error("ordinary prose must count as a phrase")
	}
}

func TestStuckDetector_ObserveOutput_TripsOnRepeatedPhrase(t *testing.T) {
	s := &StuckDetector{}
	var stuck bool
	var detail string
	for range MaxOutputRepeats * 4 {
		if stuck, detail = s.ObserveOutput(runawayPhrase); stuck {
			break
		}
	}
	if !stuck {
		t.Fatal("expected the repeated phrase to trip the detector")
	}
	if !strings.Contains(detail, "repeated") {
		t.Errorf("detail = %q, want it to mention repetition", detail)
	}
}

func TestStuckDetector_ObserveOutput_AllowsProse(t *testing.T) {
	s := &StuckDetector{}
	// Distinct sentences, well past the byte volume that trips a repeat.
	for i := range 400 {
		text := strings.Repeat("x", i%37) + " unique sentence number " + strings.Repeat("y", i%29) + ". "
		if stuck, detail := s.ObserveOutput(text); stuck {
			t.Fatalf("non-repeating output tripped the detector at chunk %d: %s", i, detail)
		}
	}
}

func TestStuckDetector_ObserveOutput_IgnoresShortPeriods(t *testing.T) {
	// A horizontal rule is periodic with period 1; it must not count.
	s := &StuckDetector{}
	for range 40 {
		if stuck, _ := s.ObserveOutput(strings.Repeat("-", 80) + "\n"); stuck {
			t.Fatal("a run of dashes must not be treated as a repetition loop")
		}
	}
}

func TestToolFingerprint_IgnoresPagination(t *testing.T) {
	base := map[string]any{"file_path": "/repo/chat.go"}
	paged := map[string]any{"file_path": "/repo/chat.go", "offset": 230, "limit": 40}
	if toolFingerprint("read", base) != toolFingerprint("read", paged) {
		t.Error("pagination args must not change the fingerprint")
	}
	other := map[string]any{"file_path": "/repo/other.go", "offset": 230, "limit": 40}
	if toolFingerprint("read", paged) == toolFingerprint("read", other) {
		t.Error("a different file must change the fingerprint")
	}
}

func TestStuckDetector_Observe_TripsOnPagedRereads(t *testing.T) {
	s := &StuckDetector{}
	var stuck bool
	for i := range MaxRepeatToolCalls {
		stuck, _ = s.Observe("read", map[string]any{"file_path": "/repo/chat.go", "offset": i * 40})
	}
	if !stuck {
		t.Fatalf("re-reading one file %d times must trip the detector", MaxRepeatToolCalls)
	}
}

// runawayLLM streams one sentence over and over and never calls a tool,

// ObserveEvent is the entry point every front end now shares, so its arms are
// tested directly rather than only through one front end's loop.

func callEvent(name string, args map[string]any) *session.Event {
	return &session.Event{LLMResponse: model.LLMResponse{Content: &genai.Content{
		Role:  "model",
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: name, Args: args}}},
	}}}
}

func respEvent(name string, resp map[string]any) *session.Event {
	return &session.Event{LLMResponse: model.LLMResponse{Content: &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: name, Response: resp}}},
	}}}
}

func TestObserveEvent_NilAndEmptyAreNoops(t *testing.T) {
	var d StuckDetector
	if err := d.ObserveEvent(nil); err != nil {
		t.Errorf("nil event must be a no-op, got %v", err)
	}
	if err := d.ObserveEvent(&session.Event{}); err != nil {
		t.Errorf("event with no content must be a no-op, got %v", err)
	}
}

func TestObserveEvent_TripsOnRepeatedOutput(t *testing.T) {
	var d StuckDetector
	var err error
	for range MaxOutputRepeats * 4 {
		if err = d.ObserveEvent(textEvent("thinking", runawayPhrase, false)); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("a turn repeating one phrase must abort")
	}
	if !strings.Contains(err.Error(), "repeated") {
		t.Errorf("error should name the repetition, got %q", err)
	}
}

func TestObserveEvent_TripsOnIdenticalToolCalls(t *testing.T) {
	var d StuckDetector
	args := map[string]any{"path": "main.go"}
	var err error
	for range MaxRepeatToolCalls + 2 {
		if err = d.ObserveEvent(callEvent("read", args)); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("identical tool calls must abort")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should name the tool, got %q", err)
	}
}

func TestObserveEvent_TripsOnToolErrorStreak(t *testing.T) {
	var d StuckDetector
	var err error
	// Vary the args so the identical-call arm cannot be what fires.
	for i := range MaxToolErrorStreak + 2 {
		if err = d.ObserveEvent(callEvent("bash", map[string]any{"cmd": i})); err != nil {
			break
		}
		if err = d.ObserveEvent(respEvent("bash", map[string]any{"error": "boom"})); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("a tool failing repeatedly must abort")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error should describe the failure streak, got %q", err)
	}
}

// Polling is not a loop: a repeated call whose result changes is progress.
func TestObserveEvent_ProductivePollingSurvives(t *testing.T) {
	var d StuckDetector
	args := map[string]any{"handle": "h1"}
	for i := range MaxRepeatToolCalls * 3 {
		if err := d.ObserveEvent(callEvent("bash_output", args)); err != nil {
			t.Fatalf("productive polling aborted at %d: %v", i, err)
		}
		if err := d.ObserveEvent(respEvent("bash_output", map[string]any{"out": i})); err != nil {
			t.Fatalf("productive polling aborted on result at %d: %v", i, err)
		}
	}
}

// The tool-free-thinking arm: a turn that reasons at length and never acts.

// thinkingChunk returns a distinct ~360-byte block of model reasoning. Distinct
// matters: identical blocks would trip ObserveOutput's byte-exact arm first, and
// the test would silently stop exercising the arm it names.
func thinkingChunk(i int) string {
	return fmt.Sprintf("Pass %d: restating the goal in fresh words, %s, and still calling nothing.\n",
		i, strings.Repeat(fmt.Sprintf("clause-%d ", i), 30))
}

// bigThinkingEvent is one model event carrying that block.
func bigThinkingEvent(i int) *session.Event {
	return textEvent("thinking", thinkingChunk(i), true)
}

func TestObserveEvent_TripsOnToolFreeThinking(t *testing.T) {
	var d StuckDetector
	var err error
	seen := 0
	for i := range MaxThinkingEventStreak * 2 {
		seen = i + 1
		if err = d.ObserveEvent(bigThinkingEvent(i)); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatalf("%d tool-free thinking events must abort", seen)
	}
	if !strings.Contains(err.Error(), "no tool call") {
		t.Fatalf("a different arm fired: %q", err)
	}
	if seen < MaxThinkingEventStreak {
		t.Errorf("aborted after %d events, want at least %d", seen, MaxThinkingEventStreak)
	}
}

func TestObserveEvent_ThinkingBelowThresholdSurvives(t *testing.T) {
	var d StuckDetector
	for i := range MaxThinkingEventStreak - 1 {
		if err := d.ObserveEvent(bigThinkingEvent(i)); err != nil {
			t.Fatalf("aborted at event %d, below the threshold of %d: %v", i+1, MaxThinkingEventStreak, err)
		}
	}
}

// Volume alone is not enough either: a long streak of tiny chunks is how a
// token-level provider packetizes ordinary reasoning, not a stalled turn.
func TestObserveEvent_TinyThinkingChunksSurvive(t *testing.T) {
	var d StuckDetector
	total := 0
	for i := range MaxThinkingEventStreak * 20 {
		chunk := fmt.Sprintf("ok%d ", i)
		total += len(chunk)
		if err := d.ObserveEvent(textEvent("thinking", chunk, true)); err != nil {
			t.Fatalf("aborted at event %d on %d bytes: %v", i+1, total, err)
		}
	}
	if total >= MinThinkingStreakBytes {
		t.Fatalf("test fed %d bytes, at or above the %d-byte floor; it no longer proves anything",
			total, MinThinkingStreakBytes)
	}
}

// A turn that alternates reasoning with action never accumulates a streak, no
// matter how long it runs.
func TestObserveEvent_ToolCallResetsThinkingStreak(t *testing.T) {
	var d StuckDetector
	for i := range MaxThinkingEventStreak * 10 {
		if err := d.ObserveEvent(bigThinkingEvent(i)); err != nil {
			t.Fatalf("interleaved run aborted at thinking event %d: %v", i+1, err)
		}
		if i%(MaxThinkingEventStreak-1) != 0 {
			continue
		}
		// Distinct args, so neither the identical-call nor the cycle arm fires.
		if err := d.ObserveEvent(callEvent("read", map[string]any{"path": fmt.Sprintf("f%d.go", i)})); err != nil {
			t.Fatalf("interleaved run aborted on the tool call at %d: %v", i+1, err)
		}
	}
}

func TestObserveEvent_ToolResponseResetsThinkingStreak(t *testing.T) {
	var d StuckDetector
	for range 3 {
		for i := range MaxThinkingEventStreak - 1 {
			if err := d.ObserveEvent(bigThinkingEvent(i)); err != nil {
				t.Fatalf("aborted at thinking event %d: %v", i+1, err)
			}
		}
		if err := d.ObserveEvent(respEvent("read", map[string]any{"content": "ok"})); err != nil {
			t.Fatalf("aborted on the tool response: %v", err)
		}
	}
}

// Text from anyone but the model is a new turn, not a continuation of the streak.
func TestObserveEvent_UserTextResetsThinkingStreak(t *testing.T) {
	var d StuckDetector
	for range 3 {
		for i := range MaxThinkingEventStreak - 1 {
			if err := d.ObserveEvent(bigThinkingEvent(i)); err != nil {
				t.Fatalf("aborted at thinking event %d: %v", i+1, err)
			}
		}
		if err := d.ObserveEvent(textEvent("user", "actually, do this instead", false)); err != nil {
			t.Fatalf("aborted on the user turn: %v", err)
		}
	}
}

// An event that both reasons and acts is progress; it must not count as
// tool-free even though it carries text.
func TestObserveEvent_ThinkingWithCallInSameEventIsProgress(t *testing.T) {
	var d StuckDetector
	for i := range MaxThinkingEventStreak * 3 {
		ev := bigThinkingEvent(i)
		ev.Content.Parts = append(ev.Content.Parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{Name: "read", Args: map[string]any{"path": fmt.Sprintf("f%d.go", i)}},
		})
		if err := d.ObserveEvent(ev); err != nil {
			t.Fatalf("an event that reasons and acts aborted at %d: %v", i+1, err)
		}
	}
}

func TestObserveThinking_RequiresBothGates(t *testing.T) {
	// Count without volume.
	var byCount StuckDetector
	for range MaxThinkingEventStreak * 4 {
		if stuck, detail := byCount.ObserveThinking(1); stuck {
			t.Fatalf("one-byte events must not trip on count alone: %s", detail)
		}
	}
	// Volume without count.
	var byBytes StuckDetector
	if stuck, detail := byBytes.ObserveThinking(MinThinkingStreakBytes * 4); stuck {
		t.Fatalf("a single large block must not trip on volume alone: %s", detail)
	}
	// Both.
	var both StuckDetector
	var stuck bool
	var detail string
	for range MaxThinkingEventStreak {
		stuck, detail = both.ObserveThinking(MinThinkingStreakBytes/MaxThinkingEventStreak + 1)
	}
	if !stuck {
		t.Fatal("meeting both gates must trip")
	}
	if !strings.Contains(detail, "no tool call") {
		t.Errorf("detail = %q, want it to name the missing tool call", detail)
	}
}

func TestObserveThinking_ResetAndEmpty(t *testing.T) {
	var d StuckDetector
	for range MaxThinkingEventStreak - 1 {
		d.ObserveThinking(MinThinkingStreakBytes)
	}
	d.ResetThinking()
	if d.thinkEvents != 0 || d.thinkBytes != 0 {
		t.Fatalf("ResetThinking left events=%d bytes=%d, want both 0", d.thinkEvents, d.thinkBytes)
	}
	if stuck, _ := d.ObserveThinking(0); stuck {
		t.Error("an event with no text must not count")
	}
	if d.thinkEvents != 0 {
		t.Errorf("an event with no text advanced the streak to %d", d.thinkEvents)
	}
}
