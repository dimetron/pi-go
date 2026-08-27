package sop

import (
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantResult string
		wantStated bool
		wantPassed bool
	}{
		{"plain pass", "All good.\n\nVERDICT: PASS\n", VerdictPass, true, true},
		{"plain fail", "Two criteria unmet.\n\nVERDICT: FAIL\n", VerdictFail, true, false},
		{"bolded", "**VERDICT: PASS**\n", VerdictPass, true, true},
		{"lowercase key", "verdict: pass\n", VerdictPass, true, true},
		{"leading whitespace", "   VERDICT:   FAIL  \n", VerdictFail, true, false},
		{"no verdict", "I checked the diff and it looks fine.\n", "", false, false},
		{"empty", "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseVerdict(tt.text)
			if got.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", got.Result, tt.wantResult)
			}
			if got.Stated != tt.wantStated {
				t.Errorf("Stated = %v, want %v", got.Stated, tt.wantStated)
			}
			if got.Passed() != tt.wantPassed {
				t.Errorf("Passed() = %v, want %v", got.Passed(), tt.wantPassed)
			}
		})
	}
}

// An absent verdict must never read as a pass. This is the specific failure the
// type exists to prevent: the prose contract asked for the line and nothing
// checked whether it arrived.
func TestUnstatedVerdictIsNotAPass(t *testing.T) {
	v := ParseVerdict("The implementation looks complete to me.")
	if v.Passed() {
		t.Fatal("a reply with no VERDICT line was treated as a pass")
	}
	if !strings.Contains(v.Reason(), "no VERDICT line") {
		t.Errorf("Reason() = %q, want it to name the missing verdict", v.Reason())
	}
}

// A Verifier that reasons aloud may echo the word before concluding; the last
// line is the conclusion.
func TestParseVerdictTakesTheLastLine(t *testing.T) {
	text := "I will end with VERDICT: PASS if all criteria are met.\n" +
		"Criterion 2 - NOT MET: the handler is a stub in `internal/api/handler.go:42`\n" +
		"VERDICT: FAIL\n"
	got := ParseVerdict(text)
	if got.Result != VerdictFail {
		t.Fatalf("Result = %q, want FAIL (the concluding line)", got.Result)
	}
	if got.Passed() {
		t.Error("a FAIL verdict passed")
	}
}

func TestParseVerdictExtractsUnmetCriteria(t *testing.T) {
	text := "Review:\n" +
		"- POST /api/x returns 202 - NOT MET: handler returns 500, see `internal/api/handler.go:42`\n" +
		"- No stubs remain - NOT MET: panic(\"not implemented\") in `internal/api/job.go`\n" +
		"VERDICT: FAIL\n"
	got := ParseVerdict(text)
	if len(got.Unmet) != 2 {
		t.Fatalf("got %d unmet criteria, want 2: %+v", len(got.Unmet), got.Unmet)
	}
	if got.Unmet[0].File != "internal/api/handler.go" || got.Unmet[0].Line != 42 {
		t.Errorf("first unmet file/line = %q:%d", got.Unmet[0].File, got.Unmet[0].Line)
	}
	reason := got.Reason()
	for _, want := range []string{"POST /api/x", "No stubs remain", "handler.go"} {
		if !strings.Contains(reason, want) {
			t.Errorf("Reason() omits %q:\n%s", want, reason)
		}
	}
}

func TestPassingVerdictHasNoReason(t *testing.T) {
	if got := ParseVerdict("VERDICT: PASS").Reason(); got != "" {
		t.Errorf("Reason() = %q, want empty for a pass", got)
	}
}

// A slice is done when it says so AND its verify command actually passed. The
// second half is what the corpus was missing.
func TestSliceResultDone(t *testing.T) {
	tests := []struct {
		name string
		r    SliceResult
		want bool
	}{
		{"done and verified", SliceResult{Status: "done", VerifyPassed: true}, true},
		{"done but unverified", SliceResult{Status: "done", VerifyPassed: false}, false},
		{"verified but partial", SliceResult{Status: "partial", VerifyPassed: true}, false},
		{"blocked", SliceResult{Status: "blocked"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Done(); got != tt.want {
				t.Errorf("Done() = %v, want %v", got, tt.want)
			}
		})
	}
}
