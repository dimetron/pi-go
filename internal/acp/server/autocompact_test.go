package server

import (
	"context"
	"testing"

	adksession "google.golang.org/adk/v2/session"

	piagent "github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/autocompact"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// newTestAgent builds the smallest agent installAutoCompact can be handed. It
// needs no model or tools: the hook is set on the agent, not exercised here.
func newTestAgent(t *testing.T) *piagent.Agent {
	t.Helper()
	ag, err := piagent.New(piagent.Config{
		Model:          nil,
		SessionService: adksession.InMemoryService(),
	})
	if err != nil {
		t.Skipf("agent.New unavailable in this environment: %v", err)
	}
	return ag
}

// newTracker returns a tracker with a known window, since an unknown window is
// itself a reason the hook declines to install.
func newTracker(t *testing.T, window int64) *guardrail.Tracker {
	t.Helper()
	tr := guardrail.NewWithPath(0, t.TempDir()+"/usage.json")
	tr.SetContextWindowSize(window)
	return tr
}

// TestInstallAutoCompact covers which ACP sessions get a compaction hook.
//
// The gap this closes was silent: ACP sessions are the longest-lived shape
// pi-go runs in and had no compaction at all, so the only symptom was a
// transcript that grew until the provider rejected it. A regression here would
// be equally quiet, which is why the wiring is asserted rather than assumed.
func TestInstallAutoCompact(t *testing.T) {
	enabled, disabled := true, false

	tests := []struct {
		desc        string
		sessionSvc  adksession.Service
		cfg         config.Config
		window      int64
		wantInstall bool
	}{
		{
			desc:        "file-backed session with a known window gets the hook",
			sessionSvc:  newFileService(t),
			cfg:         config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &enabled}},
			window:      200_000,
			wantInstall: true,
		},
		{
			desc:        "in-memory session has no stored transcript to rewrite",
			sessionSvc:  adksession.InMemoryService(),
			cfg:         config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &enabled}},
			window:      200_000,
			wantInstall: false,
		},
		{
			desc:        "no session service at all",
			sessionSvc:  nil,
			cfg:         config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &enabled}},
			window:      200_000,
			wantInstall: false,
		},
		{
			desc:        "disabled by config",
			sessionSvc:  newFileService(t),
			cfg:         config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &disabled}},
			window:      200_000,
			wantInstall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ag := newTestAgent(t)
			installAutoCompact(
				context.Background(),
				RuntimeConfig{SessionService: tt.sessionSvc},
				tt.cfg, ag, nil, newTracker(t, tt.window),
			)
			if got := ag.HasPreTurnHook(); got != tt.wantInstall {
				t.Errorf("HasPreTurnHook() = %v, want %v", got, tt.wantInstall)
			}
		})
	}
}

func newFileService(t *testing.T) *pisession.FileService {
	t.Helper()
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	return svc
}

var _ autocompact.ContextMeter = (*guardrail.Tracker)(nil)
