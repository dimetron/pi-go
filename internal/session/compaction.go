package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Context growth in a long coding session is dominated by tool results, not by
// the conversation: measured over a day of pi-go sessions, tool results were
// 80% of all context bytes, and every one is re-sent on every later turn.
//
// Compaction runs in two stages so that the cheap remedy is not skipped in
// favor of the expensive one:
//
//   - Shed (the lower threshold) drops the payload of tool results that have
//     been superseded by a later call on the same target. No LLM call, and the
//     stable cached prefix is untouched, so the prompt cache survives.
//   - Summarize (the upper threshold) is the Codex-style rebuild: keep the
//     initial context and the user's own messages, replace everything else
//     with an LLM-written handoff summary. This invalidates the prompt cache,
//     so it is deliberately reserved for a nearly-full window.
//
// Thresholds are measured against the body — tokens accumulated after the
// stable cached prefix — so a large but fully cached system prompt and tool
// declaration block never pushes a session toward compaction.

// CompactionAction is what the auto-compactor decided to do.
type CompactionAction int

const (
	// CompactionNone means the context is comfortably within budget.
	CompactionNone CompactionAction = iota
	// CompactionShed means drop superseded tool-result payloads.
	CompactionShed
	// CompactionSummarize means run a full summarizing rebuild.
	CompactionSummarize
)

func (a CompactionAction) String() string {
	switch a {
	case CompactionShed:
		return "shed"
	case CompactionSummarize:
		return "summarize"
	default:
		return "none"
	}
}

// AutoCompactConfig controls the two-stage auto-compaction trigger.
type AutoCompactConfig struct {
	// Enabled turns auto-compaction on. Manual /compact works regardless.
	Enabled bool `json:"enabled"`

	// ShedPercent is the share of the context window at which superseded tool
	// results are dropped. Default 60.
	ShedPercent int `json:"shed_percent"`

	// SummarizePercent is the share at which a full summarizing rebuild runs.
	// Default 88. Upstream Codex uses 90 as a hard ceiling that user config may
	// only lower; 88 leaves headroom for the summarization call itself.
	SummarizePercent int `json:"summarize_percent"`

	// KeepUserMessageTokens caps the user messages carried across a summarizing
	// rebuild, newest first. Matches Codex's COMPACT_USER_MESSAGE_MAX_TOKENS.
	KeepUserMessageTokens int `json:"keep_user_message_tokens"`

	// KeepRecentEvents is the tail of the conversation that shedding never
	// touches, so the model always retains its immediate working set.
	KeepRecentEvents int `json:"keep_recent_events"`
}

// DefaultAutoCompactConfig returns the two-stage defaults: shed at 60% of the
// context window, summarize at 90%.
func DefaultAutoCompactConfig() AutoCompactConfig {
	return AutoCompactConfig{
		Enabled:               true,
		ShedPercent:           60,
		SummarizePercent:      90,
		KeepUserMessageTokens: 20000,
		KeepRecentEvents:      10,
	}
}

// normalize fills zero fields with defaults and clamps the thresholds into a
// sane order, so a partial config from config.json cannot produce a compactor
// that summarizes before it sheds.
func (c AutoCompactConfig) normalize() AutoCompactConfig {
	d := DefaultAutoCompactConfig()
	if c.ShedPercent <= 0 {
		c.ShedPercent = d.ShedPercent
	}
	if c.SummarizePercent <= 0 {
		c.SummarizePercent = d.SummarizePercent
	}
	if c.KeepUserMessageTokens <= 0 {
		c.KeepUserMessageTokens = d.KeepUserMessageTokens
	}
	if c.KeepRecentEvents <= 0 {
		c.KeepRecentEvents = d.KeepRecentEvents
	}
	// A hard ceiling: never let config push summarization past the point where
	// the summarization request itself would not fit.
	if c.SummarizePercent > 95 {
		c.SummarizePercent = 95
	}
	if c.ShedPercent > c.SummarizePercent {
		c.ShedPercent = c.SummarizePercent
	}
	return c
}

// Decide returns the action for the given body size. bodyTokens is the context
// accumulated after the stable cached prefix; windowSize is the model's full
// context window. A zero or unknown windowSize disables auto-compaction, since
// a percentage of an unknown quantity is meaningless.
func (c AutoCompactConfig) Decide(bodyTokens, windowSize int64) CompactionAction {
	if !c.Enabled || windowSize <= 0 || bodyTokens <= 0 {
		return CompactionNone
	}
	n := c.normalize()
	pct := float64(bodyTokens) / float64(windowSize) * 100
	switch {
	case pct >= float64(n.SummarizePercent):
		return CompactionSummarize
	case pct >= float64(n.ShedPercent):
		return CompactionShed
	default:
		return CompactionNone
	}
}

// SummarizationPrompt is the instruction given to the summarizing model.
// Adapted from Codex's compact prompt (codex-rs/prompts/templates/compact/prompt.md).
const SummarizationPrompt = `You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue
- Absolute paths of files read or modified, so the next model does not re-discover them

Be concise, structured, and focused on helping the next LLM seamlessly continue the work.`

