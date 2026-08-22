//go:build !windows

package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/voicegemini"
	"github.com/dimetron/pi-go/internal/webserver"
)

// enableServeVoice is the startup gate for --voice: no key is a usage error
// that names the key, and a key the provider rejects is a boot error rather
// than a dead microphone. Both are exercised without the network — the key
// lookup is isolated from the developer's own ~/.pi-go/.env and the verify
// round-trip is pointed at a fake models endpoint.
func TestEnableServeVoice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	oldProject, oldModel := flagServeProject, flagServeVoiceModel
	flagServeProject, flagServeVoiceModel = t.TempDir(), ""
	t.Cleanup(func() { flagServeProject, flagServeVoiceModel = oldProject, oldModel })

	t.Run("missing key", func(t *testing.T) {
		err := enableServeVoice(t.Context(), webserver.NewServerV2(webserver.Config{Addr: "127.0.0.1:0"}))
		if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
			t.Fatalf("enableServeVoice() = %v, want an error naming GEMINI_API_KEY", err)
		}
	})

	t.Setenv("GEMINI_API_KEY", "AIzaSyTestKeyLongEnough")
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "provider rejects the key", status: http.StatusForbidden, body: "nope", wantErr: "enabling voice"},
		{name: "accepted", status: http.StatusOK, body: `{"supportedGenerationMethods":["bidiGenerateContent"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer models.Close()

			err := enableServeVoice(t.Context(), webserver.NewServerV2(webserver.Config{Addr: "127.0.0.1:0"}),
				voicegemini.WithModelsURL(models.URL), voicegemini.WithHTTPClient(models.Client()))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("enableServeVoice() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("enableServeVoice() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}
