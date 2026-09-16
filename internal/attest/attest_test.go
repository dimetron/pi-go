package attest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	in_toto "github.com/in-toto/attestation/go/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestFileDigest_KnownVector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FileDigest(path)
	if err != nil {
		t.Fatalf("FileDigest: %v", err)
	}
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != emptySHA256 {
		t.Errorf("digest of empty file = %s, want %s", got, emptySHA256)
	}
}

func TestFileDigest_Missing(t *testing.T) {
	if _, err := FileDigest(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// TestWorkflowSANPattern pins the identity policy. Every case here is a
// certificate an attacker could plausibly obtain from Fulcio by running a
// workflow they control; only the first should satisfy the policy.
func TestWorkflowSANPattern(t *testing.T) {
	re, err := workflowSANPattern(DefaultRepo)
	if err != nil {
		t.Fatalf("workflowSANPattern: %v", err)
	}

	tests := []struct {
		name string
		san  string
		want bool
	}{
		{
			name: "release workflow on a tag",
			san:  "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v0.0.74",
			want: true,
		},
		{
			name: "a different workflow in the same repo",
			san:  "https://github.com/dimetron/pi-go/.github/workflows/ci.yml@refs/tags/v0.0.74",
			want: false,
		},
		{
			name: "release workflow on a branch, not a tag",
			san:  "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/heads/main",
			want: false,
		},
		{
			name: "a repo whose name merely starts with ours",
			san:  "https://github.com/dimetron/pi-go-evil/.github/workflows/release.yml@refs/tags/v1",
			want: false,
		},
		{
			name: "our path nested under someone else's repo",
			san:  "https://github.com/evil/repo/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v1",
			want: false,
		},
		{
			name: "a lookalike host",
			san:  "https://github.com.evil.example/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v1",
			want: false,
		},
		{
			name: "trailing content after the tag",
			san:  "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v1\nhttps://evil",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := re.MatchString(tt.san); got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.san, got, tt.want)
			}
		})
	}
}

func TestWorkflowSANPattern_BadRepo(t *testing.T) {
	for _, repo := range []string{"", "noslash", "too/many/slashes"} {
		if _, err := workflowSANPattern(repo); err == nil {
			t.Errorf("workflowSANPattern(%q) succeeded, want an error", repo)
		}
	}
}

// fetchServer serves one canned response from the attestations endpoint.
func fetchServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Accept"), "application/vnd.github+json"; got != want {
			t.Errorf("Accept header = %q, want %q", got, want)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch_NotFound(t *testing.T) {
	srv := fetchServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	f := &Fetcher{BaseURL: srv.URL}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if !errors.Is(err, ErrNoAttestations) {
		t.Fatalf("Fetch error = %v, want ErrNoAttestations", err)
	}
}

func TestFetch_EmptyList(t *testing.T) {
	srv := fetchServer(t, http.StatusOK, `{"attestations":[]}`)
	f := &Fetcher{BaseURL: srv.URL}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if !errors.Is(err, ErrNoAttestations) {
		t.Fatalf("Fetch error = %v, want ErrNoAttestations", err)
	}
}

