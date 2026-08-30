// Command session-merge folds a staged remote pi-go sessions tree into the
// local one without losing local data. It is the merge half of
// scripts/sync-sessions.sh: rsync stages the remote tree, this command merges
// it into ~/.pi-go/sessions.
//
// Usage:
//
//	session-merge --local <sessions-dir> --remote <staged-dir> [--dry-run]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dimetron/pi-go/internal/session"
)

func main() {
	local := flag.String("local", "", "local sessions dir (e.g. ~/.pi-go/sessions)")
	remote := flag.String("remote", "", "staged remote sessions dir")
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	flag.Parse()

	if *local == "" || *remote == "" {
		fmt.Fprintln(os.Stderr, "session-merge: --local and --remote are required")
		flag.Usage()
		os.Exit(2)
	}

	report, err := session.MergeRemoteSessions(*local, *remote, session.MergeOptions{DryRun: *dryRun})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session-merge: %v\n", err)
		os.Exit(1)
	}

	verb := "merged"
	if *dryRun {
		verb = "would merge"
	}
	fmt.Printf("session-merge: %s %d new sessions, %d existing sessions, %d errors\n",
		verb, report.Added, report.Merged, len(report.Errors))
	for _, e := range report.Errors {
		fmt.Fprintf(os.Stderr, "  error: %s\n", e)
	}
	if len(report.Errors) > 0 {
		os.Exit(1)
	}
}
