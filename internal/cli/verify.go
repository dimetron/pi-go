package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/attest"
)

// verifyTimeout bounds the whole command: a TUF refresh plus one API call per
// target. Generous, because the first run on a machine downloads Sigstore's
// trust root.
const verifyTimeout = 60 * time.Second

// newVerifyCmd wires up `pi verify`, which checks the running binary — or any
// file named on the command line — against the attestations the release
// workflow published for it.
//
// The check starts from the file's SHA-256 and nothing else. A version string
// compiled into the binary, the name it was installed under, and the URL it
// came from are all attacker-controlled; the digest is not.
func newVerifyCmd() *cobra.Command {
	var (
		repo     string
		asJSON   bool
		dumpSBOM bool
	)

	cmd := &cobra.Command{
		Use:   "verify [file...]",
		Short: "Verify this binary's build provenance and SBOM attestations",
		Long: `Verify that a pi binary was built by pi-go's release workflow.

With no arguments, verifies the running executable. Otherwise verifies each
file named on the command line — a downloaded archive, for instance, before
you extract it.

Both attestations published for every release are checked:

  build provenance  the workflow, git ref and commit the artifact was built from
  SBOM              the dependency document cataloged at release time

Verification is cryptographic, not advisory. The artifact's SHA-256 is looked
up in GitHub's attestations API, and the returned Sigstore bundles are checked
against the public-good Sigstore trust root: certificate chain, transparency
log inclusion, signed certificate timestamp, and a certificate identity that
must name this repository's release workflow running on a tag.

Requires network access. A binary you built yourself has no attestation and
will report as unverified — that is the expected result, not a failure.`,
		Example: `  pi verify                                  # the running binary
  pi verify ./pi                             # a specific file
  pi verify pi-go_0.0.74_linux_amd64.tar.gz  # a downloaded archive
  pi verify --json                           # machine-readable
  pi verify --sbom > sbom.spdx.json          # print the attested SBOM`,
		// A failed verification is a result, not a misuse of the command:
		// printing the flag list underneath it buries the finding, and
		// main already prints the error itself.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(cmd.Context(), cmd.OutOrStdout(), verifyOptions{
				Repo:     repo,
				Targets:  args,
				JSON:     asJSON,
				DumpSBOM: dumpSBOM,
			})
		},
	}

	cmd.Flags().StringVar(&repo, "repo", attest.DefaultRepo,
		"repository whose release workflow must have signed the artifact")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the result as JSON")
	cmd.Flags().BoolVar(&dumpSBOM, "sbom", false,
		"print the attested SBOM document to stdout instead of a summary")

	return cmd
}

type verifyOptions struct {
	Repo     string
	Targets  []string
	JSON     bool
	DumpSBOM bool
}

