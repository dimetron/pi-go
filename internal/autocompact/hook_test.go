package autocompact

import (
	"context"
	"strings"
	"testing"

	pisession "github.com/dimetron/pi-go/internal/session"
)

// TestBuildHookDeclines covers the shapes that cannot compact at all, where
// returning nil lets a caller install the result unconditionally.
func TestBuildHookDeclines(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	on := pisession.DefaultAutoCompactConfig()
	on.Enabled = true
	off := on
	off.Enabled = false

	tests := []struct {
		desc     string
		deps     Deps
		wantHook bool
	}{
		{desc: "nothing wired", deps: Deps{}},
		{desc: "no session service", deps: Deps{Tracker: NewMeter(), Cfg: on}},
		{desc: "no meter", deps: Deps{SessionSvc: svc, Cfg: on}},
		{desc: "disabled by config", deps: Deps{SessionSvc: svc, Tracker: NewMeter(), Cfg: off}},
		{desc: "fully wired", deps: Deps{SessionSvc: svc, Tracker: NewMeter(), Cfg: on}, wantHook: true},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			if got := BuildHook(tt.deps) != nil; got != tt.wantHook {
				t.Errorf("BuildHook() non-nil = %v, want %v", got, tt.wantHook)
			}
		})
	}
}

// TestHookReportsUnknownWindowOnce covers the state that used to be silent: a
// model whose context window pi-go cannot resolve makes compaction inert, and
// a session can then grow until the provider rejects it with nothing having
// said so. The notice fires once, not once per turn, or it would bury itself.
func TestHookReportsUnknownWindowOnce(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	cfg := pisession.DefaultAutoCompactConfig()
	cfg.Enabled = true

	meter := NewMeter() // window deliberately left unknown
	meter.Observe(500_000)

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Cfg:        cfg,
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil for a fully-wired Deps")
	}

	for range 5 {
		if err := hook(context.Background(), "session-1"); err != nil {
			t.Fatalf("hook: %v", err)
		}
	}

	if len(notices) != 1 {
		t.Fatalf("got %d notices %q, want exactly 1", len(notices), notices)
	}
	if !strings.Contains(notices[0], "context window unknown") {
		t.Errorf("notice = %q, want it to name the unknown context window", notices[0])
	}
}
