package eval

import (
	"strings"
	"testing"
)

func TestRenderMarkdown_JudgeSection(t *testing.T) {
	r := &RunReport{
		Metadata: ReportMetadata{Spec: "s", BaseRef: "eval/base", BaseCommit: "abc1234def5678"},
		Judge: &JudgeVerdict{
			Model:   "judge-model",
			Verdict: "fail",
			Overall: 2.5,
			Summary: "The worker retried the same failing command three times.",
			Scores: []JudgeScore{
				{Dimension: "outcome_correctness", Score: 1, Rationale: "merge failed"},
				{Dimension: "tools_efficiency", Score: 4, Rationale: "otherwise economical"},
			},
			Issues: []string{"three identical bash retries"},
		},
	}

	md := RenderMarkdown(r)

	for _, want := range []string{
		"## LLM judge",
		"judge-model",
		"FAIL",
		"2.50 / 5",
		"retried the same failing command",
		"outcome_correctness | 1/5",
		"tools_efficiency | 4/5",
		"Issues raised",
		"three identical bash retries",
		"**base ref**: `eval/base`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q\n---\n%s", want, md)
		}
	}
}

// A judge that could not run must degrade to a note, not an empty section that
// reads as "the run was not graded well".
func TestRenderMarkdown_JudgeUnavailable(t *testing.T) {
	md := RenderMarkdown(&RunReport{Judge: &JudgeVerdict{Error: "no API key"}})

	if !strings.Contains(md, "unavailable (no API key)") {
		t.Errorf("markdown does not report the judge as unavailable:\n%s", md)
	}
}

// No judge configured means no judge section at all.
func TestRenderMarkdown_NoJudgeSection(t *testing.T) {
	if md := RenderMarkdown(&RunReport{}); strings.Contains(md, "LLM judge") {
		t.Errorf("markdown has a judge section with no judge:\n%s", md)
	}
}

// Model-authored rationales contain pipes often enough to break the table they
// land in.
func TestRenderMarkdown_JudgeRationaleEscapesPipes(t *testing.T) {
	md := RenderMarkdown(&RunReport{Judge: &JudgeVerdict{
		Verdict: "pass",
		Scores:  []JudgeScore{{Dimension: "d", Score: 3, Rationale: "ran a | b\nacross lines"}},
	}})

	if !strings.Contains(md, `ran a \| b across lines`) {
		t.Errorf("rationale was not escaped for the table:\n%s", md)
	}
}
