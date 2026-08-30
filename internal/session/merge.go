// Package session — merge.go implements safe merging of a remote sessions
// tree into the local one. It is the merge half of an rsync-based session
// sync: rsync stages the remote tree, this code folds it into the local tree
// without ever deleting or truncating local data.
package session

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MergeOptions controls MergeRemoteSessions.
type MergeOptions struct {
	// DryRun reports what would change without writing anything.
	DryRun bool
}

// MergeReport summarizes one MergeRemoteSessions run.
type MergeReport struct {
	// Added counts sessions that existed only on the remote side.
	Added int
	// Merged counts sessions present on both sides that were folded together.
	Merged int
	// Errors lists per-session failures; the run continues past them.
	Errors []string
}

// MergeRemoteSessions folds every session under remoteDir into localDir in
// place. Sessions that exist only on the remote side are copied in. Sessions
// present on both sides are merged:
//
//   - events.jsonl: union by event ID. If one side is a prefix of the other
//     (the common "one machine continued the session" case) the longer file
//     wins; otherwise events are deduplicated by ID and ordered by timestamp.
//   - meta.json: the copy with the newer updatedAt wins.
//   - trajectory.atif.json / acp.jsonl: the winning side's copy wins.
//   - branches/: per-branch events are merged with the same union rule;
//     branches.json metadata follows the events winner.
//
// Local-only sessions are never touched, and nothing is ever deleted. All
// writes are atomic (temp + rename), so an interrupted merge leaves each file
// either old or new, never torn.
func MergeRemoteSessions(localDir, remoteDir string, opts MergeOptions) (MergeReport, error) {
	var report MergeReport
	entries, err := os.ReadDir(remoteDir)
	if err != nil {
		return report, fmt.Errorf("reading remote sessions dir: %w", err)
	}
	r := &mergeRunner{dryRun: opts.DryRun}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		remoteSession := filepath.Join(remoteDir, id)
		// Only real session dirs are synced. The sessions root also holds
		// non-session dirs (archive/, backup/, .idea/) that must not be
		// treated as sessions.
		if !isSessionDir(remoteSession) {
			continue
		}
		localSession := filepath.Join(localDir, id)
		if _, err := os.Stat(filepath.Join(localSession, "meta.json")); os.IsNotExist(err) {
			if err := r.copyDir(localSession, remoteSession); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			report.Added++
			continue
		}
		if err := r.mergeSession(localSession, remoteSession); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		report.Merged++
	}
	return report, nil
}

