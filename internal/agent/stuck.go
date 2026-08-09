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
// identical tool calls, a tool failing over and over, or the model's own output
// collapsing into repetition. It lives here rather than in the TUI so every
// front end -- interactive, print, json, rpc and ACP -- shares one guard. A
// front end that skips it runs unprotected.
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
func (s *StuckDetector) ObserveEvent(ev *session.Event) error {
	if ev == nil || ev.Content == nil {
		return nil
	}
	for _, part := range ev.Content.Parts {
		if part.Text != "" {
			if err := StuckErr(s.ObserveOutput(part.Text)); err != nil {
				return err
			}
		}
		if fc := part.FunctionCall; fc != nil {
			if err := StuckErr(s.Observe(fc.Name, fc.Args)); err != nil {
				return err
			}
		}
		if fr := part.FunctionResponse; fr != nil {
			// A changed result on a repeated call is progress, not a loop.
			s.ObserveResult(fr.Name, fr.Response)
			_, isErr := fr.Response["error"]
			if err := StuckErr(s.ObserveError(fr.Name, isErr)); err != nil {
				return err
			}
		}
	}
	return nil
}