// A bundle this package cannot parse must not mask the ones it can — the same
// endpoint also serves attestations GitHub generates itself.
func TestFetch_SkipsUnparseableBundles(t *testing.T) {
	good, err := os.ReadFile(filepath.Join("testdata", "bundle-provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"attestations": []map[string]json.RawMessage{
			{"bundle": json.RawMessage(`{"mediaType":"nonsense"}`)},
			{"bundle": json.RawMessage(good)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := fetchServer(t, http.StatusOK, string(body))
	f := &Fetcher{BaseURL: srv.URL}

	bundles, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("got %d bundles, want 1 (the unparseable one should be dropped)", len(bundles))
	}
}

func TestFetch_ServerError(t *testing.T) {
	srv := fetchServer(t, http.StatusInternalServerError, `{}`)
	f := &Fetcher{BaseURL: srv.URL}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if err == nil || errors.Is(err, ErrNoAttestations) {
		t.Fatalf("Fetch error = %v, want a transport error", err)
	}
}

func TestFetch_SendsTokenAndAgent(t *testing.T) {
	var gotAuth, gotAgent, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAgent, gotPath = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.URL.Path
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := &Fetcher{BaseURL: srv.URL, Token: "s3cret", UserAgent: "pi-go/test"}
	_, _ = f.Fetch(context.Background(), "owner/name", "deadbeef")

	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAgent != "pi-go/test" {
		t.Errorf("User-Agent = %q", gotAgent)
	}
	if want := "/repos/owner/name/attestations/sha256:deadbeef"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestVerify_RealBundle runs the full Sigstore verification path — certificate
// chain, transparency-log inclusion, signed certificate timestamp and
// observer timestamps — against a real public-good bundle and a pinned trust
// root, with no network access. The bundle is sigstore-js's own npm release
// provenance, shipped as an example by sigstore-go.
//
// Its subject digest is SHA-512 and its SAN names a branch rather than a tag,
// so the artifact and identity policies are supplied here instead of the ones
// NewVerifierWithMaterial builds; everything else is the production path.
func TestVerify_RealBundle(t *testing.T) {
	material := loadPinnedTrustRoot(t)
	b := loadFixtureBundle(t)

	v, err := NewVerifierWithMaterial("sigstore/sigstore-js", material)
	if err != nil {
		t.Fatalf("NewVerifierWithMaterial: %v", err)
	}
	// The fixture was signed from refs/heads/main, which the production
	// pattern deliberately rejects.
	v.sanRE = regexp.MustCompile(`^https://github\.com/sigstore/sigstore-js/`)

	res, err := verifyStatementOnly(v, b)
	if err != nil {
		t.Fatalf("verifying a known-good bundle failed: %v", err)
	}
	if res.PredicateType == "" {
		t.Error("verified result carries no predicate type")
	}
	if res.SignerIdentity == "" {
		t.Error("verified result carries no signer identity")
	}
	if res.SignedAt.IsZero() {
		t.Error("verified result carries no signing timestamp")
	}
}

// TestVerify_RejectsWrongIdentity proves the identity policy is load-bearing:
// the same known-good bundle must fail when the policy names another repo.
func TestVerify_RejectsWrongIdentity(t *testing.T) {
	material := loadPinnedTrustRoot(t)
	b := loadFixtureBundle(t)

	v, err := NewVerifierWithMaterial(DefaultRepo, material)
	if err != nil {
		t.Fatalf("NewVerifierWithMaterial: %v", err)
	}

	if _, err := verifyStatementOnly(v, b); err == nil {
		t.Fatal("a bundle signed by another repository's workflow verified against pi-go's policy")
	}
}

// verifyStatementOnly runs the production verifier and policy but drops the
// artifact-digest binding, so a fixture whose subject is hashed with SHA-512
// can still exercise signature, transparency-log and identity verification.
// Production code never takes this path: Verify always binds a digest.
func verifyStatementOnly(v *Verifier, b *bundle.Bundle) (*Result, error) {
	certID, err := verify.NewShortCertificateIdentity(oidcIssuer, "", "", v.sanRE.String())
	if err != nil {
		return nil, err
	}
	res, err := v.verifier.Verify(b, verify.NewPolicy(
		verify.WithoutArtifactUnsafe(),
		verify.WithCertificateIdentity(certID),
	))
	if err != nil {
		return nil, err
	}
	return newResult(res)
}

func loadPinnedTrustRoot(t *testing.T) root.TrustedMaterial {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "trusted-root-public-good.json"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := root.NewTrustedRootFromJSON(raw)
	if err != nil {
		t.Fatalf("loading pinned trust root: %v", err)
	}
	return root.TrustedMaterialCollection{tr}
}

func loadFixtureBundle(t *testing.T) *bundle.Bundle {
	t.Helper()
	b, err := bundle.LoadJSONFromPath(filepath.Join("testdata", "bundle-provenance.json"))
	if err != nil {
		t.Fatalf("loading fixture bundle: %v", err)
	}
	return b
}

// TestTUFCacheDir pins the TUF cache location under the user's own state dir.
// The test is not parallel: it mutates process environment.
func TestTUFCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows reads this, not HOME.

	got, err := TUFCacheDir()
	if err != nil {
		t.Fatalf("TUFCacheDir: %v", err)
	}
	want := filepath.Join(home, ".pi-go", "sigstore")
	if got != want {
		t.Errorf("TUFCacheDir() = %q, want %q", got, want)
	}
}

// TestVerify_InvalidDigest covers input rejection before any cryptographic
// work: non-hex and odd-length digests must fail fast.
func TestVerify_InvalidDigest(t *testing.T) {
	// A verifier is needed only for the digest-decode step; any valid one works.
	material := loadPinnedTrustRoot(t)
	v, err := NewVerifierWithMaterial(DefaultRepo, material)
	if err != nil {
		t.Fatalf("NewVerifierWithMaterial: %v", err)
	}

	for _, digest := range []string{"zzz", "abc", "123"} {
		if _, err := v.Verify(nil, digest); err == nil {
			t.Errorf("Verify with digest %q succeeded, want invalid-digest error", digest)
		} else if !strings.Contains(err.Error(), "invalid digest") {
			t.Errorf("Verify(%q) error = %q, want \"invalid digest\"", digest, err)
		}
	}
}

// TestNewVerifierWithMaterial_BadRepo: an invalid owner/name form must fail at
// construction, before any trust material is consulted.
func TestNewVerifierWithMaterial_BadRepo(t *testing.T) {
	material := loadPinnedTrustRoot(t)
	for _, repo := range []string{"", "noslash", "too/many/slashes"} {
		if _, err := NewVerifierWithMaterial(repo, material); err == nil {
			t.Errorf("NewVerifierWithMaterial(%q) succeeded, want an error", repo)
		}
	}
}

// ---- coverage additions ----

// TestFileDigest_Directory: a path that opens but cannot be read (a
// directory) must surface as a hashing error, not a panic or a silent "".
func TestFileDigest_Directory(t *testing.T) {
	if _, err := FileDigest(t.TempDir()); err == nil || !strings.Contains(err.Error(), "hashing") {
		t.Errorf("FileDigest(dir) error = %v, want a hashing error", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper, so fetch behavior
// can be driven without any network at all.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetch_DefaultBaseURL(t *testing.T) {
	var gotHost string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
	})
	f := &Fetcher{HTTPClient: &http.Client{Transport: rt}}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if !errors.Is(err, ErrNoAttestations) {
		t.Fatalf("Fetch error = %v, want ErrNoAttestations", err)
	}
	if gotHost != "api.github.com" {
		t.Errorf("request went to %q, want api.github.com", gotHost)
	}
}

func TestFetch_TransportError(t *testing.T) {
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused by test")
	})
	f := &Fetcher{HTTPClient: &http.Client{Transport: rt}}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if err == nil || errors.Is(err, ErrNoAttestations) || !strings.Contains(err.Error(), "fetching attestations") {
		t.Errorf("Fetch error = %v, want a wrapped transport error", err)
	}
}

