// Command lint reports structural defects in the TLA+ models under hack/formal.
//
//	go run ./hack/formal/lint        # every model
//	go run ./hack/formal/lint DIR    # one directory tree
//
// Exit 0 when every spec is clean, 1 on a finding, 2 when there is nothing to lint
// or a file cannot be read. It is a Go package so that each check is covered by a
// test: the checks exist to catch false greens, and an untested checker is one more
// thing to trust without evidence.
package main

import (
	"fmt"
	"os"

	formal "github.com/dimetron/pi-go/hack/formal/lint"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"hack/formal"}
	}

	total, specs, failed := 0, 0, false
	for _, root := range roots {
		findings, n, err := formal.LintDir(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "formal-lint: %v\n", err)
			os.Exit(2)
		}
		specs += n
		for _, f := range findings {
			fmt.Println(f)
		}
		total += len(findings)
		if n == 0 {
			failed = true
		}
	}
	if specs == 0 || failed {
		fmt.Fprintln(os.Stderr, "formal-lint: no .tla files found")
		os.Exit(2)
	}
	if total > 0 {
		fmt.Printf("\nformal-lint: %d finding(s) in %d spec(s)\n", total, specs)
		os.Exit(1)
	}
	fmt.Printf("formal-lint: %d spec(s) clean\n", specs)
}
