//go:build e2e

package provider

// Probe gate for the live-API OpenAI tests: the account's prepaid credit
// balance gates every real call, and an exhausted balance returns
// 429 insufficient_quota / credit_balance_exhausted — a billing state, not a
// test failure. The tests in openai_e2e_test.go skip through here so the e2e
// suite stays green when the balance is empty and runs when it is not.

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// e2eOpenAICreditsAvailable reports whether the account can serve a one-token
// request. Cheap: one Responses call, minimal tokens.
func e2eOpenAICreditsAvailable(t *testing.T) bool {
	t.Helper()
	llm, err := NewOpenAI(context.Background(), e2eOpenAIModel, testGetOpenAIAPIKey(t), "", nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "1"}}}},
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			if isCreditExhausted(err) {
				t.Skipf("skipping: OpenAI account has no credits (%s) — add credits at platform.openai.com/settings/organization/billing and re-run", "credit_balance_exhausted")
			}
			t.Logf("probe error (running tests anyway): %v", err)
			return true
		}
	}
	return true
}

// isCreditExhausted reports whether err is OpenAI's prepaid-balance failure.
func isCreditExhausted(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "credit_balance_exhausted") || strings.Contains(msg, "insufficient_quota")
}
