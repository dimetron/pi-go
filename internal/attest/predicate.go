package attest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Provenance is the part of a SLSA v1 build-provenance predicate worth showing
// a user: which workflow, at which ref, from which commit, in which run.
type Provenance struct {
	Repository string `json:"repository,omitempty"`
	Workflow   string `json:"workflow,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	RunURL     string `json:"runURL,omitempty"`
	BuildType  string `json:"buildType,omitempty"`
}

// slsaV1 mirrors the fields actions/attest-build-provenance populates.
type slsaV1 struct {
	BuildDefinition struct {
		BuildType          string `json:"buildType"`
		ExternalParameters struct {
			Workflow struct {
				Ref        string `json:"ref"`
				Repository string `json:"repository"`
				Path       string `json:"path"`
			} `json:"workflow"`
		} `json:"externalParameters"`
		ResolvedDependencies []struct {
			URI    string            `json:"uri"`
			Digest map[string]string `json:"digest"`
		} `json:"resolvedDependencies"`
	} `json:"buildDefinition"`
	RunDetails struct {
		Metadata struct {
			InvocationID string `json:"invocationId"`
		} `json:"metadata"`
	} `json:"runDetails"`
}

func parseProvenance(raw json.RawMessage) (*Provenance, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("provenance predicate is empty")
	}
	var p slsaV1
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parsing provenance predicate: %w", err)
	}

	wf := p.BuildDefinition.ExternalParameters.Workflow
	out := &Provenance{
		Repository: strings.TrimPrefix(wf.Repository, "https://"),
		Workflow:   wf.Path,
		Ref:        wf.Ref,
		BuildType:  p.BuildDefinition.BuildType,
		RunURL:     p.RunDetails.Metadata.InvocationID,
	}

	// The source commit is the gitCommit digest of the repository listed in
	// resolvedDependencies; there is normally exactly one such entry.
	for _, dep := range p.BuildDefinition.ResolvedDependencies {
		if c := dep.Digest["gitCommit"]; c != "" {
			out.Commit = c
			break
		}
	}
	return out, nil
}

// SBOM summarizes an SPDX or CycloneDX document without holding onto it.
type SBOM struct {
	Format   string `json:"format"`
	Version  string `json:"version,omitempty"`
	Name     string `json:"name,omitempty"`
	Packages int    `json:"packages"`

	// Ecosystems counts packages by purl type ("golang", "github", ...), so
	// the summary says something about what is in the document rather than
	// just how big it is.
	Ecosystems map[string]int `json:"ecosystems,omitempty"`
}

type spdxDoc struct {
	SPDXVersion string `json:"spdxVersion"`
	Name        string `json:"name"`
	Packages    []struct {
		Name         string `json:"name"`
		ExternalRefs []struct {
			ReferenceType    string `json:"referenceType"`
			ReferenceLocator string `json:"referenceLocator"`
		} `json:"externalRefs"`
	} `json:"packages"`
}

type cycloneDXDoc struct {
	SpecVersion string `json:"specVersion"`
	Metadata    struct {
		Component struct {
			Name string `json:"name"`
		} `json:"component"`
	} `json:"metadata"`
	Components []struct {
		Name string `json:"name"`
		PURL string `json:"purl"`
	} `json:"components"`
}

func parseSBOM(predicateType string, raw json.RawMessage) (*SBOM, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("SBOM predicate is empty")
	}

	if predicateType == PredicateCycloneDX {
		var doc cycloneDXDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parsing CycloneDX predicate: %w", err)
		}
		out := &SBOM{
			Format:     "CycloneDX",
			Version:    doc.SpecVersion,
			Name:       doc.Metadata.Component.Name,
			Packages:   len(doc.Components),
			Ecosystems: map[string]int{},
		}
		for _, c := range doc.Components {
			if t := purlType(c.PURL); t != "" {
				out.Ecosystems[t]++
			}
		}
		return out, nil
	}

	var doc spdxDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing SPDX predicate: %w", err)
	}
	out := &SBOM{
		Format:     "SPDX",
		Version:    strings.TrimPrefix(doc.SPDXVersion, "SPDX-"),
		Name:       doc.Name,
		Packages:   len(doc.Packages),
		Ecosystems: map[string]int{},
	}
	for _, p := range doc.Packages {
		for _, ref := range p.ExternalRefs {
			if ref.ReferenceType != "purl" {
				continue
			}
			if t := purlType(ref.ReferenceLocator); t != "" {
				out.Ecosystems[t]++
			}
			break
		}
	}
	return out, nil
}

// purlType pulls the type out of a package URL: "pkg:golang/foo@v1" -> "golang".
func purlType(purl string) string {
	rest, ok := strings.CutPrefix(purl, "pkg:")
	if !ok {
		return ""
	}
	t, _, ok := strings.Cut(rest, "/")
	if !ok || t == "" {
		return ""
	}
	return t
}

// EcosystemsSorted renders the ecosystem counts deterministically, most
// packages first, so output does not shuffle between runs of a map.
func (s *SBOM) EcosystemsSorted() []string {
	if len(s.Ecosystems) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.Ecosystems))
	for k := range s.Ecosystems {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if s.Ecosystems[keys[i]] != s.Ecosystems[keys[j]] {
			return s.Ecosystems[keys[i]] > s.Ecosystems[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s %d", k, s.Ecosystems[k]))
	}
	return out
}
