package tui

import (
	"fmt"
	"strings"
	"testing"
)

// scrollback builds a chat with n read-tool messages, the shape that made
// rendering expensive: each one gets syntax-highlighted.
func scrollback(t testing.TB, n int) ChatModel {
	t.Helper()
	c := NewChatModel(nil)
	c.Width = 100
	c.ToolDisplay.Width = 100

	var code strings.Builder
	for i := range 40 {
		fmt.Fprintf(&code, "%d\tfunc handler%d(w http.ResponseWriter, r *http.Request) error {\n", i+1, i)
	}
	for i := range n {
		c.Messages = append(c.Messages,
			message{role: "user", content: fmt.Sprintf("question %d", i)},
			message{role: "tool", tool: "read", toolIn: `{"file_path":"server.go"}`, content: code.String()},
			message{role: "assistant", content: fmt.Sprintf("Here is answer %d.", i)},
		)
	}
	return c
}

// The cache has to work *while the agent is running* — that is when frames tick
// fastest and the scrollback is longest. It used to be disabled in exactly that
// case, so every frame re-highlighted the whole history.
func TestRenderMessagesUsesCacheWhileRunning(t *testing.T) {
	c := scrollback(t, 3)

	c.RenderMessages(true) // populate

	for i := range c.Messages {
		if !c.Messages[i].renderCached {
			t.Fatalf("message %d not cached after a render with running=true", i)
		}
	}

	// Hijack a cached entry without touching its key. A second render must serve
	// it verbatim, which is only possible if the cache was consulted.
	c.Messages[1].renderCache = "SENTINEL-CACHE-HIT"
	if out := c.RenderMessages(true); !strings.Contains(out, "SENTINEL-CACHE-HIT") {
		t.Fatal("cache not used on re-render while running; the scrollback is being re-rendered every frame")
	}
}

// The flip side: a message whose content changes must not serve a stale render.
// This is what the old `!running` guard was protecting against, and what the
// input fingerprint now guarantees.
func TestRenderMessagesRerendersMutatedMessage(t *testing.T) {
	c := scrollback(t, 1)
	c.RenderMessages(true)

	// Streaming assistant text grows: same message, new content.
	last := len(c.Messages) - 1
	c.Messages[last].content = "Here is answer 0. And a follow-up sentence."

	out := c.RenderMessages(true)
	if !strings.Contains(out, "follow-up sentence") {
		t.Fatal("mutated message served a stale cached render")
	}
}

// Growing a subagent's event stream changes the render even though `content`
// is untouched — the key covers agentEvents too.
func TestRenderMessagesRerendersOnAgentEvents(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 100
	c.ToolDisplay.Width = 100
	c.Messages = []message{{
		role: "tool", tool: "agent", agentID: "a1", agentTitle: "explore",
		agentEvents: []agentEv{{kind: "text", content: "starting"}},
	}}

	c.RenderMessages(true)
	c.Messages[0].agentEvents = append(c.Messages[0].agentEvents,
		agentEv{kind: "text", content: "UNIQUE-SECOND-EVENT"})

	if out := c.RenderMessages(true); !strings.Contains(out, "UNIQUE-SECOND-EVENT") {
		t.Fatal("new subagent event served a stale cached render")
	}
}

// Toggling compact mode changes every tool card, so it must bust the cache.
func TestRenderMessagesRerendersOnCompactToggle(t *testing.T) {
	c := scrollback(t, 1)
	full := c.RenderMessages(false)

	c.ToolDisplay.CompactTools = true
	compact := c.RenderMessages(false)

	if full == compact {
		t.Fatal("compact-tools toggle served a stale cached render")
	}
}

// The win: a steady-state frame with nothing changed does no highlighting work.
// Compare against the old behavior, reproduced by dropping the cache each frame.
func BenchmarkRenderMessagesRunningCached(b *testing.B) {
	c := scrollback(b, 10)
	c.RenderMessages(true)
	b.ResetTimer()
	for b.Loop() {
		c.RenderMessages(true)
	}
}

func BenchmarkRenderMessagesRunningUncached(b *testing.B) {
	c := scrollback(b, 10)
	b.ResetTimer()
	for b.Loop() {
		c.invalidateRenderCaches() // what !running used to do, in effect
		c.RenderMessages(true)
	}
}

// The lexer lookup is the second fix, and the render cache would otherwise hide
// it: lexers.Match globs the filename against every registered lexer.
func BenchmarkMatchLexerCached(b *testing.B) {
	matchLexer("server.go")
	b.ResetTimer()
	for b.Loop() {
		matchLexer("server.go")
	}
}

func BenchmarkMatchLexerUncached(b *testing.B) {
	for b.Loop() {
		lexerCacheMu.Lock()
		delete(lexerCache, "server.go")
		lexerCacheMu.Unlock()
		matchLexer("server.go")
	}
}
