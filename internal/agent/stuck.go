package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/session"
)

// StuckDetector recognizes an agent turn that has stopped making progress:
// identical tool calls, a tool failing over and over, the model's own output
// collapsing into repetition, or the model reasoning at length without ever
// acting. It lives here rather than in the TUI so every front end --
// interactive, print, json, rpc and ACP -- shares one guard. A front end that
// skips it runs unprotected.
//
// The zero value is ready to use. Not safe for concurrent use; one value tracks
// one run.

const (
	// MaxRepeatToolCalls is the number of identical consecutive tool calls
	// before the loop is considered stuck and aborted. A repeated call whose
	// result changes resets the count (see ObserveResult): polling tools like
	// bash_output repeat identical args by design, and only identical results
	// make the repetition meaningless.
	MaxRepeatToolCalls = 10

	// MaxRepeatErrorCalls aliases MaxRepeatToolCalls for callers that frame
	// the threshold as an error-streak rather than a call-streak. The
	// underlying detector is identical — identical fingerprint = stuck.
	MaxRepeatErrorCalls = MaxRepeatToolCalls

	// MaxToolErrorStreak is the number of consecutive failures of the same
	// tool name (regardless of args) before the loop is aborted. Catches the
	// "flailing" pattern where the model tries a different argument each
	// turn but the call still fails.
	MaxToolErrorStreak = 10

	// recentWindowSize is the sliding window of tool-call fingerprints kept
	// for repetition detection.
	recentWindowSize = 12

	// MaxOutputRepeats is the number of back-to-back copies of one phrase in
	// the model's own output before the turn is called degenerate.
	MaxOutputRepeats = 12

	// outputWindowBytes is the rolling tail of streamed output kept for
	// repetition detection. It has to hold MaxOutputRepeats copies of the
	// longest phrase worth catching (~680 bytes).
	outputWindowBytes = 8192

	// outputProbeBytes is the suffix matched against earlier output to find
	// the length of the phrase being repeated.
	outputProbeBytes = 48

	// minOutputPeriod is the shortest phrase treated as a repetition unit.
	// Below this, ordinary output (indentation, ASCII art) is periodic often
	// enough to matter.
	minOutputPeriod = 16

	// minPeriodVariety is how many distinct bytes the repeating phrase must
	// contain. A rule of dashes or a run of spaces is perfectly periodic and
	// perfectly harmless; a sentence the model cannot stop restating is not.
	minPeriodVariety = 8

	// outputCheckEvery is how much new output accumulates between scans.
	// Scanning per token would put a linear search in the streaming path.
	outputCheckEvery = 512

	// MaxThinkingEventStreak is how many consecutive model events may carry
	// thinking or reply text without a single tool call before the turn is
	// called degenerate. It is only half the test: MinThinkingStreakBytes
	// must be met as well (see ObserveThinking).
	//
	// The number comes from measured runs. The longest tool-free reasoning
	// burst seen in a *healthy* run was 45 events / 13 KB (minimax-m3, in a
	// session that went on to make 37 tool calls); claude-sonnet's longest was
	// 7. Degenerate runs measured 21, 34, 38, 57, 68, 78, 87, 89, 122 and 164
	// events, spanning 10 KB to 148 KB. The two populations overlap at the low
	// end, so no threshold separates them cleanly and the choice is which
	// error to make. A false abort kills legitimate deep reasoning outright,
	// while a missed loop is still covered by the other three arms — so this
	// arm is deliberately biased towards missing.
	//
	// 50 sits above every healthy run observed and catches 7 of the 10
	// degenerate ones, including the 57-event / 23 KB case that motivated this
	// arm and tripped nothing else (its output was semantically repetitive but
	// never byte-exact, so ObserveOutput saw reps=0). The three shortest
	// degenerate runs are below the healthy ceiling and are conceded.
	MaxThinkingEventStreak = 50

	// MinThinkingStreakBytes is how much tool-free text must accompany that
	// streak. Event *count* alone is not a safe signal: providers chunk the
	// stream differently, and measured events range from 1 byte to ~2.9 KB, so
	// a token-level stream reaches 50 events in a sentence. Requiring volume
	// as well makes the arm depend on how much the model actually said rather
	// than on how its provider happens to packetize.
	//
	// 16 KiB clears the 13 KB of the largest healthy burst with room to spare,
	// so a legitimate reasoning burst has to beat the observed ceiling on
	// *both* axes at once to trip — a far stronger bar than either alone.
	MinThinkingStreakBytes = 16384
)

