package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// pingDoneMsg carries the result of an async /ping execution.
type pingDoneMsg struct {
	output string
	err    error
}

// handlePingCommand runs the ping test asynchronously and displays the result.
// Uses the TUI's current LLM (from the agent) — not a fresh config reload.
func (m *model) handlePingCommand(args []string) (tea.Model, tea.Cmd) {
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: "Pinging model...",
	})
	m.inputModel.Clear()

	if m.cfg.LLM == nil {
		m.messages[len(m.messages)-1].content = "✗ No LLM configured"
		return m, nil
	}

	prompt := strings.Join(args, " ")
	ctx := m.ctx
	llm := m.cfg.LLM
	providerName := m.cfg.ProviderName
	modelName := m.cfg.ModelName

	return m, func() tea.Msg {
		output, pingErr := executePing(ctx, llm, providerName, modelName, prompt)
		return pingDoneMsg{output: output, err: pingErr}
	}
}

// executePing runs a ping test against the given LLM and returns formatted output.
// Extracted for testability with mock LLMs.
func executePing(ctx context.Context, llm llmmodel.LLM, providerName, modelName, prompt string) (string, error) {
	var buf strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&buf, format, a...) }

	w("**Provider:** %s\n", providerName)
	w("**Model:** %s\n", modelName)

	isPingPong := prompt == ""
	testPrompt := prompt
	if isPingPong {
		testPrompt = "Ping"
	}

	systemMsg := "You are a connectivity test. Reply briefly and concisely."
	if isPingPong {
		systemMsg = `You are a connectivity test. When the user says "Ping", reply with exactly "Pong" and nothing else.`
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
	w("**Non-streaming test...**\n")
	var reply strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			w("ERROR: %v\n", err)
			return buf.String(), err
		}
		if resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part.Text != "" {
					reply.WriteString(part.Text)
				}
			}
		}
		if resp.UsageMetadata != nil {
			w("tokens(in=%d out=%d)\n",
				resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
		}
	}

	replyText := strings.TrimSpace(reply.String())
	if replyText == "" {
		return buf.String(), fmt.Errorf("model returned empty response")
	}

	w("**Reply:** %s\n", replyText)
	w("\n✓ Model **%s** is ALIVE", modelName)

	return buf.String(), nil
}
