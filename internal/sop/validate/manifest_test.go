package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
)

// ruleManifestValid is the gate that decides whether a spec's validation record
// can be trusted. It had no test at all before this, and it is the only rule
// here whose input is a file on disk, so each branch needs its own fixture.
//
// The severity split is the part worth pinning: a missing manifest is a warning
// because hand-written specs are still runnable, while a manifest recording a
// failed validation, or one from a newer SOP, blocks.
func TestRuleManifestValid(t *testing.T) {
	specAt := func(dir string) *specdoc.Spec { return &specdoc.Spec{Dir: dir} }

	writeManifest := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	tests := []struct {
		name     string
		target   Target
		args     Args
		wantN    int
		wantSev  Severity
		wantIn   string
		skipFile bool
	}{
		{
			name:   "no spec is not a finding",
			target: Target{},
			wantN:  0,
		},
		{
			name:   "spec without a dir is not a finding",
			target: Target{Spec: &specdoc.Spec{}},
			wantN:  0,
		},
		{
			name:     "missing manifest warns rather than blocks",
			target:   Target{Spec: specAt(t.TempDir())},
			wantN:    1,
			wantSev:  SeverityWarn,
			wantIn:   "has not been validated",
			skipFile: true,
		},
		{
			name:    "unreadable manifest is a finding",
			target:  Target{Spec: specAt(writeManifest(t, "{not json"))},
			wantN:   1,
			wantSev: SeverityError,
			wantIn:  "unreadable",
		},
		{
			name:    "manifest from a newer SOP blocks",
			target:  Target{Spec: specAt(writeManifest(t, `{"sopVersion":9,"contract":"c","valid":true}`))},
			args:    Args{Named: map[string]string{"max_version": "3"}},
			wantN:   1,
			wantSev: SeverityError,
			wantIn:  "newer than this build's 3",
		},
		{
			name:    "manifest recording a failed validation blocks",
			target:  Target{Spec: specAt(writeManifest(t, `{"sopVersion":1,"contract":"my-contract","valid":false}`))},
			wantN:   1,
			wantSev: SeverityError,
			wantIn:  "my-contract",
		},
		{
			name:   "valid manifest passes",
			target: Target{Spec: specAt(writeManifest(t, `{"sopVersion":1,"contract":"c","valid":true}`))},
			wantN:  0,
		},
		{
			// max_version absent means "no ceiling", so a high recorded version
			// must not block on its own.
			name:   "no max_version means no version ceiling",
			target: Target{Spec: specAt(writeManifest(t, `{"sopVersion":999,"contract":"c","valid":true}`))},
			wantN:  0,
		},
		{
			name:   "max_version equal to the recorded one passes",
			target: Target{Spec: specAt(writeManifest(t, `{"sopVersion":3,"contract":"c","valid":true}`))},
			args:   Args{Named: map[string]string{"max_version": "3"}},
			wantN:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ruleManifestValid(tc.target, tc.args)
			if len(got) != tc.wantN {
				t.Fatalf("findings = %d, want %d: %+v", len(got), tc.wantN, got)
			}
			if tc.wantN == 0 {
				return
			}
			if got[0].Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", got[0].Severity, tc.wantSev)
			}
			if !strings.Contains(got[0].Message, tc.wantIn) {
				t.Errorf("message %q does not contain %q", got[0].Message, tc.wantIn)
			}
			// Every rule that reports must also say how to fix it, or the
			// reader is left with a block and no way out.
			if got[0].Fix == "" {
				t.Error("finding has no fix text")
			}
		})
	}
}
