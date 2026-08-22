//go:build windows

package cli

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/webserver"
)

// --voice must fail at startup on Windows before any key is looked up, so the
// error has to arrive even with no GEMINI_API_KEY anywhere.
func TestEnableServeVoiceUnsupported(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	err := enableServeVoice(t.Context(), webserver.NewServerV2(webserver.Config{Addr: "127.0.0.1:0"}))
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("enableServeVoice() = %v, want the Windows reason", err)
	}
}
