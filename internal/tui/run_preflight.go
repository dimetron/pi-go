package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// runPreflight checks a spec before /run spawns anything.
//
// It exists because the only pre-run check used to be "PROMPT.md exists". A
// spec with placeholder gates, slices that name no files, or a PROMPT.md
// describing a different set of slices than plan.md tracks would start a run
// that could not succeed — and the failure surfaced forty minutes later as a
// worker with an unrunnable verify command rather than as a planning defect.
//
// It returns the findings and whether the run may proceed. Warnings are
// reported and do not block.
func (m *model) runPreflight(specName string) (validate.Findings, bool) {
	spec, err := specdoc.Load(m.cfg.WorkDir, specName)
	if err != nil {
		return validate.Findings{{
			Artifact: specName, Rule: "spec", Severity: validate.SeverityError,
			Message: err.Error(),
		}}, false
	}
	findings := validate.Check(spec, m.cfg.WorkDir, validate.RunPreflightContract())
	return findings, findings.OK()
}

// formatPreflightBlock renders the message shown when preflight refuses to
// start a run. It names the override so the decision stays the user's.
func formatPreflightBlock(specName string, findings validate.Findings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Preflight failed** for spec `%s` — not starting the run.\n\n", specName)
	b.WriteString(findings.Format())
	b.WriteString("\nFix the spec (or re-plan it) and run again. " +
		"To start anyway, use `/run " + specName + " --force`.\n")
	return b.String()
}

// formatPreflightWarnings renders non-blocking findings.
func formatPreflightWarnings(findings validate.Findings) string {
	var b strings.Builder
	b.WriteString("**Preflight warnings** — starting anyway:\n\n")
	b.WriteString(findings.Format())
	return b.String()
}

// writeRunManifest records the preflight result in the spec directory so a
// later run, or a human, can see what was checked and when.
func (m *model) writeRunManifest(specName string) {
	spec, err := specdoc.Load(m.cfg.WorkDir, specName)
	if err != nil {
		return
	}
	manifest := sop.BuildManifest(spec, m.cfg.WorkDir, validate.RunPreflightContract(), time.Now())
	_ = sop.WriteManifest(spec.Dir, manifest)
}

// preflightAllows runs preflight, reports the outcome, records the manifest,
// and reports whether the run may start. force downgrades a blocking result to
// a warning so the user keeps the final say.
func (m *model) preflightAllows(specName string, force bool) bool {
	findings, ok := m.runPreflight(specName)
	switch {
	case !ok && !force:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: formatPreflightBlock(specName, findings),
		})
		return false
	case !ok:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "**Preflight failed but `--force` was given** — starting anyway.\n\n" + findings.Format(),
		})
	case len(findings) > 0:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: formatPreflightWarnings(findings),
		})
	}
	m.writeRunManifest(specName)
	return true
}