// verifyReport is the JSON shape of one target's result.
type verifyReport struct {
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Repo     string `json:"repo"`
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`

	// noAttestation separates "GitHub holds nothing for these bytes" — the
	// expected answer for a locally built binary — from a real verification
	// failure, which is a much louder result.
	noAttestation bool
	Results       []*attest.Result `json:"attestations,omitempty"`
}

func runVerify(ctx context.Context, out io.Writer, opts verifyOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	targets := opts.Targets
	if len(targets) == 0 {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locating the running binary: %w", err)
		}
		targets = []string{self}
	}

	// One verifier for all targets: building it refreshes the Sigstore trust
	// root over the network, which is by far the slowest part.
	verifier, err := attest.NewVerifier(opts.Repo)
	if err != nil {
		return err
	}
	fetcher := &attest.Fetcher{
		Token:     githubTokenFromEnv(),
		UserAgent: "pi-go/" + versionString(),
	}

	reports := make([]verifyReport, 0, len(targets))
	for _, target := range targets {
		reports = append(reports, verifyOne(ctx, fetcher, verifier, opts.Repo, target))
	}

	switch {
	case opts.DumpSBOM:
		return writeSBOMDocuments(out, reports)
	case opts.JSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return err
		}
	default:
		writeVerifyText(out, reports)
	}

	for _, r := range reports {
		if !r.Verified {
			return errors.New("verification failed")
		}
	}
	return nil
}

func verifyOne(ctx context.Context, fetcher *attest.Fetcher, verifier *attest.Verifier, repo, path string) verifyReport {
	report := verifyReport{Path: path, Repo: repo}

	digest, err := attest.FileDigest(path)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Digest = "sha256:" + digest

	bundles, err := fetcher.Fetch(ctx, repo, digest)
	if err != nil {
		report.Error = err.Error()
		report.noAttestation = errors.Is(err, attest.ErrNoAttestations)
		return report
	}

	// Bundles this policy rejects are not necessarily forgeries: the same
	// endpoint serves GitHub's own release attestations, signed by a
	// different authority. Only report an error if nothing verified, and
	// report the last rejection as the reason.
	var lastErr error
	for _, b := range bundles {
		res, err := verifier.Verify(b, digest)
		if err != nil {
			lastErr = err
			continue
		}
		report.Results = append(report.Results, res)
	}

	if len(report.Results) == 0 {
		if lastErr != nil {
			report.Error = lastErr.Error()
		} else {
			report.Error = attest.ErrNoAttestations.Error()
			report.noAttestation = true
		}
		return report
	}

	sort.Slice(report.Results, func(i, j int) bool {
		return report.Results[i].PredicateType < report.Results[j].PredicateType
	})
	report.Verified = true
	return report
}

func writeVerifyText(out io.Writer, reports []verifyReport) {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(out)
		}
		writeVerifyReport(out, r)
	}
}

// writeVerifyReport prints one target: its path and digest, then either the
// reason it is unverified or the attestations that checked out.
func writeVerifyReport(out io.Writer, r verifyReport) {
	fmt.Fprintf(out, "%s\n", r.Path)
	if r.Digest != "" {
		fmt.Fprintf(out, "  %s\n", r.Digest)
	}

	if !r.Verified {
		fmt.Fprintf(out, "\n  ✗ unverified: %s\n", r.Error)
		if r.noAttestation {
			fmt.Fprintf(out, "\n    %s holds no attestation for these bytes. A binary you\n", r.Repo)
			fmt.Fprintf(out, "    built yourself never has one, and neither does one from a\n")
			fmt.Fprintf(out, "    release made before the workflow started attesting.\n")
		}
		return
	}

	for _, res := range r.Results {
		writeAttestation(out, res)
	}
}

// writeAttestation prints one verified attestation: the detail block its
// predicate type calls for, then the signer identity and signing time that
// every predicate shares.
func writeAttestation(out io.Writer, res *attest.Result) {
	switch {
	case res.Provenance != nil:
		p := res.Provenance
		fmt.Fprintf(out, "\n  ✓ build provenance\n")
		writeField(out, "repository", p.Repository)
		writeField(out, "workflow", joinRef(p.Workflow, p.Ref))
		writeField(out, "commit", p.Commit)
		writeField(out, "run", p.RunURL)
	case res.SBOM != nil:
		s := res.SBOM
		fmt.Fprintf(out, "\n  ✓ SBOM\n")
		writeField(out, "format", strings.TrimSpace(s.Format+" "+s.Version))
		writeField(out, "packages", fmt.Sprintf("%d", s.Packages))
		if eco := s.EcosystemsSorted(); len(eco) > 0 {
			writeField(out, "ecosystems", strings.Join(eco, ", "))
		}
	default:
		fmt.Fprintf(out, "\n  ✓ %s\n", res.PredicateType)
	}
	writeField(out, "signer", res.SignerIdentity)
	if !res.SignedAt.IsZero() {
		writeField(out, "signed", res.SignedAt.UTC().Format(time.RFC3339))
	}
}

func writeField(out io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(out, "      %-11s %s\n", label, value)
}

func joinRef(workflow, ref string) string {
	switch {
	case workflow == "":
		return ref
	case ref == "":
		return workflow
	default:
		return workflow + "@" + ref
	}
}

// writeSBOMDocuments prints the attested SBOM predicates themselves. The point
// is that the bytes written here are the ones that were signed — piping this
// to a file gives a document whose authenticity was just checked.
func writeSBOMDocuments(out io.Writer, reports []verifyReport) error {
	found := false
	for _, r := range reports {
		if !r.Verified {
			return fmt.Errorf("%s: %s", r.Path, r.Error)
		}
		wrote, err := writeSBOMPredicates(out, r.Results)
		if err != nil {
			return err
		}
		found = found || wrote
	}
	if !found {
		return errors.New("no SBOM attestation found for the given artifacts")
	}
	return nil
}

// writeSBOMPredicates writes the raw SBOM predicate of every SBOM attestation
// in results, and reports whether it wrote any. Attestations that are not
// SBOMs, and SBOM results whose raw predicate was not retained, are skipped.
func writeSBOMPredicates(out io.Writer, results []*attest.Result) (bool, error) {
	found := false
	for _, res := range results {
		if res.SBOM == nil || len(res.Predicate) == 0 {
			continue
		}
		buf, err := json.MarshalIndent(res.Predicate, "", "  ")
		if err != nil {
			return found, fmt.Errorf("formatting SBOM: %w", err)
		}
		if _, err := out.Write(append(buf, '\n')); err != nil {
			return found, err
		}
		found = true
	}
	return found, nil
}

// githubTokenFromEnv returns a token if one is around. The attestations
// endpoint is public for public repositories, so this only buys a higher rate
// limit — verification never depends on being authenticated.
func githubTokenFromEnv() string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}
