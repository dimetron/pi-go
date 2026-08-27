package sop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// ManifestName is the machine-readable record a validated spec carries.
const ManifestName = ".sop-manifest.json"

// SOPVersion is the contract version this build writes and understands. A spec
// planned under a newer version must fail loudly rather than be executed on
// assumptions that no longer hold.
const SOPVersion = 2

// Manifest records what was checked, by which contract, and with what result.
// /run reads it instead of re-deriving whether a spec is sound, and a spec
// whose manifest is absent or stale is revalidated rather than trusted.
type Manifest struct {
	SOPVersion  int                `json:"sopVersion"`
	Spec        string             `json:"spec"`
	Contract    string             `json:"contract"`
	ValidatedAt time.Time          `json:"validatedAt"`
	Artifacts   []ArtifactRecord   `json:"artifacts"`
	Rules       []string           `json:"rules"`
	Findings    []validate.Finding `json:"findings,omitempty"`
	Valid       bool               `json:"valid"`
}

// ArtifactRecord is one artifact's state at validation time.
type ArtifactRecord struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Lines   int    `json:"lines,omitempty"`
	Slices  int    `json:"slices,omitempty"`
}

// BuildManifest validates spec against contract and returns the record.
func BuildManifest(spec *specdoc.Spec, repoRoot string, contract validate.Contract, now time.Time) *Manifest {
	findings := validate.Check(spec, repoRoot, contract)

	m := &Manifest{
		SOPVersion:  SOPVersion,
		Spec:        spec.Name,
		Contract:    contract.Name,
		ValidatedAt: now.UTC(),
		Findings:    findings,
		Valid:       findings.OK(),
	}
	for _, ac := range contract.Artifacts {
		m.Rules = append(m.Rules, ac.Rules...)
		rec := ArtifactRecord{Name: ac.Artifact, Present: spec.Has(ac.Artifact)}
		if rec.Present {
			content := spec.Files[ac.Artifact]
			rec.Lines = specdoc.CountLines(content)
			switch ac.Artifact {
			case specdoc.Plan:
				rec.Slices = len(specdoc.ParsePlanSlices(content))
			case specdoc.Prompt:
				rec.Slices = len(specdoc.ParsePromptSlices(content))
			}
		}
		m.Artifacts = append(m.Artifacts, rec)
	}
	return m
}

// WriteManifest writes the manifest into the spec directory.
func WriteManifest(specDir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	path := filepath.Join(specDir, ManifestName)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", ManifestName, err)
	}
	return nil
}

// ReadManifest loads the manifest from a spec directory. A missing manifest is
// reported as an error so the caller can choose to revalidate rather than
// silently treat an unvalidated spec as valid.
func ReadManifest(specDir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(specDir, ManifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestName, err)
	}
	return &m, nil
}

// Compatible reports whether a manifest was written by a contract version this
// build can rely on. A newer manifest is not compatible: it may assert rules
// this build does not implement.
func (m *Manifest) Compatible() bool {
	return m != nil && m.SOPVersion > 0 && m.SOPVersion <= SOPVersion
}
