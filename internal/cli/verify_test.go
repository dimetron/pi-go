package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/attest"
)

func provenanceReport() verifyReport {
	return verifyReport{
		Path:     "/usr/local/bin/pi",
		Digest:   "sha256:deadbeef",
		Repo:     attest.DefaultRepo,
		Verified: true,
		Results: []*attest.Result{
			{
				PredicateType:  attest.PredicateSLSAProvenanceV1,
				SignerIdentity: "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v0.0.74",
				SignedAt:       time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC),
				Provenance: &attest.Provenance{
					Repository: "github.com/dimetron/pi-go",
					Workflow:   ".github/workflows/release.yml",
					Ref:        "refs/tags/v0.0.74",
					Commit:     "4086645",
					RunURL:     "https://github.com/dimetron/pi-go/actions/runs/1234",
				},
			},
			{
				PredicateType:  "https://spdx.dev/Document/v2.3",
				SignerIdentity: "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v0.0.74",
				SBOM: &attest.SBOM{
					Format:     "SPDX",
					Version:    "2.3",
					Packages:   192,
					Ecosystems: map[string]int{"golang": 180, "github": 12},
				},
			},
		},
	}
}

func TestWriteVerifyText_Verified(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, []verifyReport{provenanceReport()})
	out := buf.String()

	for _, want := range []string{
		"/usr/local/bin/pi",
		"sha256:deadbeef",
		"✓ build provenance",
		"github.com/dimetron/pi-go",
		".github/workflows/release.yml@refs/tags/v0.0.74",
		"4086645",
		"✓ SBOM",
		"SPDX 2.3",
		"192",
		"golang 180, github 12",
		"2026-08-21T12:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "✗") {
		t.Errorf("a verified report should not render a failure marker:\n%s", out)
	}
}

// The common case for someone running `pi verify` on their own build must read
// as an explanation, not as an alarm.
func TestWriteVerifyText_NoAttestation(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, []verifyReport{{
		Path:          "/tmp/pi",
		Digest:        "sha256:abc",
		Repo:          attest.DefaultRepo,
		Error:         attest.ErrNoAttestations.Error(),
		noAttestation: true,
	}})
	out := buf.String()

	if !strings.Contains(out, "✗ unverified") {
		t.Errorf("expected a failure marker:\n%s", out)
	}
	if !strings.Contains(out, "built yourself") {
		t.Errorf("expected the explanatory note:\n%s", out)
	}
}

// A cryptographic failure must not be softened with the "you built it
// yourself" note.
func TestWriteVerifyText_VerificationFailure(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, []verifyReport{{
		Path:   "/tmp/pi",
		Digest: "sha256:abc",
		Repo:   attest.DefaultRepo,
		Error:  "certificate identity does not match",
	}})
	out := buf.String()

	if !strings.Contains(out, "certificate identity does not match") {
		t.Errorf("expected the underlying error:\n%s", out)
	}
	if strings.Contains(out, "built yourself") {
		t.Errorf("a crypto failure must not be explained away as a local build:\n%s", out)
	}
}

func TestWriteVerifyText_MultipleTargets(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, []verifyReport{
		provenanceReport(),
		{Path: "/tmp/other", Digest: "sha256:f00", Repo: attest.DefaultRepo, Error: "boom"},
	})
	out := buf.String()
	if !strings.Contains(out, "/usr/local/bin/pi") || !strings.Contains(out, "/tmp/other") {
		t.Errorf("both targets should appear:\n%s", out)
	}
}

func TestWriteSBOMDocuments(t *testing.T) {
	r := provenanceReport()
	r.Results[1].Predicate = json.RawMessage(`{"spdxVersion":"SPDX-2.3","packages":[]}`)

	var buf bytes.Buffer
	if err := writeSBOMDocuments(&buf, []verifyReport{r}); err != nil {
		t.Fatalf("writeSBOMDocuments: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if doc["spdxVersion"] != "SPDX-2.3" {
		t.Errorf("unexpected document: %v", doc)
	}
}

// --sbom must refuse to emit a document it could not verify; the whole point
// of the flag is that what it prints was signed.
func TestWriteSBOMDocuments_RefusesUnverified(t *testing.T) {
	err := writeSBOMDocuments(&bytes.Buffer{}, []verifyReport{{
		Path: "/tmp/pi", Error: "nope",
	}})
	if err == nil {
		t.Fatal("expected an error for an unverified target")
	}
}

func TestWriteSBOMDocuments_NoSBOM(t *testing.T) {
	r := provenanceReport()
	r.Results = r.Results[:1] // provenance only
	if err := writeSBOMDocuments(&bytes.Buffer{}, []verifyReport{r}); err == nil {
		t.Fatal("expected an error when no SBOM attestation is present")
	}
}

func TestJoinRef(t *testing.T) {
	tests := []struct{ workflow, ref, want string }{
		{"a.yml", "refs/tags/v1", "a.yml@refs/tags/v1"},
		{"a.yml", "", "a.yml"},
		{"", "refs/tags/v1", "refs/tags/v1"},
		{"", "", ""},
	}
	for _, tt := range tests {
		if got := joinRef(tt.workflow, tt.ref); got != tt.want {
			t.Errorf("joinRef(%q,%q) = %q, want %q", tt.workflow, tt.ref, got, tt.want)
		}
	}
}

func TestGithubTokenFromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if got := githubTokenFromEnv(); got != "" {
		t.Errorf("expected no token, got %q", got)
	}
	t.Setenv("GH_TOKEN", "  from-gh  ")
	if got := githubTokenFromEnv(); got != "from-gh" {
		t.Errorf("token = %q, want from-gh", got)
	}
	t.Setenv("GITHUB_TOKEN", "from-github")
	if got := githubTokenFromEnv(); got != "from-github" {
		t.Errorf("GITHUB_TOKEN should win, got %q", got)
	}
}

func TestNewVerifyCmd_Flags(t *testing.T) {
	cmd := newVerifyCmd()
	if cmd.Use != "verify [file...]" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if !cmd.SilenceUsage {
		t.Error("a failed verification should not print the usage block")
	}
	if f := cmd.Flags().Lookup("repo"); f == nil || f.DefValue != attest.DefaultRepo {
		t.Errorf("--repo should default to %s", attest.DefaultRepo)
	}
	for _, name := range []string{"json", "sbom"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}