// isSessionDir reports whether dir looks like a session directory: it carries
// a meta.json or an events.jsonl at its top level. Non-session dirs in the
// sessions root (archive/, backup/, .idea/) are skipped by the merge.
func isSessionDir(dir string) bool {
	for _, f := range []string{"meta.json", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// mergeRunner carries merge state (currently just dry-run) through the
// per-session merge helpers.
type mergeRunner struct {
	dryRun bool
}

// mergeSession folds one remote session directory into the local one.
func (r *mergeRunner) mergeSession(localDir, remoteDir string) error {
	wEvents, err := r.mergeEventsFiles(localDir, remoteDir)
	if err != nil {
		return err
	}
	if _, err := r.mergeMeta(localDir, remoteDir); err != nil {
		return err
	}
	if wEvents == "remote" {
		// Remote is a strict continuation of local: its derived files are the
		// authoritative ones. For "local" or "merged" winners the local
		// derived files stay put.
		if err := r.copyDerivedFiles(localDir, remoteDir); err != nil {
			return err
		}
	}
	return r.mergeBranchesDir(localDir, remoteDir, wEvents)
}

// derivedFiles are per-session artifacts derived from events; they follow the
// events winner rather than being merged themselves.
var derivedFiles = []string{"trajectory.atif.json", "acp.jsonl"}

// copyDerivedFiles copies the winner side's derived files over the local ones.
func (r *mergeRunner) copyDerivedFiles(localDir, remoteDir string) error {
	for _, f := range derivedFiles {
		src := filepath.Join(remoteDir, f)
		dst := filepath.Join(localDir, f)
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := r.writeFileAtomic(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// mergeEventsFiles merges the events.jsonl of two session dirs into the local
// one and reports which side won: "local", "remote", or "merged".
func (r *mergeRunner) mergeEventsFiles(localDir, remoteDir string) (string, error) {
	lData, lErr := os.ReadFile(filepath.Join(localDir, "events.jsonl"))
	rData, rErr := os.ReadFile(filepath.Join(remoteDir, "events.jsonl"))
	lMissing := lErr != nil && os.IsNotExist(lErr)
	rMissing := rErr != nil && os.IsNotExist(rErr)
	switch {
	case lErr != nil && !lMissing:
		return "", lErr
	case rErr != nil && !rMissing:
		return "", rErr
	case lMissing && rMissing:
		return "local", nil
	case lMissing:
		if err := r.writeFileAtomic(filepath.Join(localDir, "events.jsonl"), rData, 0o644); err != nil {
			return "", err
		}
		return "remote", nil
	case rMissing:
		return "local", nil
	}
	merged, winner := mergeEventsData(lData, rData)
	if winner != "local" {
		if err := r.writeFileAtomic(filepath.Join(localDir, "events.jsonl"), merged, 0o644); err != nil {
			return "", err
		}
	}
	return winner, nil
}

// rawEvent is one JSONL line plus the fields the merge keys on. Keeping the
// raw line means a merged file preserves every field of the original events,
// including ones this binary does not know about.
type rawEvent struct {
	line      []byte
	id        string
	timestamp time.Time
}

// parseRawEvents splits a JSONL events file into rawEvent entries.
func parseRawEvents(data []byte) ([]rawEvent, error) {
	var out []rawEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var meta struct {
			ID        string    `json:"id"`
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal([]byte(line), &meta); err != nil {
			return nil, err
		}
		out = append(out, rawEvent{line: []byte(line), id: meta.ID, timestamp: meta.Timestamp})
	}
	return out, nil
}

// isPrefix reports whether a's event ID sequence is a prefix of b's.
func isPrefix(a, b []rawEvent) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i].id != b[i].id {
			return false
		}
	}
	return true
}

// unionEvents deduplicates by event ID and orders by timestamp. Events with an
// empty ID are kept as-is (they cannot be deduplicated).
func unionEvents(a, b []rawEvent) []rawEvent {
	seen := make(map[string]bool)
	out := make([]rawEvent, 0, len(a)+len(b))
	for _, e := range append(append([]rawEvent{}, a...), b...) {
		if e.id != "" {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].timestamp.Before(out[j].timestamp) })
	return out
}

// mergeEventsData merges two events.jsonl payloads. It returns the merged
// bytes and the winner: "local" (keep local), "remote" (remote is a strict
// continuation), or "merged" (both sides contributed unique events).
func mergeEventsData(local, remote []byte) ([]byte, string) {
	l, err := parseRawEvents(local)
	if err != nil {
		return local, "local"
	}
	r, err := parseRawEvents(remote)
	if err != nil {
		return local, "local"
	}
	if len(l) == 0 {
		return remote, "remote"
	}
	if len(r) == 0 {
		return local, "local"
	}
	if isPrefix(l, r) {
		return remote, "remote"
	}
	if isPrefix(r, l) {
		return local, "local"
	}
	merged := unionEvents(l, r)
	var sb strings.Builder
	for _, e := range merged {
		sb.Write(e.line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), "merged"
}

// mergeMeta keeps the meta.json with the newer updatedAt. The winning file is
// copied byte-for-byte so fields this binary does not know about survive.
func (r *mergeRunner) mergeMeta(localDir, remoteDir string) (string, error) {
	lMeta, lErr := readMeta(localDir)
	rMeta, rErr := readMeta(remoteDir)
	lMissing := lErr != nil && os.IsNotExist(lErr)
	rMissing := rErr != nil && os.IsNotExist(rErr)
	switch {
	case lErr != nil && !lMissing:
		return "", lErr
	case rErr != nil && !rMissing:
		return "", rErr
	case lMissing && rMissing:
		return "local", nil
	case lMissing:
		return r.copyMeta(localDir, remoteDir)
	case rMissing:
		return "local", nil
	}
	if rMeta.UpdatedAt.After(lMeta.UpdatedAt) {
		return r.copyMeta(localDir, remoteDir)
	}
	return "local", nil
}

func (r *mergeRunner) copyMeta(localDir, remoteDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(remoteDir, "meta.json"))
	if err != nil {
		return "", err
	}
	if err := r.writeFileAtomic(filepath.Join(localDir, "meta.json"), data, 0o644); err != nil {
		return "", err
	}
	return "remote", nil
}

// mergeBranchesDir merges the branches/ subdirectory. Per-branch events are
// merged with the same union rule; branches.json metadata follows the events
// winner.
func (r *mergeRunner) mergeBranchesDir(localDir, remoteDir, winner string) error {
	localBranches := filepath.Join(localDir, "branches")
	remoteBranches := filepath.Join(remoteDir, "branches")
	_, lErr := os.ReadDir(localBranches)
	rEntries, rErr := os.ReadDir(remoteBranches)
	lMissing := lErr != nil && os.IsNotExist(lErr)
	rMissing := rErr != nil && os.IsNotExist(rErr)
	switch {
	case lErr != nil && !lMissing:
		return lErr
	case rErr != nil && !rMissing:
		return rErr
	case lMissing && rMissing:
		return nil
	case lMissing:
		return r.copyDir(localBranches, remoteBranches)
	case rMissing:
		return nil
	}
	for _, re := range rEntries {
		if !re.IsDir() {
			continue
		}
		name := re.Name()
		lBranch := filepath.Join(localBranches, name)
		rBranch := filepath.Join(remoteBranches, name)
		if _, err := os.Stat(filepath.Join(lBranch, "events.jsonl")); os.IsNotExist(err) {
			if err := r.copyDir(lBranch, rBranch); err != nil {
				return err
			}
			continue
		}
		if _, err := r.mergeEventsFiles(lBranch, rBranch); err != nil {
			return err
		}
	}
	if winner == "remote" {
		data, err := os.ReadFile(filepath.Join(remoteDir, "branches.json"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return r.writeFileAtomic(filepath.Join(localDir, "branches.json"), data, 0o644)
	}
	return nil
}

// copyDir copies a directory tree from src to dst, atomically per file.
func (r *mergeRunner) copyDir(dst, src string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if r.dryRun {
				return nil
			}
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return r.writeFileAtomic(target, data, 0o644)
	})
}

// writeFileAtomic writes data to path via a temp file and rename, unless the
// runner is in dry-run mode, in which case it does nothing.
func (r *mergeRunner) writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if r.dryRun {
		return nil
	}
	return writeFileAtomic(path, data, perm)
}
