package attest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
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
