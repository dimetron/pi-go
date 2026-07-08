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
	if isPingPong {
		testPrompt = "prompt-prompt"
	}

	systemMsg := "You are a connectivity test. Reply briefly and concisely."
	if isPingPong {
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
	var reply strings.Builder
	var totalIn int32
	var totalOut int32
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			w("**✗ Error:** %v", err)
			return buf.String(), "", err
		}
		if resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part.Text != "" {
					reply.WriteString(part.Text)
				}
			}
		}
		if resp.UsageMetadata != nil {
			totalIn = resp.UsageMetadata.PromptTokenCount
			totalOut = resp.UsageMetadata.CandidatesTokenCount
		}
	}

	replyText := strings.TrimSpace(reply.String())
	if replyText == "" {
		return buf.String(), "", fmt.Errorf("model returned empty response")
	}

	// Format the reply for display - truncate if too long
	displayReply := replyText
	if len(displayReply) > 80 {
		displayReply = displayReply[:80] + "..."
	}

	truncPrompt := testPrompt
	if len(truncPrompt) > 40 {
		truncPrompt = truncPrompt[:40] + "..."
	}

	w("**Provider:** %s  \n", providerName)
	w("**Model:** %s  \n", modelName)
	if isPingPong {
		w("**Test:** prompt-prompt  \n")
	} else {
		w("**Prompt:** %s  \n", truncPrompt)
	}
	w("**Tokens:** %din / %dout  \n", totalIn, totalOut)
	w("**Reply:** %s  \n\n", displayReply)
	w("✓ Model **%s** is ALIVE", modelName)

	return buf.String(), replyText, nil
}