// A repo whose name carries a control character must fail while the request
// is being built, before anything is sent.
func TestFetch_RequestBuildError(t *testing.T) {
	f := &Fetcher{BaseURL: "https://unit.test"}
	_, err := f.Fetch(context.Background(), "own\nname", "abc")
	if err == nil || !strings.Contains(err.Error(), "creating attestation request") {
		t.Errorf("Fetch error = %v, want a request-creation error", err)
	}
}

func TestFetch_BadJSON(t *testing.T) {
	srv := fetchServer(t, http.StatusOK, `not json`)
	f := &Fetcher{BaseURL: srv.URL}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if err == nil || !strings.Contains(err.Error(), "decoding attestations") {
		t.Errorf("Fetch error = %v, want a decode error", err)
	}
}

// A 200 whose every bundle is unreadable is, as far as this package can
// answer, an artifact with no attestations at all.
func TestFetch_AllBundlesUnparseable(t *testing.T) {
	srv := fetchServer(t, http.StatusOK, `{"attestations":[{"bundle":{"mediaType":"nonsense"}}]}`)
	f := &Fetcher{BaseURL: srv.URL}

	_, err := f.Fetch(context.Background(), DefaultRepo, "abc")
	if !errors.Is(err, ErrNoAttestations) {
		t.Fatalf("Fetch error = %v, want ErrNoAttestations", err)
	}
}

