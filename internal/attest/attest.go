// Package attest verifies the Sigstore artifact attestations that pi-go's
// release workflow publishes.
//
// Every tagged release signs two statements per artifact: SLSA build
// provenance, which ties the artifact to the workflow run that produced it,
// and an SBOM. Both are stored by GitHub against the artifact's SHA-256
// digest, so verification starts from the bytes on disk and never trusts a
// filename, a version string compiled into the binary, or the download URL.
//
// Trust is anchored in the public-good Sigstore TUF root: pi-go is a public
// repository, so its attestations are signed with certificates from
// fulcio.sigstore.dev and logged in Rekor. Attestations GitHub generates on
// its own for release assets are signed by a different, GitHub-operated
// authority and are deliberately out of scope here — this package answers
// "did dimetron/pi-go's release workflow build these bytes", not "is this
// file attached to some GitHub release".
package attest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	// DefaultRepo is the repository whose release workflow signs official
	// pi-go builds.
	DefaultRepo = "dimetron/pi-go"

	// WorkflowPath is the only workflow permitted to sign a release. Pinning
	// it matters: without it, any workflow in the repository — including one
	// added by a pull request — would satisfy the identity check.
	WorkflowPath = ".github/workflows/release.yml"

	// oidcIssuer is the issuer GitHub Actions' OIDC tokens carry. Fulcio
	// records it in the signing certificate.
	oidcIssuer = "https://token.actions.githubusercontent.com"

	defaultAPIBaseURL = "https://api.github.com"
)

// Predicate types, as they appear in the in-toto statement.
const (
	PredicateSLSAProvenanceV1 = "https://slsa.dev/provenance/v1"
	PredicateSPDXPrefix       = "https://spdx.dev/Document"
	PredicateCycloneDX        = "https://cyclonedx.org/bom"
)

// ErrNoAttestations reports that GitHub holds no attestation for a digest.
// It is not a verification failure: a locally built binary, or one from a
// release predating attestation, will never have one.
var ErrNoAttestations = errors.New("no attestations found for this artifact")

// FileDigest returns the hex-encoded SHA-256 of the file at path. This is the
// only identity an artifact has as far as verification is concerned.
func FileDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is supplied by the operator
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Fetcher retrieves attestation bundles from GitHub's attestations API.
type Fetcher struct {
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient *http.Client
	// BaseURL defaults to https://api.github.com. Tests override it.
	BaseURL string
	// Token is optional. The endpoint is public for public repositories;
	// a token only raises the rate limit.
	Token string
	// UserAgent is sent with every request.
	UserAgent string
}

type apiResponse struct {
	Attestations []struct {
		Bundle json.RawMessage `json:"bundle"`
	} `json:"attestations"`
}

// Fetch returns every bundle GitHub holds for digest, which is the
// hex-encoded SHA-256 of the artifact. It returns ErrNoAttestations when the
// digest is unknown to GitHub.
func (f *Fetcher) Fetch(ctx context.Context, repo, digest string) ([]*bundle.Bundle, error) {
	base := f.BaseURL
	if base == "" {
		base = defaultAPIBaseURL
	}
	client := f.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	endpoint := fmt.Sprintf("%s/repos/%s/attestations/sha256:%s",
		strings.TrimSuffix(base, "/"), repo, url.PathEscape(digest))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating attestation request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching attestations: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNoAttestations
	default:
		return nil, fmt.Errorf("attestations API returned HTTP %d", resp.StatusCode)
	}

	var payload apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding attestations: %w", err)
	}
	if len(payload.Attestations) == 0 {
		return nil, ErrNoAttestations
	}

	bundles := make([]*bundle.Bundle, 0, len(payload.Attestations))
	for _, a := range payload.Attestations {
		b := &bundle.Bundle{}
		if err := b.UnmarshalJSON(a.Bundle); err != nil {
			// One unreadable bundle must not hide the others: this endpoint
			// also returns the attestations GitHub generates for release
			// assets, which use bundle shapes this package does not verify.
			continue
		}
		bundles = append(bundles, b)
	}
	if len(bundles) == 0 {
		return nil, ErrNoAttestations
	}
	return bundles, nil
}

// Verifier checks bundles against the public-good Sigstore trust root and an
// identity policy naming a specific repository's release workflow.
type Verifier struct {
	verifier *verify.Verifier
	sanRE    *regexp.Regexp
	repo     string
}

// TUFCacheDir is where the Sigstore TUF metadata is cached. It lives under
// pi-go's own state directory so that `pi verify` does not write into a
// location shared with other Sigstore tooling.
func TUFCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".pi-go", "sigstore"), nil
}

