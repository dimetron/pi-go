package attest

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The shape actions/attest-build-provenance emits for a tagged release.
const slsaV1Predicate = `{
  "buildDefinition": {
    "buildType": "https://actions.github.io/buildtypes/workflow/v1",
    "externalParameters": {
      "workflow": {
        "ref": "refs/tags/v0.0.74",
        "repository": "https://github.com/dimetron/pi-go",
        "path": ".github/workflows/release.yml"
      }
    },
    "resolvedDependencies": [
      {
        "uri": "git+https://github.com/dimetron/pi-go@refs/tags/v0.0.74",
        "digest": {"gitCommit": "4086645aa1f2c3d4e5f60718293a4b5c6d7e8f90"}
      }
    ]
  },
  "runDetails": {
    "builder": {"id": "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v0.0.74"},
    "metadata": {"invocationId": "https://github.com/dimetron/pi-go/actions/runs/1234/attempts/1"}
  }
}`

func TestParseProvenance(t *testing.T) {
	got, err := parseProvenance(json.RawMessage(slsaV1Predicate))
	if err != nil {
		t.Fatalf("parseProvenance: %v", err)
	}

	want := &Provenance{
		Repository: "github.com/dimetron/pi-go",
		Workflow:   ".github/workflows/release.yml",
		Ref:        "refs/tags/v0.0.74",
		Commit:     "4086645aa1f2c3d4e5f60718293a4b5c6d7e8f90",
		RunURL:     "https://github.com/dimetron/pi-go/actions/runs/1234/attempts/1",
		BuildType:  "https://actions.github.io/buildtypes/workflow/v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProvenance =\n%+v\nwant\n%+v", got, want)
	}
}

// A predicate missing the fields we read must not fail the whole
// verification — the signature was still valid, we just have less to show.
func TestParseProvenance_Sparse(t *testing.T) {
	got, err := parseProvenance(json.RawMessage(`{"buildDefinition":{}}`))
	if err != nil {
		t.Fatalf("parseProvenance: %v", err)
	}
	if got.Commit != "" || got.Workflow != "" {
		t.Errorf("expected empty fields, got %+v", got)
	}
}

func TestParseProvenance_Invalid(t *testing.T) {
	if _, err := parseProvenance(json.RawMessage(`not json`)); err == nil {
		t.Error("expected an error for a malformed predicate")
	}
	if _, err := parseProvenance(nil); err == nil {
		t.Error("expected an error for an empty predicate")
	}
}

const spdxPredicate = `{
  "spdxVersion": "SPDX-2.3",
  "name": "pi-go",
  "packages": [
    {"name": "a", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:golang/example.com/a@v1"}]},
    {"name": "b", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:golang/example.com/b@v2"}]},
    {"name": "c", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:github/actions/checkout@v7"}]},
    {"name": "d"}
  ]
}`

func TestParseSBOM_SPDX(t *testing.T) {
	got, err := parseSBOM("https://spdx.dev/Document/v2.3", json.RawMessage(spdxPredicate))
	if err != nil {
		t.Fatalf("parseSBOM: %v", err)
	}
	if got.Format != "SPDX" || got.Version != "2.3" {
		t.Errorf("format/version = %q/%q, want SPDX/2.3", got.Format, got.Version)
	}
	if got.Packages != 4 {
		t.Errorf("packages = %d, want 4", got.Packages)
	}
	want := map[string]int{"golang": 2, "github": 1}
	if !reflect.DeepEqual(got.Ecosystems, want) {
		t.Errorf("ecosystems = %v, want %v", got.Ecosystems, want)
	}
	if eco := got.EcosystemsSorted(); len(eco) != 2 || eco[0] != "golang 2" {
		t.Errorf("EcosystemsSorted = %v, want golang first", eco)
	}
}

func TestParseSBOM_CycloneDX(t *testing.T) {
	const doc = `{
      "specVersion": "1.5",
      "metadata": {"component": {"name": "pi"}},
      "components": [
        {"name": "a", "purl": "pkg:golang/example.com/a@v1"},
        {"name": "b", "purl": "pkg:golang/example.com/b@v2"}
      ]
    }`
	got, err := parseSBOM(PredicateCycloneDX, json.RawMessage(doc))
	if err != nil {
		t.Fatalf("parseSBOM: %v", err)
	}
	if got.Format != "CycloneDX" || got.Version != "1.5" || got.Packages != 2 {
		t.Errorf("got %+v", got)
	}
	if got.Name != "pi" {
		t.Errorf("name = %q, want pi", got.Name)
	}
}

func TestParseSBOM_Invalid(t *testing.T) {
	if _, err := parseSBOM(PredicateSPDXPrefix, nil); err == nil {
		t.Error("expected an error for an empty predicate")
	}
	if _, err := parseSBOM(PredicateCycloneDX, json.RawMessage(`{`)); err == nil {
		t.Error("expected an error for malformed CycloneDX")
	}
}

func TestPurlType(t *testing.T) {
	tests := map[string]string{
		"pkg:golang/example.com/a@v1": "golang",
		"pkg:github/actions/x@v1":     "github",
		"pkg:npm/left-pad@1":          "npm",
		"not-a-purl":                  "",
		"pkg:":                        "",
		"pkg:golang":                  "",
	}
	for in, want := range tests {
		if got := purlType(in); got != want {
			t.Errorf("purlType(%q) = %q, want %q", in, got, want)
		}
	}
}

// EcosystemsSorted must be deterministic; ranging a map is not.
func TestEcosystemsSorted_Deterministic(t *testing.T) {
	s := &SBOM{Ecosystems: map[string]int{"golang": 5, "github": 5, "npm": 9}}
	first := s.EcosystemsSorted()
	for i := 0; i < 20; i++ {
		if got := s.EcosystemsSorted(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d gave %v, first gave %v", i, got, first)
		}
	}
	// Ties break by name, so github precedes golang.
	want := []string{"npm 9", "github 5", "golang 5"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("EcosystemsSorted = %v, want %v", first, want)
	}
}

func TestEcosystemsSorted_Empty(t *testing.T) {
	if got := (&SBOM{}).EcosystemsSorted(); got != nil {
		t.Errorf("expected nil for an empty map, got %v", got)
	}
}