// StuckDetector tracks recent tool calls and detects repetition loops.
type StuckDetector struct {
	recent      []string // ring of fingerprints (len <= recentWindowSize)
	lastPrint   string   // fingerprint of last tool call
	lastName    string   // tool name behind lastPrint
	lastResult  string   // fingerprint of the streak's previous result ("" = none yet)
	streak      int      // consecutive identical tool calls with identical results
	lastErrTool string   // name of last tool that errored
	errStreak   int      // consecutive errors for that tool
	outBuf      string   // rolling tail of the model's own output
	outSince    int      // bytes of output since the last repetition scan
	thinkEvents int      // consecutive model events with text and no tool call
	thinkBytes  int      // text bytes accumulated over that streak
}

// volatileToolArgs address a slice of a target rather than the target itself.
// A model paging through one file — read(x, offset 1), read(x, offset 230),
// read(x, offset 240) — is repeating itself, but hashing the raw args makes
// every one of those calls unique and hides the loop from the detector.
var volatileToolArgs = map[string]bool{
	"offset":     true,
	"limit":      true,
	"head_limit": true,
	"start_line": true,
	"end_line":   true,
}

// toolFingerprint produces a short hash of a tool call for comparison.
// Pagination arguments are dropped first, so re-reading one file region by
// region collapses to a single fingerprint. json.Marshal sorts map keys, so
// the hash does not depend on argument order.
func toolFingerprint(name string, args map[string]any) string {
	h := sha256.New()
	h.Write([]byte(name))
	b, _ := json.Marshal(stableToolArgs(args))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// stableToolArgs returns args without the volatile keys. The input map belongs
// to the event being streamed, so it is copied rather than filtered in place.
func stableToolArgs(args map[string]any) map[string]any {
	stable := make(map[string]any, len(args))
	for k, v := range args {
		if volatileToolArgs[k] {
			continue
		}
		stable[k] = v
	}
	return stable
}

// Observe records a tool call and returns true if the loop appears stuck.
func (s *StuckDetector) Observe(name string, args map[string]any) (stuck bool, detail string) {
	fp := toolFingerprint(name, args)

	// Consecutive identical call detection.
	if fp == s.lastPrint {
		s.streak++
	} else {
		s.streak = 1
		s.lastPrint = fp
		s.lastName = name
		s.lastResult = ""
	}

	// Sliding window.
	s.recent = append(s.recent, fp)
	if len(s.recent) > recentWindowSize {
		s.recent = s.recent[1:]
	}

	if s.streak >= MaxRepeatToolCalls {
		return true, fmt.Sprintf("identical tool call %q repeated %d times", name, s.streak)
	}

	// Detect short repeating cycles (AB AB AB) in the window.
	if cycle := s.detectCycle(); cycle != "" {
		return true, fmt.Sprintf("repeating tool cycle detected: %s", cycle)
	}

	return false, ""
}

// ObserveResult records a tool call's response. Polling tools repeat identical
// calls by design — bash_output on a running command sends the same handle
// every time and gets fresh output back — so a response that differs from the
// streak's previous response is progress, and resets the identical-call
// streak. A response identical to the last one keeps the streak counting:
// re-polling a finished command or re-reading an unchanged file is genuine
// repetition. A stuck loop whose responses vary trivially (timestamps) escapes
// this detector, but ObserveError and ObserveOutput still cover that shape.
func (s *StuckDetector) ObserveResult(name string, response map[string]any) {
	if name != s.lastName {
		return
	}
	b, _ := json.Marshal(response)
	sum := sha256.Sum256(b)
	fp := hex.EncodeToString(sum[:])[:16]
	if s.lastResult != "" && fp != s.lastResult {
		s.streak = 0
	}
	s.lastResult = fp
}

// ObserveError records the outcome of a tool call by name. Consecutive errors
// of the same tool name — regardless of args — trip the detector once the
// streak reaches MaxToolErrorStreak. A success (isError == false) or a switch
// to a different tool name resets the streak.
func (s *StuckDetector) ObserveError(name string, isError bool) (stuck bool, detail string) {
	if isError && name == s.lastErrTool {
		s.errStreak++
	} else {
		s.errStreak = 1
		s.lastErrTool = name
	}
	if s.errStreak >= MaxToolErrorStreak {
		return true, fmt.Sprintf("tool %q failed %d times in a row", name, s.errStreak)
	}
	return false, ""
}

// ObserveOutput records a chunk of the model's own output — reply text or
// thinking — and reports whether the turn has collapsed into repetition.
//
// The detectors above watch tool calls, so a turn that makes no calls at all is
// invisible to them. That is exactly the shape of a degenerate turn: one
// sentence restated until the output cap is hit, with nothing else emitted.
// This scans the tail of the stream for a repeating period instead.
func (s *StuckDetector) ObserveOutput(text string) (stuck bool, detail string) {
	if text == "" {
		return false, ""
	}
	s.outBuf += text
	if len(s.outBuf) > outputWindowBytes {
		s.outBuf = s.outBuf[len(s.outBuf)-outputWindowBytes:]
	}

	s.outSince += len(text)
	if s.outSince < outputCheckEvery {
		return false, ""
	}
	s.outSince = 0

	period := repeatPeriod(s.outBuf)
	if period < minOutputPeriod || !isPeriodic(s.outBuf, period, MaxOutputRepeats) {
		return false, ""
	}
	if !hasVariety(s.outBuf[len(s.outBuf)-period:]) {
		return false, ""
	}
	return true, fmt.Sprintf("model repeated a %d-character phrase %d times", period, MaxOutputRepeats)
}

// ObserveThinking records one model event that carried textBytes of thinking or
// reply text and made no tool call, and reports whether the turn has spent too
// long reasoning without ever acting.
//
// ObserveOutput above already watches for a turn that makes no tool calls, but
// only catches *byte-exact* repetition. A model can loop semantically instead —
// restating the same intent in fresh words on every pass, never calling a tool,
// never making progress. Repetition penalties make that shape more likely, not
// less, because they suppress the very signature ObserveOutput keys on. This
// arm ignores what the text says and watches only for the absence of action.
//
// Both MaxThinkingEventStreak and MinThinkingStreakBytes must be exceeded; see
// their comments for why one alone is not a safe signal.
func (s *StuckDetector) ObserveThinking(textBytes int) (stuck bool, detail string) {
	if textBytes <= 0 {
		return false, ""
	}
	s.thinkEvents++
	s.thinkBytes += textBytes
	if s.thinkEvents < MaxThinkingEventStreak || s.thinkBytes < MinThinkingStreakBytes {
		return false, ""
	}
	return true, fmt.Sprintf("model produced %d thinking events (%d bytes) with no tool call",
		s.thinkEvents, s.thinkBytes)
}

// ResetThinking clears the tool-free streak. A tool call is progress by
// definition, and so is its response; a turn that alternates reasoning with
// action never accumulates a streak no matter how long it runs. Text authored
// by anyone but the model resets it too, because that begins a fresh turn.
func (s *StuckDetector) ResetThinking() {
	s.thinkEvents = 0
	s.thinkBytes = 0
}

// repeatPeriod returns the distance between the tail of buf and the previous
// occurrence of that same tail — the length of the phrase the model may be
// cycling on — or 0 when the tail does not recur.
func repeatPeriod(buf string) int {
	if len(buf) < outputProbeBytes*2 {
		return 0
	}
	probe := buf[len(buf)-outputProbeBytes:]
	prev := strings.LastIndex(buf[:len(buf)-outputProbeBytes], probe)
	if prev < 0 {
		return 0
	}
	return len(buf) - outputProbeBytes - prev
}

// hasVariety reports whether unit contains enough distinct bytes to be a
// phrase rather than filler.
func hasVariety(unit string) bool {
	var seen [256]bool
	distinct := 0
	for i := range len(unit) {
		if seen[unit[i]] {
			continue
		}
		seen[unit[i]] = true
		distinct++
		if distinct >= minPeriodVariety {
			return true
		}
	}
	return false
}

// isPeriodic reports whether the last period*repeats bytes of buf are one
// period-long phrase repeated back to back. Comparing bytes is safe for UTF-8
// here: a byte-exact repeat is a rune-exact repeat.
func isPeriodic(buf string, period, repeats int) bool {
	span := period * repeats
	if period <= 0 || span > len(buf) {
		return false
	}
	tail := buf[len(buf)-span:]
	for i := period; i < len(tail); i++ {
		if tail[i] != tail[i-period] {
			return false
		}
	}
	return true
}

// detectCycle checks the recent window for repeating subsequences.
// Returns a description if found, empty string otherwise.
//
// A "cycle" requires that consecutive elements differ — a uniform window
// like [a,a,a,a,a,a] is a streak, not a cycle, and the identical-call
// detector above already handles that case at MaxRepeatToolCalls.
func (s *StuckDetector) detectCycle() string {
	n := len(s.recent)
	if n < 6 {
		return ""
	}
	// Check cycle lengths 2 and 3.
	for cycleLen := 2; cycleLen <= 3; cycleLen++ {
		need := cycleLen * 3 // require 3 full repetitions
		if n < need {
			continue
		}
		tail := s.recent[n-need:]
		cycle := tail[:cycleLen]
		// Require adjacent elements in the candidate cycle to differ —
		// otherwise it's a uniform streak, not an alternating cycle.
		cycleValid := true
		for i := 1; i < cycleLen; i++ {
			if cycle[i] == cycle[i-1] {
				cycleValid = false
				break
			}
		}
		if !cycleValid {
			continue
		}
		match := true
		for i := cycleLen; i < need; i++ {
			if tail[i] != cycle[i%cycleLen] {
				match = false
				break
			}
		}
		if match {
			return fmt.Sprintf("length-%d cycle repeated %d times", cycleLen, need/cycleLen)
		}
	}
	return ""
}

// StuckErr adapts a StuckDetector verdict into an error, so both detector call
// sites read as a single guard instead of a repeated five-line block.
func StuckErr(stuck bool, detail string) error {
	if !stuck {
		return nil
	}
	return fmt.Errorf("agent loop aborted: %s", detail)
}

// ObserveEvent feeds one event's parts through every arm of the detector and
// returns a non-nil error once the run has been judged degenerate. Front ends
// call it after emitting the event, so the user sees the offending output
// before the run aborts; the thresholds are the same either way.
//
// This exists so each front end does not hand-roll the same four calls in a
// different order and drift apart.
//
// The tool-free-thinking arm is scored per event rather than per part, so an
// event carrying three text parts counts once; the parts are summed first and
// the verdict taken after the loop, once it is known whether the same event
// also acted.
//
// No stream dedup is applied here, so under SSE the aggregate re-send is
// counted alongside the deltas and the byte total runs roughly double. That is
// deliberate rather than overlooked: the session logs the thresholds were
// calibrated from are written without dedup on the thinking path too, so
// measurement and runtime inflate alike. The practical effect is that
// MinThinkingStreakBytes behaves like ~8 KiB of genuine text, which offsets
// some of the conservatism chosen above.
func (s *StuckDetector) ObserveEvent(ev *session.Event) error {
	if ev == nil || ev.Content == nil {
		return nil
	}
	textBytes, acted := 0, false
	for _, part := range ev.Content.Parts {
		if part.Text != "" {
			textBytes += len(part.Text)
			if err := StuckErr(s.ObserveOutput(part.Text)); err != nil {
				return err
			}
		}
		if fc := part.FunctionCall; fc != nil {
			acted = true
			s.ResetThinking()
			if err := StuckErr(s.Observe(fc.Name, fc.Args)); err != nil {
				return err
			}
		}
		if fr := part.FunctionResponse; fr != nil {
			acted = true
			s.ResetThinking()
			// A changed result on a repeated call is progress, not a loop.
			s.ObserveResult(fr.Name, fr.Response)
			_, isErr := fr.Response["error"]
			if err := StuckErr(s.ObserveError(fr.Name, isErr)); err != nil {
				return err
			}
		}
	}
	if acted || textBytes == 0 {
		return nil
	}
	// Text the model did not author — a user turn — starts the count over
	// rather than adding to it.
	if !modelRoles[ev.Content.Role] {
		s.ResetThinking()
		return nil
	}
	return StuckErr(s.ObserveThinking(textBytes))
}