func TestTUFCacheDir_NoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // Windows reads this, not HOME.

	if _, err := TUFCacheDir(); err == nil {
		t.Error("TUFCacheDir() succeeded without a home directory, want an error")
	}
}

func structPredicate(t *testing.T, raw string) *structpb.Struct {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The newResult branches below run for every verified bundle, but a fixture
// exercises only one shape at a time; building VerificationResults by hand
// pins each branch without needing a signed bundle per case.
func TestNewResult(t *testing.T) {
	signedAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	const san = "https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v0.0.74"

	t.Run("nil statement", func(t *testing.T) {
		if _, err := newResult(&verify.VerificationResult{}); err == nil ||
			!strings.Contains(err.Error(), "no in-toto statement") {
			t.Errorf("newResult error = %v, want the no-statement error", err)
		}
	})

	t.Run("provenance", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateSLSAProvenanceV1,
				Predicate:     structPredicate(t, slsaV1Predicate),
			},
			Signature:          &verify.SignatureVerificationResult{Certificate: &certificate.Summary{SubjectAlternativeName: san}},
			VerifiedTimestamps: []verify.TimestampVerificationResult{{Timestamp: signedAt}},
		}
		got, err := newResult(res)
		if err != nil {
			t.Fatalf("newResult: %v", err)
		}
		if got.PredicateType != PredicateSLSAProvenanceV1 {
			t.Errorf("PredicateType = %q", got.PredicateType)
		}
		if got.SignerIdentity != san {
			t.Errorf("SignerIdentity = %q, want %q", got.SignerIdentity, san)
		}
		if !got.SignedAt.Equal(signedAt) {
			t.Errorf("SignedAt = %v, want %v", got.SignedAt, signedAt)
		}
		want := &Provenance{
			Repository: "github.com/dimetron/pi-go",
			Workflow:   ".github/workflows/release.yml",
			Ref:        "refs/tags/v0.0.74",
			Commit:     "4086645aa1f2c3d4e5f60718293a4b5c6d7e8f90",
			RunURL:     "https://github.com/dimetron/pi-go/actions/runs/1234/attempts/1",
			BuildType:  "https://actions.github.io/buildtypes/workflow/v1",
		}
		if !reflect.DeepEqual(got.Provenance, want) {
			t.Errorf("Provenance =\n%+v\nwant\n%+v", got.Provenance, want)
		}
		if len(got.Predicate) == 0 {
			t.Error("raw predicate was dropped")
		}
	})

	// A verified bundle whose provenance predicate is missing the fields we
	// read must still produce a result — just a thinner one.
	t.Run("provenance without fields", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateSLSAProvenanceV1,
				Predicate:     structPredicate(t, `{"buildDefinition":{}}`),
			},
		}
		got, err := newResult(res)
		if err != nil {
			t.Fatalf("newResult: %v", err)
		}
		if got.Provenance == nil || *got.Provenance != (Provenance{}) || got.SBOM != nil || got.SignerIdentity != "" || !got.SignedAt.IsZero() {
			t.Errorf("expected a bare result, got %+v", got)
		}
	})

	t.Run("unparseable provenance", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateSLSAProvenanceV1,
				Predicate:     structPredicate(t, `{"buildDefinition": "not an object"}`),
			},
		}
		if _, err := newResult(res); err == nil || !strings.Contains(err.Error(), "parsing provenance predicate") {
			t.Errorf("newResult error = %v, want a provenance-parse error", err)
		}
	})

	t.Run("spdx", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateSPDXPrefix + "Ref",
				Predicate:     structPredicate(t, spdxPredicate),
			},
		}
		got, err := newResult(res)
		if err != nil {
			t.Fatalf("newResult: %v", err)
		}
		if got.SBOM == nil || got.SBOM.Format != "SPDX" || got.SBOM.Packages != 4 {
			t.Errorf("SBOM = %+v, want an SPDX summary of 4 packages", got.SBOM)
		}
	})

	t.Run("cyclonedx", func(t *testing.T) {
		const doc = `{
		  "specVersion": "1.5",
		  "metadata": {"component": {"name": "pi"}},
		  "components": [{"name": "a", "purl": "pkg:golang/example.com/a@v1"}]
		}`
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateCycloneDX,
				Predicate:     structPredicate(t, doc),
			},
		}
		got, err := newResult(res)
		if err != nil {
			t.Fatalf("newResult: %v", err)
		}
		if got.SBOM == nil || got.SBOM.Name != "pi" || got.SBOM.Version != "1.5" {
			t.Errorf("SBOM = %+v", got.SBOM)
		}
		if !reflect.DeepEqual(got.SBOM.Ecosystems, map[string]int{"golang": 1}) {
			t.Errorf("ecosystems = %v", got.SBOM.Ecosystems)
		}
	})

	t.Run("malformed SBOM predicate", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateCycloneDX,
				Predicate:     structPredicate(t, `{"specVersion": 5}`),
			},
		}
		if _, err := newResult(res); err == nil || !strings.Contains(err.Error(), "parsing CycloneDX predicate") {
			t.Errorf("newResult error = %v, want a CycloneDX-parse error", err)
		}
	})

	t.Run("unreadable predicate", func(t *testing.T) {
		// A predicate string with invalid UTF-8 cannot be marshaled back to
		// JSON; newResult must report it instead of returning garbage.
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: PredicateSLSAProvenanceV1,
				Predicate: &structpb.Struct{Fields: map[string]*structpb.Value{
					"x": structpb.NewStringValue("\xff"),
				}},
			},
		}
		if _, err := newResult(res); err == nil || !strings.Contains(err.Error(), "reading predicate") {
			t.Errorf("newResult error = %v, want a read-predicate error", err)
		}
	})

	t.Run("unknown predicate type", func(t *testing.T) {
		res := &verify.VerificationResult{
			Statement: &in_toto.Statement{PredicateType: "https://example.com/other"},
		}
		got, err := newResult(res)
		if err != nil {
			t.Fatalf("newResult: %v", err)
		}
		if got.Provenance != nil || got.SBOM != nil {
			t.Errorf("unknown predicate parsed as %+v", got)
		}
	})
}

// TestVerify_DigestMismatch: a well-formed digest that binds no subject in
// the bundle must fail inside the Sigstore verifier, not earlier.
func TestVerify_DigestMismatch(t *testing.T) {
	material := loadPinnedTrustRoot(t)
	b := loadFixtureBundle(t)

	v, err := NewVerifierWithMaterial(DefaultRepo, material)
	if err != nil {
		t.Fatalf("NewVerifierWithMaterial: %v", err)
	}

	if _, err := v.Verify(b, strings.Repeat("ab", 32)); err == nil {
		t.Fatal("Verify succeeded for a digest the bundle does not contain")
	}
}
