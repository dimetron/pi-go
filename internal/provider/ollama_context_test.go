package provider

import "testing"

// TestOllamaContextWindows pins the context windows of the Ollama Cloud
// catalog, in both the bare and the ":cloud"-tagged spelling.
//
// The values are Ollama's own. Ollama enforces the limit it reports, and the
// other source pi-go consults — models.dev — disagrees for two models:
// glm-5.2 (976000 against Ollama's 1048576) and nemotron-3-nano:30b (1048576
// against Ollama's 262144). Both disagreements were checked against the live
// API by sending a prompt past the smaller number, and the server refused it
// with "model maximum context length: <Ollama's number>" in both cases. The
// server's number is the one that decides whether a request is accepted.
//
// Getting this wrong fails in both directions. A window that is too small
// compacts the transcript before the model would have refused the prompt; a
// window that is too large lets the session grow until the API rejects it.
func TestOllamaContextWindows(t *testing.T) {
	tests := map[string]int64{
		"beam":                     1_048_576,
		"deepseek-v4-pro:0813":     1_048_576,
		"deepseek-v4.1-flash":      1_048_576,
		"deepseek-v4.1-flash:1017": 1_048_576,
		"gemma4:31b":               262_144,
		"glm-5.2":                  1_048_576,
		"glm-5.3":                  1_048_576,
		"glm-5.3-flash":            1_048_576,
		"gpt-oss:120b":             131_072,
		"gpt-oss:20b":              131_072,
		"kimi-k2.6":                262_144,
		"kimi-k2.7-code":           262_144,
		"kimi-k3":                  1_048_576,
		"minimax-m2.7":             196_608,
		"minimax-m3":               512_000,
		"minimax-m3.1-flash":       1_000_000,
		"mistral-large-3:675b":     262_144,
		"mistral-large-4":          1_048_576,
		"muse-spark-open":          1_048_576,
		"nemotron-3-nano:30b":      262_144,
		"nemotron-3-super":         262_144,
		"nemotron-3-ultra":         262_144,
	}
	for model, want := range tests {
		t.Run(model, func(t *testing.T) {
			// Both spellings resolve: the bare name is what the cloud API
			// lists, the ":cloud" tag is how pi-go names the same model
			// locally, and a user may type either.
			for _, spelled := range []string{model, model + ":cloud"} {
				if got := ContextWindowSizeFor("ollama", spelled); got != want {
					t.Errorf("ContextWindowSizeFor(ollama, %q) = %d, want %d", spelled, got, want)
				}
			}
		})
	}
}

// TestOllamaContextWindowLongestPrefixWins pins that a versioned model
// resolves to its own window and not to a shorter-named sibling.
//
// "minimax-m3" is a prefix of "minimax-m3.1-flash", and the two are sized
// differently (512K against 1M). The lookup is prefix-based, so the longest
// match is the one that has to win — and this is the only ordering in the
// catalog where the shorter name is a prefix of the longer one, so a naive
// first-match would charge the sibling's window.
func TestOllamaContextWindowLongestPrefixWins(t *testing.T) {
	if got := ContextWindowSizeFor("ollama", "minimax-m3"); got != 512_000 {
		t.Errorf("ContextWindowSizeFor(ollama, minimax-m3) = %d, want 512000", got)
	}
	if got := ContextWindowSizeFor("ollama", "minimax-m3.1-flash"); got != 1_000_000 {
		t.Errorf("ContextWindowSizeFor(ollama, minimax-m3.1-flash) = %d, want 1000000", got)
	}
	if got := ContextWindowSizeFor("ollama", "minimax-m3.1-flash:cloud"); got != 1_000_000 {
		t.Errorf("ContextWindowSizeFor(ollama, minimax-m3.1-flash:cloud) = %d, want 1000000", got)
	}
}
