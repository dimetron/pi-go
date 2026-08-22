package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/attest"
)

// These tests pin the exact bytes writeVerifyText produces — `pi verify` is a
// security-reporting command, so the difference between "✓" and "✗", and the
// no-attestation explanation that separates a locally built binary from a real
// verification failure, are the output. Every golden below was captured by
// running the same reports through the pre-refactor writeVerifyText, so a
// passing run proves the flattening changed nothing a user sees.

var cogVerifySignedAt = time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

// cogVerifyFullReport is a fully populated success: one provenance
// attestation with every field set, and one SBOM attestation with ecosystems
// and no signing time.
func cogVerifyFullReport() verifyReport {
	return verifyReport{
		Path:     "/usr/local/bin/pi",
		Digest:   "sha256:deadbeef",
		Repo:     "dimetron/pi-go",
		Verified: true,
		Results: []*attest.Result{
			{
				PredicateType:  "https://slsa.dev/provenance/v1",
				SignerIdentity: "signer-a",
				SignedAt:       cogVerifySignedAt,
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
				SignerIdentity: "signer-b",
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

func TestWriteVerifyTextGoldenOutput(t *testing.T) {
	tests := []struct {
		name    string
		reports []verifyReport
		want    string
	}{
		{
			name:    "provenance and SBOM, every field populated",
			reports: []verifyReport{cogVerifyFullReport()},
			want: "/usr/local/bin/pi\n  sha256:deadbeef\n" +
				"\n  ✓ build provenance\n" +
				"      repository  github.com/dimetron/pi-go\n" +
				"      workflow    .github/workflows/release.yml@refs/tags/v0.0.74\n" +
				"      commit      4086645\n" +
				"      run         https://github.com/dimetron/pi-go/actions/runs/1234\n" +
				"      signer      signer-a\n" +
				"      signed      2026-08-21T12:00:00Z\n" +
				"\n  ✓ SBOM\n" +
				"      format      SPDX 2.3\n" +
				"      packages    192\n" +
				"      ecosystems  golang 180, github 12\n" +
				"      signer      signer-b\n",
		},
		{
			name: "no attestation carries the locally-built explanation",
			reports: []verifyReport{{
				Path: "./pi", Digest: "sha256:abc", Repo: "dimetron/pi-go",
				Error: "no attestations found", noAttestation: true,
			}},
			want: "./pi\n  sha256:abc\n" +
				"\n  ✗ unverified: no attestations found\n" +
				"\n    dimetron/pi-go holds no attestation for these bytes. A binary you\n" +
				"    built yourself never has one, and neither does one from a\n" +
				"    release made before the workflow started attesting.\n",
		},
		{
			name: "a real verification failure gets no explanation block",
			reports: []verifyReport{{
				Path: "./pi", Digest: "sha256:abc", Repo: "dimetron/pi-go", Error: "boom",
			}},
			want: "./pi\n  sha256:abc\n\n  ✗ unverified: boom\n",
		},
		{
			name:    "a missing digest line is omitted",
			reports: []verifyReport{{Path: "./pi", Repo: "r", Error: "digest failed"}},
			want:    "./pi\n\n  ✗ unverified: digest failed\n",
		},
		{
			name: "a blank line separates consecutive targets",
			reports: []verifyReport{
				{Path: "a", Digest: "sha256:1", Repo: "r", Error: "e1"},
				{Path: "b", Digest: "sha256:2", Repo: "r", Error: "e2"},
			},
			want: "a\n  sha256:1\n\n  ✗ unverified: e1\n" +
				"\nb\n  sha256:2\n\n  ✗ unverified: e2\n",
		},
		{
			name: "an attestation that is neither provenance nor SBOM prints its predicate type",
			reports: []verifyReport{{
				Path: "x", Digest: "sha256:9", Repo: "r", Verified: true,
				Results: []*attest.Result{{
					PredicateType:  "https://example.com/custom/v1",
					SignerIdentity: "sid",
					SignedAt:       cogVerifySignedAt,
				}},
			}},
			want: "x\n  sha256:9\n\n  ✓ https://example.com/custom/v1\n" +
				"      signer      sid\n" +
				"      signed      2026-08-21T12:00:00Z\n",
		},
		{
			name: "an SBOM without ecosystems omits that field, and a zero package count is still printed",
			reports: []verifyReport{{
				Path: "x", Digest: "sha256:9", Repo: "r", Verified: true,
				Results: []*attest.Result{{
					PredicateType: "p",
					SBOM:          &attest.SBOM{Format: "SPDX", Packages: 0},
				}},
			}},
			want: "x\n  sha256:9\n\n  ✓ SBOM\n      format      SPDX\n      packages    0\n",
		},
		{
			name: "provenance with only a workflow prints one field",
			reports: []verifyReport{{
				Path: "x", Repo: "r", Verified: true,
				Results: []*attest.Result{{PredicateType: "p", Provenance: &attest.Provenance{Workflow: "wf"}}},
			}},
			want: "x\n\n  ✓ build provenance\n      workflow    wf\n",
		},
		{
			name: "provenance with only a ref prints the ref under the workflow label",
			reports: []verifyReport{{
				Path: "x", Repo: "r", Verified: true,
				Results: []*attest.Result{{PredicateType: "p", Provenance: &attest.Provenance{Ref: "refs/heads/main"}}},
			}},
			want: "x\n\n  ✓ build provenance\n      workflow    refs/heads/main\n",
		},
		{
			name:    "a verified report with no attestations prints only its header",
			reports: []verifyReport{{Path: "x", Digest: "sha256:9", Repo: "r", Verified: true}},
			want:    "x\n  sha256:9\n",
		},
		{
			name:    "no reports produce no output",
			reports: []verifyReport{},
			want:    "",
		},
		{
			name:    "a nil report slice produces no output",
			reports: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeVerifyText(&buf, tt.reports)
			if got := buf.String(); got != tt.want {
				t.Errorf("writeVerifyText output mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// cogVerifyFailingWriter fails every write, so the SBOM dump's I/O error path
// can be exercised without a real broken pipe.
type cogVerifyFailingWriter struct{}

func (cogVerifyFailingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// TestWriteSBOMDocumentsPaths pins each outcome of the SBOM dump: the bytes it
// writes on success, the two error paths, and the two ways a report can
// contribute no document.
func TestWriteSBOMDocumentsPaths(t *testing.T) {
	sbomReport := func(pred string) verifyReport {
		return verifyReport{
			Path: "./pi", Repo: "r", Verified: true,
			Results: []*attest.Result{{
				PredicateType: "https://spdx.dev/Document/v2.3",
				SBOM:          &attest.SBOM{Format: "SPDX", Version: "2.3"},
				Predicate:     json.RawMessage(pred),
			}},
		}
	}

	t.Run("a valid predicate is re-indented and newline terminated", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeSBOMDocuments(&buf, []verifyReport{sbomReport(`{"a":1}`)}); err != nil {
			t.Fatalf("writeSBOMDocuments: %v", err)
		}
		want := "{\n  \"a\": 1\n}\n"
		if got := buf.String(); got != want {
			t.Errorf("output = %q, want %q", got, want)
		}
	})

	t.Run("two reports each contribute their document", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeSBOMDocuments(&buf, []verifyReport{sbomReport(`{"a":1}`), sbomReport(`{"b":2}`)}); err != nil {
			t.Fatalf("writeSBOMDocuments: %v", err)
		}
		want := "{\n  \"a\": 1\n}\n{\n  \"b\": 2\n}\n"
		if got := buf.String(); got != want {
			t.Errorf("output = %q, want %q", got, want)
		}
	})

	t.Run("an unverified report is refused before anything is written", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeSBOMDocuments(&buf, []verifyReport{{Path: "./pi", Error: "boom"}})
		if err == nil || err.Error() != "./pi: boom" {
			t.Fatalf("error = %v, want \"./pi: boom\"", err)
		}
		if buf.Len() != 0 {
			t.Errorf("wrote %q, want nothing", buf.String())
		}
	})

	t.Run("an SBOM whose raw predicate was dropped contributes nothing", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeSBOMDocuments(&buf, []verifyReport{sbomReport("")})
		if err == nil || err.Error() != "no SBOM attestation found for the given artifacts" {
			t.Fatalf("error = %v, want the no-SBOM error", err)
		}
	})

	t.Run("a non-SBOM attestation contributes nothing", func(t *testing.T) {
		var buf bytes.Buffer
		report := verifyReport{Path: "./pi", Verified: true, Results: []*attest.Result{{
			PredicateType: "https://slsa.dev/provenance/v1",
			Provenance:    &attest.Provenance{Repository: "r"},
			Predicate:     json.RawMessage(`{"a":1}`),
		}}}
		err := writeSBOMDocuments(&buf, []verifyReport{report})
		if err == nil || err.Error() != "no SBOM attestation found for the given artifacts" {
			t.Fatalf("error = %v, want the no-SBOM error", err)
		}
	})

	t.Run("an unmarshalable predicate is reported as a formatting error", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeSBOMDocuments(&buf, []verifyReport{sbomReport(`{not json`)})
		if err == nil || !strings.HasPrefix(err.Error(), "formatting SBOM: ") {
			t.Fatalf("error = %v, want a formatting SBOM error", err)
		}
	})

	t.Run("a write failure is returned unwrapped", func(t *testing.T) {
		err := writeSBOMDocuments(cogVerifyFailingWriter{}, []verifyReport{sbomReport(`{"a":1}`)})
		if err == nil || err.Error() != "disk full" {
			t.Fatalf("error = %v, want \"disk full\"", err)
		}
	})
}
