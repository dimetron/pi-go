package tools

import (
	"fmt"
	"os"
	"sync"
)

// ReadLedger records what the agent has actually seen of each file.
//
// It exists to stop one specific failure: an overwrite of a file the agent
// never read, or read only a window of. Both destroy content the agent did not
// know was there, and neither is recoverable from the transcript, because the
// bytes it replaced were never in the transcript to begin with.
//
// Tracking a partial view separately from a full one is what makes the refusal
// accurate rather than merely cautious. "You have not read this file" is wrong
// and confusing when the agent has read 2000 of its 5000 lines; the honest
// message names the real problem, which is the 3000 lines it has not seen.
type ReadLedger struct {
	mu      sync.Mutex
	entries map[string]ledgerEntry
}

type ledgerEntry struct {
	mtime   int64
	size    int64
	partial bool
}

// NewReadLedger creates an empty ledger.
func NewReadLedger() *ReadLedger {
	return &ReadLedger{entries: make(map[string]ledgerEntry)}
}

// Record notes that path was read, and whether the view was a partial one.
func (l *ReadLedger) Record(path string, info os.FileInfo, partial bool) {
	if l == nil || info == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// A full read supersedes an earlier partial one; a partial read must not
	// downgrade a full view of the same unchanged bytes.
	prev, seen := l.entries[path]
	if seen && !prev.partial && prev.mtime == info.ModTime().UnixNano() && prev.size == info.Size() {
		partial = false
	}
	l.entries[path] = ledgerEntry{
		mtime:   info.ModTime().UnixNano(),
		size:    info.Size(),
		partial: partial,
	}
}

// Touch refreshes the recorded mtime and size after the agent itself changed
// the file, keeping whatever view it already had.
//
// A targeted edit is not a reason to make the agent re-read: it knows exactly
// what it changed. Without this the mtime check would fire on the agent's own
// edit and demand a re-read that would teach it nothing. A file the ledger has
// never seen stays unseen — Touch does not grant a view.
func (l *ReadLedger) Touch(path string, info os.FileInfo) {
	if l == nil || info == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, seen := l.entries[path]
	if !seen {
		return
	}
	entry.mtime = info.ModTime().UnixNano()
	entry.size = info.Size()
	l.entries[path] = entry
}

// Forget drops a path, so a later write is judged on a fresh read rather than
// on a view of bytes that no longer exist.
func (l *ReadLedger) Forget(path string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, path)
}

// CheckOverwrite reports why path may not be overwritten, or nil when it may.
//
// Creating a file that does not exist yet is always allowed: there is nothing
// to destroy, and requiring a read first would only produce a pointless error.
func (l *ReadLedger) CheckOverwrite(path string, info os.FileInfo) error {
	if l == nil || info == nil {
		return nil
	}

	l.mu.Lock()
	entry, seen := l.entries[path]
	l.mu.Unlock()

	if !seen {
		return fmt.Errorf("%s has not been read yet — read it before overwriting, "+
			"so the content being replaced is known", path)
	}

	current := info.ModTime().UnixNano()
	if entry.mtime != current || entry.size != info.Size() {
		return fmt.Errorf("%s changed on disk since it was read — read it again before overwriting", path)
	}

	if entry.partial {
		return fmt.Errorf("only part of %s has been read — read the rest before overwriting, "+
			"or the unread part will be silently discarded", path)
	}
	return nil
}
