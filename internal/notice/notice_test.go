package notice

import (
	"strings"
	"sync"
	"testing"
)

func TestNotifyfUsesInstalledSink(t *testing.T) {
	var got []string
	prev := SetSink(func(msg string) { got = append(got, msg) })
	defer func() { SetSink(prev) }()

	Notifyf("MCP server %q unavailable: %v", "openrouter", "Unauthorized")

	if len(got) != 1 {
		t.Fatalf("sink received %d notices, want 1: %v", len(got), got)
	}
	want := `MCP server "openrouter" unavailable: Unauthorized`
	if got[0] != want {
		t.Errorf("notice = %q, want %q", got[0], want)
	}
	// The sink renders the notice as one item; a trailing newline would show
	// up as a blank line in the chat.
	if strings.HasSuffix(got[0], "\n") {
		t.Errorf("notice %q has a trailing newline", got[0])
	}
}

func TestSetSinkReturnsPreviousSink(t *testing.T) {
	first := func(string) {}
	prev := SetSink(first)
	defer func() { SetSink(prev) }()

	restored := SetSink(nil)
	if restored == nil {
		t.Fatal("SetSink did not return the sink it replaced")
	}
	// Restoring nil puts the os.Stderr default back; Notifyf must not panic.
	Notifyf("back to stderr")
}

// A notice can be raised from a lazily-connecting MCP toolset on any
// goroutine, so the sink must survive concurrent delivery and replacement.
func TestNotifyfConcurrent(t *testing.T) {
	var mu sync.Mutex
	count := 0
	prev := SetSink(func(string) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	defer func() { SetSink(prev) }()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Notifyf("notice")
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 32 {
		t.Errorf("sink saw %d notices, want 32", count)
	}
}