// SummaryPrefix introduces the summary in the rebuilt conversation, telling the
// resuming model where the text came from.
const SummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. " +
	"Use the information in this summary to continue the work and avoid duplicating it. Here is the summary:"

// LLMSummarizer returns a Summarizer backed by a real model call. It renders
// the events being dropped as a transcript and asks the model for a handoff
// summary.
//
// The returned Summarizer never fails the compaction outright: if the model
// call errors or comes back empty, it returns a degraded placeholder so the
// caller still reclaims the context rather than aborting and staying over
// budget with no way to recover.
func LLMSummarizer(ctx context.Context, llm llmmodel.LLM) Summarizer {
	return func(events []*session.Event) (string, error) {
		return summarizeWithLLM(ctx, llm, events)
	}
}

// summarizeWithLLM is the body of the Summarizer LLMSummarizer returns. It
// never fails: a model error or an empty answer degrades to a placeholder so
// the caller still reclaims the context.
func summarizeWithLLM(ctx context.Context, llm llmmodel.LLM, events []*session.Event) (string, error) {
	if llm == nil {
		return SimpleSummarizer(events)
	}
	transcript := renderTranscript(events)
	if strings.TrimSpace(transcript) == "" {
		return SimpleSummarizer(events)
	}

	summary, err := generateSummaryText(ctx, llm, transcript)
	if err != nil {
		return degradedSummary(events, err), nil
	}
	if summary == "" {
		return degradedSummary(events, nil), nil
	}
	return summary, nil
}

// generateSummaryText asks the model to summarize the transcript and joins the
// text of every part it streams back. A partial answer interrupted by an error
// is discarded rather than returned: half a handoff summary reads as complete
// to the resuming model.
func generateSummaryText(ctx context.Context, llm llmmodel.LLM, transcript string) (string, error) {
	req := &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(transcript, genai.RoleUser),
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(SummarizationPrompt, genai.RoleUser),
		},
	}

	var b strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		appendPartText(&b, resp.Content.Parts)
	}
	return strings.TrimSpace(b.String()), nil
}

// appendPartText writes the text of every part to b, in order.
func appendPartText(b *strings.Builder, parts []*genai.Part) {
	for _, part := range parts {
		if part.Text != "" {
			b.WriteString(part.Text)
		}
	}
}

// degradedSummary is the fallback when summarization could not produce text.
// It states plainly that detail was lost, rather than implying a real summary.
func degradedSummary(events []*session.Event, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Compaction summary unavailable — %d earlier events were dropped to reclaim context.", len(events))
	if err != nil {
		fmt.Fprintf(&b, " Summarizer error: %v.", err)
	}
	b.WriteString(" Detail from before this point is lost; re-read files or ask the user if you need it.]")

	if files := filesTouched(events); len(files) > 0 {
		b.WriteString("\n\nFiles touched before compaction:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	return b.String()
}

// renderTranscript flattens events into a plain-text transcript for the
// summarizing model. Tool results are truncated: the summarizer needs to know
// what was done, not to re-ingest every byte that made the context too large.
func renderTranscript(events []*session.Event) string {
	const maxToolResultChars = 2000

	var b strings.Builder
	for _, ev := range events {
		if ev == nil || ev.Content == nil {
			continue
		}
		role := ev.Content.Role
		if ev.Author != "" {
			role = ev.Author
		}
		for _, part := range ev.Content.Parts {
			switch {
			case part.Text != "":
				fmt.Fprintf(&b, "%s: %s\n", role, part.Text)
			case part.FunctionCall != nil:
				args, _ := json.Marshal(part.FunctionCall.Args)
				fmt.Fprintf(&b, "%s called %s(%s)\n", role, part.FunctionCall.Name, truncateForPrompt(string(args), 500))
			case part.FunctionResponse != nil:
				resp, _ := json.Marshal(part.FunctionResponse.Response)
				fmt.Fprintf(&b, "%s result: %s\n", part.FunctionResponse.Name,
					truncateForPrompt(string(resp), maxToolResultChars))
			}
		}
	}
	return b.String()
}

func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("... (%d more bytes)", len(s)-max)
}

// filesTouched collects file paths seen in tool-call arguments, so even a
// degraded summary leaves the next model a trail to follow.
func filesTouched(events []*session.Event) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ev := range events {
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			out = appendCallFilePaths(out, seen, part)
		}
	}
	return out
}

// filePathArgKeys are the tool-call argument names that name a file, in the
// order they are collected.
var filePathArgKeys = []string{"file_path", "path", "filePath"}

// appendCallFilePaths appends the not-yet-seen paths named by part's
// function-call arguments, marking each one in seen. Non-string and empty
// values carry no trail, so they are skipped.
func appendCallFilePaths(out []string, seen map[string]bool, part *genai.Part) []string {
	if part.FunctionCall == nil {
		return out
	}
	for _, key := range filePathArgKeys {
		s, isStr := part.FunctionCall.Args[key].(string)
		if !isStr || s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