// NewVerifier builds a verifier for repo, refreshing the Sigstore TUF trust
// root over the network. Callers that already hold trusted material — tests,
// mainly — should use NewVerifierWithMaterial.
func NewVerifier(repo string) (*Verifier, error) {
	opts := tuf.DefaultOptions()
	cache, err := TUFCacheDir()
	if err == nil {
		opts.CachePath = cache
	}

	trustedRoot, err := root.FetchTrustedRootWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("fetching Sigstore trust root: %w", err)
	}
	return NewVerifierWithMaterial(repo, trustedRoot)
}

// NewVerifierWithMaterial builds a verifier over caller-supplied trusted
// material.
func NewVerifierWithMaterial(repo string, material root.TrustedMaterial) (*Verifier, error) {
	sanRE, err := workflowSANPattern(repo)
	if err != nil {
		return nil, err
	}

	// Require a transparency-log entry, a signed certificate timestamp and an
	// observer timestamp. Dropping any of these would let an attacker who
	// briefly held a signing certificate backdate a signature.
	v, err := verify.NewVerifier(material,
		verify.WithTransparencyLog(1),
		verify.WithSignedCertificateTimestamps(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return nil, fmt.Errorf("building verifier: %w", err)
	}
	return &Verifier{verifier: v, sanRE: sanRE, repo: repo}, nil
}

// workflowSANPattern builds the certificate-identity pattern for repo's
// release workflow. Fulcio puts the workflow ref in the certificate's SAN, so
// this is what binds a signature to "the release workflow of this repository,
// running on a tag" rather than to GitHub Actions in general.
func workflowSANPattern(repo string) (*regexp.Regexp, error) {
	if repo == "" || strings.Count(repo, "/") != 1 {
		return nil, fmt.Errorf("repository must be in owner/name form, got %q", repo)
	}
	pattern := fmt.Sprintf(`^https://github\.com/%s/%s@refs/tags/.+$`,
		regexp.QuoteMeta(repo), regexp.QuoteMeta(WorkflowPath))
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("building identity pattern: %w", err)
	}
	return re, nil
}

// Result is one verified attestation.
type Result struct {
	PredicateType  string      `json:"predicateType"`
	SignerIdentity string      `json:"signerIdentity,omitempty"`
	SignedAt       time.Time   `json:"signedAt,omitempty"`
	Provenance     *Provenance `json:"provenance,omitempty"`
	SBOM           *SBOM       `json:"sbom,omitempty"`

	// Predicate is the raw predicate, kept so `pi verify --sbom` can print
	// the SBOM document itself rather than a summary of it.
	Predicate json.RawMessage `json:"-"`
}

// Verify checks one bundle against the policy and the artifact digest.
func (v *Verifier) Verify(b *bundle.Bundle, digest string) (*Result, error) {
	raw, err := hex.DecodeString(digest)
	if err != nil {
		return nil, fmt.Errorf("invalid digest %q: %w", digest, err)
	}

	certID, err := verify.NewShortCertificateIdentity(oidcIssuer, "", "", v.sanRE.String())
	if err != nil {
		return nil, fmt.Errorf("building certificate identity: %w", err)
	}

	res, err := v.verifier.Verify(b, verify.NewPolicy(
		verify.WithArtifactDigest("sha256", raw),
		verify.WithCertificateIdentity(certID),
	))
	if err != nil {
		return nil, err
	}
	return newResult(res)
}

func newResult(res *verify.VerificationResult) (*Result, error) {
	if res.Statement == nil {
		return nil, errors.New("verified bundle carries no in-toto statement")
	}

	out := &Result{PredicateType: res.Statement.PredicateType}
	// VerifiedIdentity holds the matcher that was satisfied, not the value
	// from the certificate; the actual SAN comes off the certificate summary.
	if res.Signature != nil && res.Signature.Certificate != nil {
		out.SignerIdentity = res.Signature.Certificate.SubjectAlternativeName
	}
	if len(res.VerifiedTimestamps) > 0 {
		out.SignedAt = res.VerifiedTimestamps[0].Timestamp
	}

	// The statement's predicate arrives as a protobuf Struct; round-tripping
	// through JSON is the least surprising way to read it with encoding/json.
	if res.Statement.Predicate != nil {
		encoded, err := res.Statement.Predicate.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("reading predicate: %w", err)
		}
		out.Predicate = encoded
	}

	switch {
	case out.PredicateType == PredicateSLSAProvenanceV1:
		p, err := parseProvenance(out.Predicate)
		if err != nil {
			return nil, err
		}
		out.Provenance = p
	case strings.HasPrefix(out.PredicateType, PredicateSPDXPrefix),
		out.PredicateType == PredicateCycloneDX:
		s, err := parseSBOM(out.PredicateType, out.Predicate)
		if err != nil {
			return nil, err
		}
		out.SBOM = s
	}
	return out, nil
}
