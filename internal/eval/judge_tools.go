package eval

import (
	"context"
	"fmt"
	"strings"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

// ToolsJudgeDimensions are the axes the tool-coverage judge grades.
var ToolsJudgeDimensions = []string{
	"outcome_correctness",
	"tool_selection",
	"tools_efficiency",
	"trajectory_quality",
}

const toolsJudgeSystemPrompt = `You are grading the execution traces of an autonomous coding agent across a suite of
small tool-usage scenarios, not the code it wrote.

Each scenario seeds a workspace and asks the agent to accomplish something that a competent
agent would do with specific tools (read, edit, grep, git-*, subagent, lsp-*, ...). You are
given the per-scenario verdicts the harness computed deterministically (target tools called,
assertions on files and results), the tool coverage matrix, and a condensed tool-call
timeline per scenario.

Grade these dimensions from 1 (bad) to 5 (excellent):

- outcome_correctness: did the scenarios reach their goals? Weigh the harness verdicts most
  heavily. A suite where several scenarios failed their assertions cannot score above 2.
- tool_selection: did the agent pick the tool the task called for — the dedicated tool
  rather than a bash workaround, and without calling tools the task did not need?
- tools_efficiency: were tool calls economical? Penalize duplicate calls, calls whose
  results were never used, error-producing calls repeated without changing approach, and
  pulling far more content than needed.
- trajectory_quality: was each path direct and purposeful? Penalize aimless exploration,
  re-reading the same files, and long detours before the first relevant call.

Judge only what the evidence shows. If the timeline is too sparse to assess a dimension,
score it 3 and say so in the rationale.

Reply with ONLY a JSON object, no prose and no code fences:

{
  "scores": [
    {"dimension": "outcome_correctness", "score": 1-5, "rationale": "one or two sentences"},
    {"dimension": "tool_selection", "score": 1-5, "rationale": "..."},
    {"dimension": "tools_efficiency", "score": 1-5, "rationale": "..."},
    {"dimension": "trajectory_quality", "score": 1-5, "rationale": "..."}
  ],
  "verdict": "pass" or "fail",
  "summary": "two or three sentences on how this suite went",
  "issues": ["specific, actionable problem", "..."]
}

Set verdict to "fail" if more than a quarter of the scenarios failed or any dimension scores 1.`

// JudgeTools asks an LLM to grade a tool-coverage run. Like Judge, every
// failure comes back as a verdict carrying Error rather than a Go error, so an
// unavailable judge degrades the report instead of failing the eval.
func JudgeTools(ctx context.Context, complete CompleteFunc, model string, r *ToolsReport, digest string) JudgeVerdict {
	if complete == nil {
		return JudgeVerdict{Model: model, Error: "no judge model configured"}
	}
	reply, err := complete(ctx, toolsJudgeSystemPrompt, BuildToolsJudgePrompt(r, digest))
	if err != nil {
		return JudgeVerdict{Model: model, Error: err.Error()}
	}
	v, err := ParseJudgeVerdict(reply)
	if err != nil {
		return JudgeVerdict{Model: model, Error: err.Error()}
	}
	v.Model = model
	return v
}

// BuildToolsJudgePrompt renders the evidence the tools judge grades: the
// per-scenario verdicts, the coverage summary, then the per-scenario
// tool-call timelines.
func BuildToolsJudgePrompt(r *ToolsReport, digest string) string {
	var b strings.Builder
	b.WriteString("# Tool-coverage suite under review\n\n")
	if r != nil {
		writeToolsEvidence(&b, r)
	}
	if strings.TrimSpace(digest) != "" {
		b.WriteString("\n## Tool-call timelines\n\n")
		b.WriteString(digest)
		b.WriteString("\n")
	}
	return b.String()
}

func writeToolsEvidence(b *strings.Builder, r *ToolsReport) {
	fmt.Fprintf(b, "Model: %s\n\n", r.Metadata.Model)
	fmt.Fprintf(b, "## Scenarios (%d passed, %d failed, %d skipped, %d errored)\n\n",
		r.Metadata.Passed, r.Metadata.Failed, r.Metadata.Skipped, r.Metadata.Errored)
	for _, s := range r.Scenarios {
		fmt.Fprintf(b, "- %s: %s", s.Name, s.Status)
		if s.Reason != "" {
			fmt.Fprintf(b, " — %s", truncate(oneLine(s.Reason), 300))
		}
		fmt.Fprintf(b, " (%d session(s), %d tool call(s))\n", s.Sessions, s.ToolCalls)
	}

	c := r.Coverage
	fmt.Fprintf(b, "\n## Coverage\n\n")
	fmt.Fprintf(b, "- registered tools: %d; exercised ok: %d; errors only: %d; not called: %d; excluded: %d; unmapped: %d\n",
		c.Total, c.OK, c.Errors, c.NotCalled, c.Excluded, c.Unmapped)
	if len(c.Gap) > 0 {
		fmt.Fprintf(b, "- gap: %s\n", strings.Join(c.Gap, ", "))
	}

	tm := r.Tools
	fmt.Fprintf(b, "\n## Tools\n\n")
	fmt.Fprintf(b, "- total calls: %d (results: %d)\n", tm.TotalCalls, tm.TotalResults)
	fmt.Fprintf(b, "- calls with no result: %d\n", tm.Wasted)
	fmt.Fprintf(b, "- duplicate calls (same tool, same arguments): %d\n", tm.Duplicates)
	if len(tm.ByTool) > 0 {
		b.WriteString("\n| tool | calls | errors | wasted | duplicates | avg result bytes |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, name := range sortedKeys(tm.ByTool) {
			st := tm.ByTool[name]
			fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %d |\n",
				name, st.Calls, st.Errors, st.Wasted, st.Duplicates, st.AvgResultBytes)
		}
	}
}

// ProviderComplete builds the single-shot LLM call a judge uses, resolving the
// model the same way the CLI resolves any model. It returns nil and a reason
// when the model is unresolvable or has no API key, so the caller can record
// "judge unavailable" and still produce its measured report.
func ProviderComplete(model string) (CompleteFunc, string) {
	if model == "" {
		return nil, "no judge model configured"
	}
	info, err := provider.Resolve(model)
	if err != nil {
		return nil, fmt.Sprintf("judge model %q unresolvable: %v", model, err)
	}
	apiKey := config.APIKeys()[info.Provider]
	if apiKey == "" && !info.Ollama {
		return nil, fmt.Sprintf("judge model %q has no API key for provider %q", model, info.Provider)
	}

	return func(ctx context.Context, system, user string) (string, error) {
		llm, err := provider.NewLLM(ctx, info, apiKey, "", "none", &provider.LLMOptions{})
		if err != nil {
			return "", fmt.Errorf("create judge llm: %w", err)
		}
		req := &llmmodel.LLMRequest{
			Contents: []*genai.Content{genai.NewContentFromText(user, genai.RoleUser)},
			Config: &genai.GenerateContentConfig{
				SystemInstruction: genai.NewContentFromText(system, genai.RoleUser),
			},
		}
		var reply strings.Builder
		for resp, err := range llm.GenerateContent(ctx, req, false) {
			if err != nil {
				return "", fmt.Errorf("judge llm: %w", err)
			}
			if resp.Content == nil {
				continue
			}
			for _, part := range resp.Content.Parts {
				if part.Text != "" && !part.Thought {
					reply.WriteString(part.Text)
				}
			}
		}
		if strings.TrimSpace(reply.String()) == "" {
			return "", fmt.Errorf("judge returned an empty reply")
		}
		return reply.String(), nil
	}, ""
}
