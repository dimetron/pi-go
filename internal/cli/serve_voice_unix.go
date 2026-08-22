//go:build !windows

package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/voicegemini"
	"github.com/dimetron/pi-go/internal/webserver"
)

// enableServeVoice turns on the browser voice session, failing with an
// actionable message when the key is absent.
//
// The key is resolved through config.LookupEnvFrom rather than os.Getenv so
// that the file `pi login` writes it to counts. Requiring an export for voice
// alone — when every other pi command finds the same key in ~/.pi-go/.env —
// reads as "voice is broken", and the operator has no way to tell an unset key
// from an unread one.
func enableServeVoice(ctx context.Context, server *webserver.ServerV2) error {
	key, source := config.LookupEnvFrom(serveProjectDir(), "GEMINI_API_KEY", "GOOGLE_API_KEY")
	if key == "" {
		return fmt.Errorf("--voice needs GEMINI_API_KEY: export it, or put it in .pi-go/.env, .env, or ~/.pi-go/.env")
	}
	fmt.Printf("Voice: GEMINI_API_KEY from %s\n", source)

	if ctx == nil {
		ctx = context.Background()
	}
	// Verification is a single models round-trip; bound it so a hung network
	// cannot stall startup indefinitely.
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := server.EnableVoice(vctx, key, voicegemini.WithModel(serveVoiceModel())); err != nil {
		return fmt.Errorf("enabling voice: %w", err)
	}
	return nil
}
