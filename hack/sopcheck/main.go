// Command sopcheck validates the spec directories under specs/ against the
// SOP artifact contracts, without running /plan or /run.
//
// Usage:
//
//	go run ./hack/sopcheck <repo-root>            # summary for every spec
//	go run ./hack/sopcheck <repo-root> <spec>     # findings for one spec
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sopcheck <repo-root> [spec-name]")
		os.Exit(2)
	}
	root := os.Args[1]

	if len(os.Args) > 2 {
		os.Exit(reportOne(root, os.Args[2]))
	}
	reportAll(root)
}

func reportOne(root, name string) int {
	spec, err := specdoc.Load(root, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		return 1
	}
	findings := validate.Check(spec, root, validate.PlanContract())
	for _, f := range findings {
		fmt.Println(f.String())
	}
	if findings.OK() {
		return 0
	}
	return 1
}

func reportAll(root string) {
	specs := discover(root)
	byRule := map[string]int{}
	var planOK, preflightOK int

	for _, name := range specs {
		spec, err := specdoc.Load(root, name)
		if err != nil {
			continue
		}
		plan := validate.Check(spec, root, validate.PlanContract())
		pre := validate.Check(spec, root, validate.RunPreflightContract())
		if plan.OK() {
			planOK++
		}
		if pre.OK() {
			preflightOK++
		}
		for _, f := range plan.Errors() {
			byRule[f.Rule]++
		}
		fmt.Printf("%-58s plan=%-4s preflight=%-4s errors=%d\n",
			name, verdict(plan.OK()), verdict(pre.OK()), len(plan.Errors()))
	}

	fmt.Printf("\n%d specs — plan contract clean: %d, run preflight clean: %d\n",
		len(specs), planOK, preflightOK)
	fmt.Println("\nBlocking findings by rule:")
	type kv struct {
		rule string
		n    int
	}
	var rows []kv
	for r, n := range byRule {
		rows = append(rows, kv{r, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	for _, r := range rows {
		fmt.Printf("  %4d  %s\n", r.n, r.rule)
	}
}

// discover lists every directory under specs/ that holds at least one artifact.
func discover(root string) []string {
	var out []string
	specsDir := filepath.Join(root, "specs")
	_ = filepath.WalkDir(specsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // an unreadable directory is skipped, not fatal
		}
		for _, a := range []string{specdoc.Plan, specdoc.Prompt, specdoc.RoughIdea} {
			if _, statErr := os.Stat(filepath.Join(p, a)); statErr == nil {
				rel, relErr := filepath.Rel(specsDir, p)
				if relErr == nil {
					out = append(out, filepath.ToSlash(rel))
				}
				return nil
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func verdict(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
