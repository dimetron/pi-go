package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// pingDoneMsg carries the result of an async /ping execution.
type pingDoneMsg struct {
	output string
	reply  string
	err    error
}

// handlePingCommand runs the ping test asynchronously and displays the result.
// Uses the TUI's current LLM (from the agent) — not a fresh config reload.
func (m *model) handlePingCommand(args []string) (tea.Model, tea.Cmd) {
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "thinking",
		content: "Pinging model...",
	})
	m.inputModel.Clear()

	if m.cfg.LLM == nil {
		m.chatModel.Messages[len(m.chatModel.Messages)-1].content = "✗ No LLM configured"
		return m, nil
	}

	prompt := strings.Join(args, " ")
	ctx := m.ctx
	llm := m.cfg.LLM
	providerName := m.cfg.ProviderName
	modelName := m.cfg.ModelName

	return m, func() tea.Msg {
		output, reply, pingErr := executePing(ctx, llm, providerName, modelName, prompt)
		return pingDoneMsg{output: output, reply: reply, err: pingErr}
	}
}

// executePing runs a ping test against the given LLM and returns formatted output.
// Extracted for testability with mock LLMs.
func executePing(ctx context.Context, llm llmmodel.LLM, providerName, modelName, prompt string) (string, string, error) {
	var buf strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&buf, format, a...) }

	isPingPong := prompt == ""
	testPrompt := prompt
	systemMsg := "You are a connectivity test. Reply briefly and concisely."
	if isPingPong {
		testPrompt = "prompt-prompt"
		systemMsg = `You are a connectivity test. When the user says "prompt-prompt", reply with exactly "prompt-prompt" and nothing else.`
	}

	req := &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(testPrompt, genai.RoleUser),
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(systemMsg, genai.RoleUser),
		},
	}

	// Non-streaming test.
	res, err := collectPingReply(ctx, llm, req)
	if err != nil {
		w("**✗ Error:** %v", err)
		return buf.String(), "", err
	}

	replyText := strings.TrimSpace(res.text)
	if replyText == "" {
		return buf.String(), "", fmt.Errorf("model returned empty response")
	}

	w("**Provider:** %s  \n", providerName)
	w("**Model:** %s  \n", modelName)
	if isPingPong {
		w("**Test:** prompt-prompt  \n")
	} else {
		w("**Prompt:** %s  \n", truncatePingText(testPrompt, 40))
	}
	w("**Tokens:** %din / %dout  \n", res.inTokens, res.outTokens)
	w("**Reply:** %s  \n\n", truncatePingText(replyText, 80))
	w("✓ Model **%s** is ALIVE", modelName)

	return buf.String(), replyText, nil
}

// pingReply is what one non-streaming ping exchange produced: the model's text
// with every chunk and part concatenated, and the token counts from the last
// chunk that carried usage metadata.
type pingReply struct {
	text      string
	inTokens  int32
	outTokens int32
}

// collectPingReply drains the response iterator. An error from any chunk ends
// the exchange immediately, discarding whatever text came before it.
func collectPingReply(ctx context.Context, llm llmmodel.LLM, req *llmmodel.LLMRequest) (pingReply, error) {
	var res pingReply
	var reply strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return pingReply{}, err
		}
		appendPingText(&reply, resp.Content)
		if resp.UsageMetadata != nil {
			res.inTokens = resp.UsageMetadata.PromptTokenCount
			res.outTokens = resp.UsageMetadata.CandidatesTokenCount
		}
	}
	res.text = reply.String()
	return res, nil
}

// appendPingText writes every non-empty text part of content to dst. A nil
// content contributes nothing — providers send chunks that carry only usage.
func appendPingText(dst *strings.Builder, content *genai.Content) {
	if content == nil {
		return
	}
	for _, part := range content.Parts {
		if part.Text != "" {
			dst.WriteString(part.Text)
		}
	}
}

// truncatePingText shortens s to max characters plus an ellipsis, for the
// one-line prompt and reply previews in the ping report. Length is counted in
// bytes, as it always has been, so a cut can land inside a multi-byte rune.
func truncatePingText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
