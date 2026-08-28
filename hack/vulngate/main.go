// Command vulngate turns govulncheck's JSON output into a gate that fails only
// on findings something can actually be done about.
//
// Usage:
//
//	govulncheck -format json ./... | go run ./hack/vulngate
//
// A finding is actionable when its advisory names a fixed version: upgrade the
// module and the problem goes away. A finding with no fixed version cannot be
// acted on here at all — there is nothing to upgrade to — so failing the build
// on it leaves the check red on every commit until an upstream maintainer
// happens to cut a release. A check that is always red is a check nobody reads,
// and the findings that do matter drown alongside it.
//
// So the rule is: fail when a fix exists, report when it does not. That keeps
// the gate quiet until it has something to say, and what it says is always
// "you can do something about this".
//
// The unfixed findings are still printed. They are worth knowing about even
// when they cannot be acted on, and a fix appearing upstream later moves one
// from the reported list into a build failure — which is exactly the moment
// someone should look at it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// govulncheckMessage is one object from the JSON stream. Every message carries
// at most one of these fields, so the rest stay nil.
type govulncheckMessage struct {
	OSV     *osvEntry `json:"osv"`
	Finding *finding  `json:"finding"`
}

// osvEntry is the advisory body. govulncheck emits one for every advisory it
// knows about, whether or not it applies here.
type osvEntry struct {
	ID       string     `json:"id"`
	Summary  string     `json:"summary"`
	Affected []affected `json:"affected"`
}

type affected struct {
	Package struct {
		Name string `json:"name"`
	} `json:"package"`
	Ranges []struct {
		Events []map[string]string `json:"events"`
	} `json:"ranges"`
}

// finding is govulncheck reporting that an advisory applies to this module.
type finding struct {
	OSV string `json:"osv"`
}

// fixedVersion returns the version the advisory is fixed in, or "" when the
// advisory has no fix. An OSV range is a list of events; a "fixed" event is
// what marks a released fix.
func (o *osvEntry) fixedVersion() string {
	for _, a := range o.Affected {
		for _, r := range a.Ranges {
			for _, e := range r.Events {
				if v, ok := e["fixed"]; ok && v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// modules lists the modules the advisory affects, for the report line.
func (o *osvEntry) modules() string {
	seen := map[string]bool{}
	var names []string
	for _, a := range o.Affected {
		if n := a.Package.Name; n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type report struct {
	id      string
	modules string
	fix     string
}

func run(r io.Reader, out io.Writer) (int, error) {
	// The output is a stream of concatenated JSON objects rather than one
	// document; a Decoder reads them one after another without help.
	dec := json.NewDecoder(r)

	advisories := map[string]*osvEntry{}
	reported := map[string]bool{}

	for {
		var msg govulncheckMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 2, fmt.Errorf("decoding govulncheck output: %w", err)
		}
		if msg.OSV != nil && msg.OSV.ID != "" {
			advisories[msg.OSV.ID] = msg.OSV
		}
		if msg.Finding != nil && msg.Finding.OSV != "" {
			reported[msg.Finding.OSV] = true
		}
	}

	ids := make([]string, 0, len(reported))
	for id := range reported {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var actionable, unfixed []report
	for _, id := range ids {
		a := advisories[id]
		if a == nil {
			// A finding whose advisory body never arrived. Treat it as
			// actionable rather than assume it is harmless: silence here would
			// be the wrong default for a security gate.
			actionable = append(actionable, report{id: id, fix: "unknown"})
			continue
		}
		if fix := a.fixedVersion(); fix != "" {
			actionable = append(actionable, report{id: id, modules: a.modules(), fix: fix})
		} else {
			unfixed = append(unfixed, report{id: id, modules: a.modules()})
		}
	}

	if len(unfixed) > 0 {
		fmt.Fprintf(out, "%d finding(s) with no fix available — reported, not failing:\n", len(unfixed))
		for _, r := range unfixed {
			fmt.Fprintf(out, "  %s  %s  https://pkg.go.dev/vuln/%s\n", r.id, r.modules, r.id)
		}
		fmt.Fprintln(out)
	}

	if len(actionable) > 0 {
		fmt.Fprintf(out, "%d finding(s) with a fix available:\n", len(actionable))
		for _, r := range actionable {
			fmt.Fprintf(out, "  %s  %s  fixed in %s  https://pkg.go.dev/vuln/%s\n", r.id, r.modules, r.fix, r.id)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Upgrade the module to the fixed version.")
		return 1, nil
	}

	fmt.Fprintf(out, "No findings with an available fix (%d reported in total).\n", len(reported))
	return 0, nil
}

func main() {
	code, err := run(os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vulngate:", err)
	}
	os.Exit(code)
}
