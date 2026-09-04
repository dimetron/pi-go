package autocompact

import (
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/testenv"
)

// TestAutoCompactConfigFrom covers the config.Config -> AutoCompactConfig
// mapping. Every field is guarded by its own zero-check, so a wrong guard
// silently keeps the default and the user's setting is dropped without any
// error — exactly the failure this table is here to catch.
func TestAutoCompactConfigFrom(t *testing.T) {
	t.Parallel()

	defaults := pisession.DefaultAutoCompactConfig()
	enabled, disabled := true, false

	cases := []struct {
		desc string
		in   config.Config
		want pisession.AutoCompactConfig
	}{
		{
			desc: "nil AutoCompact yields untouched defaults",
			in:   config.Config{},
			want: defaults,
		},
		{
			desc: "empty AutoCompact yields untouched defaults",
			in:   config.Config{AutoCompact: &config.AutoCompactConfig{}},
			want: defaults,
		},
		{
			desc: "Enabled=false is honored, not treated as unset",
			in:   config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &disabled}},
			want: withEnabled(defaults, false),
		},
		{
			desc: "Enabled=true is honored",
			in:   config.Config{AutoCompact: &config.AutoCompactConfig{Enabled: &enabled}},
			want: withEnabled(defaults, true),
		},
		{
			desc: "every numeric field overrides its default",
			in: config.Config{AutoCompact: &config.AutoCompactConfig{
				Enabled:               &enabled,
				ShedPercent:           41,
				SummarizePercent:      77,
				KeepUserMessageTokens: 1234,
				KeepRecentEvents:      9,
			}},
			want: pisession.AutoCompactConfig{
				Enabled:               true,
				ShedPercent:           41,
				SummarizePercent:      77,
				KeepUserMessageTokens: 1234,
				KeepRecentEvents:      9,
			},
		},
		{
			desc: "zero numerics fall back to defaults rather than zeroing the config",
			in: config.Config{AutoCompact: &config.AutoCompactConfig{
				ShedPercent:           0,
				SummarizePercent:      0,
				KeepUserMessageTokens: 0,
				KeepRecentEvents:      0,
			}},
			want: defaults,
		},
		{
			desc: "negative numerics are ignored like zero",
			in: config.Config{AutoCompact: &config.AutoCompactConfig{
				ShedPercent:      -5,
				KeepRecentEvents: -1,
			}},
			want: defaults,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			got := ConfigFrom(tc.in)
			if got != tc.want {
				t.Fatalf("ConfigFrom() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func withEnabled(c pisession.AutoCompactConfig, v bool) pisession.AutoCompactConfig {
	c.Enabled = v
	return c
}

// newTempLogger builds a logger rooted in a temp HOME so the test never
// appends to the developer's real ~/.pi-go/log tree.
func newTempLogger(t *testing.T) *logger.Logger {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	l, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// TestAutoCompactDepsReport covers the notification fan-out. Both sinks are
// optional, and compaction discarding history silently is the outcome the
// Notify hook exists to prevent, so the nil cases matter as much as the
// wired ones.
//
// Not parallel: the Log cases need t.Setenv to redirect HOME, which panics if
// this test or any parent has called t.Parallel.
func TestAutoCompactDepsReport(t *testing.T) {
	t.Run("both sinks nil does not panic", func(t *testing.T) {
		Deps{}.report("no sinks")
	})

	t.Run("Notify receives the message verbatim", func(t *testing.T) {
		var got []string
		d := Deps{Notify: func(m string) { got = append(got, m) }}
		d.report("shed 3 results")
		if len(got) != 1 || got[0] != "shed 3 results" {
			t.Fatalf("Notify got %q, want exactly [\"shed 3 results\"]", got)
		}
	})

	// The Log sink writes to ~/.pi-go/log, so these point HOME at a temp dir.
	t.Run("Log alone does not panic and does not need Notify", func(t *testing.T) {
		d := Deps{Log: newTempLogger(t)}
		d.report("logged only")
	})

	t.Run("both sinks each receive the message", func(t *testing.T) {
		var got []string
		d := Deps{
			Log:    newTempLogger(t),
			Notify: func(m string) { got = append(got, m) },
		}
		d.report("both")
		if len(got) != 1 || got[0] != "both" {
			t.Fatalf("Notify got %q, want exactly [\"both\"]", got)
		}
	})
}
