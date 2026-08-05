package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

// TestOllamaSetupError covers the one command that cannot degrade: an
// unembedded drawer is invisible to semantic search forever after, and the
// failure is silent at query time. Both branches must keep wrapping the cause
// so callers can still errors.Is it, and the ErrOllamaUnavailable branch must
// name both fixes (daemon down / model not pulled) rather than dumping the
// raw error.
func TestOllamaSetupError(t *testing.T) {
	cfg := palace.PalaceConfig{
		OllamaURL:   "http://localhost:9999",
		OllamaModel: "test-embed-model",
	}

	t.Run("unrelated cause is wrapped without instructions", func(t *testing.T) {
		cause := errors.New("disk on fire")

		var err error
		out := captureStderr(t, func() { err = ollamaSetupError(cfg, cause) })

		if err == nil {
			t.Fatal("ollamaSetupError returned nil, want an error")
		}
		if !errors.Is(err, cause) {
			t.Fatalf("err does not wrap cause: %v", err)
		}
		if out != "" {
			t.Fatalf("non-ollama cause printed guidance to stderr:\n%s", out)
		}
	})

	t.Run("ErrOllamaUnavailable prints both fixes and still wraps", func(t *testing.T) {
		cause := fmt.Errorf("dial tcp: %w", palace.ErrOllamaUnavailable)

		var err error
		out := captureStderr(t, func() { err = ollamaSetupError(cfg, cause) })

		if err == nil {
			t.Fatal("ollamaSetupError returned nil, want an error")
		}
		// The wrap must survive, or errors.Is at the call site stops
		// recognizing this as the recoverable case.
		if !errors.Is(err, palace.ErrOllamaUnavailable) {
			t.Fatalf("err no longer wraps ErrOllamaUnavailable: %v", err)
		}

		for _, want := range []string{
			"ollama serve",      // fix 1: daemon down
			"ollama pull",       // fix 2: model not pulled
			cfg.OllamaModel,     // which model to pull
			cfg.OllamaURL,       // where the daemon was expected
			"palace.ollama_url", // how to change it
		} {
			if !strings.Contains(out, want) {
				t.Errorf("stderr guidance missing %q; got:\n%s", want, out)
			}
		}
	})
}
