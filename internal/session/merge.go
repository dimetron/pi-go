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
	"strings"
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
//   - events.jsonl: the local file is never rewritten — remote-only events are
//     appended. If one side is a strict continuation of the other (the common
//     "one machine continued the session" case) the missing tail is appended;
//     otherwise the remote events whose IDs are absent locally are appended.
//     Appending (rather than replace) means a pi still writing to the session
//     cannot lose its events to the merge: both processes only ever add lines,
//     at worst interleaved.
//   - meta.json: the copy with the newer updatedAt wins.
//   - trajectory.atif.json / acp.jsonl: the winning side's copy wins.
//   - branches/: per-branch events are merged with the same append rule;
//     branches.json branch entries are merged by name, preserving local-only
//     branches.
//
// Local-only sessions are never touched, and nothing is ever deleted. All
// writes are atomic (temp + rename) or pure appends, so an interrupted merge
// leaves each file either old or new, never torn.
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
		exists, err := localSessionExists(localSession)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !exists {
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

// localSessionExists reports whether the local side already has this session.
// Either marker (meta.json or events.jsonl) counts: a session with only an
// events file (torn or legacy) must still be merged, never overwritten.
func localSessionExists(dir string) (bool, error) {
	for _, f := range []string{"meta.json", "events.jsonl"} {
		_, err := os.Stat(filepath.Join(dir, f))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
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
		// Remote events are a strict, longer continuation of local: its
		// derived files are the authoritative ones. For "local" or "merged"
		// winners the local derived files stay put.
		if err := r.copyDerivedFiles(localDir, remoteDir); err != nil {
			return err
		}
	}
	if err := r.mergeBranchesDir(localDir, remoteDir); err != nil {
		return err
	}
	return r.mergeBranchesJSON(localDir, remoteDir)
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
// one and reports which side the local history now reflects: "local" (nothing
// changed), "remote" (remote was a strict, longer continuation and its tail
// was appended), or "merged" (remote-only events were appended).
//
// The local file is never replaced wholesale: only lines absent from it are
// appended. Appends cannot lose concurrent pi writes the way a read-modify-
// write rewrite would — both writers only ever add lines.
func (r *mergeRunner) mergeEventsFiles(localDir, remoteDir string) (string, error) {
	localPath := filepath.Join(localDir, "events.jsonl")
	remotePath := filepath.Join(remoteDir, "events.jsonl")
	lData, lErr := os.ReadFile(localPath)
	rData, rErr := os.ReadFile(remotePath)
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
		if err := r.appendToFile(localPath, rData); err != nil {
			return "", err
		}
		return "remote", nil
	case rMissing:
		return "local", nil
	}
	l, err := parseRawEvents(lData)
	if err != nil {
		return "", fmt.Errorf("parsing local events: %w", err)
	}
	remote, err := parseRawEvents(rData)
	if err != nil {
		return "", fmt.Errorf("parsing remote events: %w", err)
	}
	if len(remote) == 0 {
		return "local", nil
	}
	// Strict continuation: the shorter sequence is a prefix of the longer one.
	// Append the longer tail. Equal-length identical histories are NOT a
	// continuation (nothing to append) and must not trigger a write.
	if len(l) < len(remote) && isPrefix(l, remote) {
		tail := marshalEvents(remote[len(l):])
		if err := r.appendToFile(localPath, tail); err != nil {
			return "", err
		}
		return "remote", nil
	}
	if len(remote) < len(l) && isPrefix(remote, l) {
		return "local", nil
	}
	// Divergent histories: append remote events whose IDs are absent locally.
	lIDs := make(map[string]bool, len(l))
	for _, e := range l {
		if e.id != "" {
			lIDs[e.id] = true
		}
	}
	var missing []rawEvent
	for _, e := range remote {
		if e.id != "" && lIDs[e.id] {
			continue
		}
		missing = append(missing, e)
	}
	if len(missing) == 0 {
		return "local", nil
	}
	if err := r.appendToFile(localPath, marshalEvents(missing)); err != nil {
		return "", err
	}
	return "merged", nil
}

// marshalEvents renders raw events back to JSONL in their given order.
func marshalEvents(events []rawEvent) []byte {
	var sb strings.Builder
	for _, e := range events {
		sb.Write(e.line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// appendToFile appends data to path with O_APPEND so the write is atomic and
// interleaves safely with other appenders (a pi still running on this session).
func (r *mergeRunner) appendToFile(path string, data []byte) error {
	if r.dryRun || len(data) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s for append: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("appending to %s: %w", path, err)
	}
	return f.Sync()
}

// rawEvent is one JSONL line plus its event ID, the field the merge keys on.
// Keeping the raw line means an appended event preserves every field of the
// original, including ones this binary does not know about.
type rawEvent struct {
	line []byte
	id   string
}

// parseRawEvents splits a JSONL events file into rawEvent entries.
func parseRawEvents(data []byte) ([]rawEvent, error) {
	var out []rawEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &meta); err != nil {
			return nil, err
		}
		out = append(out, rawEvent{line: []byte(line), id: meta.ID})
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
// merged with the same append rule as the root events file; branches that
// exist only on one side are copied in without touching the other side.
func (r *mergeRunner) mergeBranchesDir(localDir, remoteDir string) error {
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
	return nil
}

// mergeBranchesJSON merges branches.json by branch name, preserving local-only
// branches and the per-branch heads that each side progressed. The active
// branch is the local one unless the remote side is a strict continuation of
// it, in which case the remote active branch is kept.
func (r *mergeRunner) mergeBranchesJSON(localDir, remoteDir string) error {
	lPath := filepath.Join(localDir, "branches.json")
	rPath := filepath.Join(remoteDir, "branches.json")
	_, lStatErr := os.Stat(lPath)
	_, rStatErr := os.Stat(rPath)
	switch {
	case lStatErr != nil && !os.IsNotExist(lStatErr):
		return lStatErr
	case rStatErr != nil && !os.IsNotExist(rStatErr):
		return rStatErr
	}
	lHas := lStatErr == nil
	rHas := rStatErr == nil
	switch {
	case !lHas && !rHas:
		return nil
	case !lHas:
		data, err := os.ReadFile(rPath)
		if err != nil {
			return err
		}
		return r.writeFileAtomic(lPath, data, 0o644)
	case !rHas:
		return nil
	}

	// Both sides have branch state: union branch entries by name, keeping the
	// larger per-branch head and the local active branch unless remote is a
	// strict continuation.
	lBS, err := loadBranches(localDir)
	if err != nil {
		return err
	}
	rBS, err := loadBranches(remoteDir)
	if err != nil {
		return err
	}
	merged := branchState{
		Active:   lBS.Active,
		Branches: make(map[string]BranchInfo, len(lBS.Branches)+len(rBS.Branches)),
	}
	for name, bi := range lBS.Branches {
		merged.Branches[name] = bi
	}
	for name, rbi := range rBS.Branches {
		if lbi, ok := merged.Branches[name]; !ok {
			merged.Branches[name] = rbi
		} else if rbi.Head > lbi.Head {
			merged.Branches[name] = rbi
		}
	}
	// Keep the local active branch unless it does not exist locally and the
	// remote side introduced it (remote-only branch): never point active at a
	// branch that is absent from the merged map.
	if _, ok := rBS.Branches[lBS.Active]; !ok {
		if _, ok := merged.Branches[rBS.Active]; ok {
			merged.Active = rBS.Active
		}
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling merged branches: %w", err)
	}
	return r.writeFileAtomic(lPath, data, 0o644)
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
