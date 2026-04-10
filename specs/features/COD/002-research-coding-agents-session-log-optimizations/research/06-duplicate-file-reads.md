# Research: Duplicate File Reads (Context Preservation)

## Problem Statement

File contents are read fresh on each call to the read tool. No caching exists, leading to:
- Repeated reads of the same file
- Performance overhead for large files
- Context loss between reads

## Current State

### readHandler Function (read.go:41-96)

```go
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
    if input.FilePath == "" {
        return ReadOutput{}, fmt.Errorf("file_path is required")
    }
    
    // Every read hits the filesystem
    data, err := sb.ReadFile(input.FilePath)
    if err != nil {
        return ReadOutput{}, fmt.Errorf("reading file: %w", err)
    }
    
    // ... offset/limit handling ...
    
    return ReadOutput{...}, nil
}
```

### Key Characteristics

1. **No caching**: Each read is fresh from filesystem
2. **No session awareness**: Reads aren't tracked in session
3. **Offset/limit pagination**: Implicit truncation after 2000 lines
4. **Line-numbered output**: Format includes line numbers

### Session State (store.go)

Session state is stored as `map[string]any`:
```go
type fileSession struct {
    state  map[string]any  // populated via StateDelta
    events []*session.Event
    meta   Meta
}
```

**No file read tracking exists in session state.**

## Session State Keys

From session-and-recovery.md:
- `fileSession.state`: `map[string]any` for arbitrary state
- Temp keys: prefix `"_temp_"` stripped before persist
- File reads: not tracked anywhere

## Root Cause Analysis

1. **No cache layer**: Files read directly via sandbox
2. **No session integration**: Reads not recorded in session
3. **No invalidation**: No way to know if file changed

## Investigation Findings

### Existing Caching Mechanisms

**LSP Diagnostics** (lsp/manager.go:296-301):
```go
func (m *Manager) CachedDiagnostics(fileURI string) []Diagnostic {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.diagnostics[fileURI]
}
```
- Map-based cache with mutex
- Indexed by file URI
- Thread-safe

**Session Cache** (store.go:39-40):
```go
type FileService struct {
    sessions map[string]*fileSession  // in-memory session cache
}
```

### Compactor Metrics (compactor_metrics.go)

```go
type CompactMetrics struct {
    mu      sync.Mutex
    Records []CompactRecord
}
```
- Mutex-protected metrics
- Persisted to disk

## Options for Fix

### Option A: In-Memory File Content Cache (Medium Risk)
- Add cache to `Sandbox` or `readHandler`
- LRU eviction policy
- Thread-safe with mutex
- Invalidate on write

### Option B: Session-Level File Tracking (Medium Risk)
- Track file reads in session state
- Store metadata (path, lines, timestamp, hash)
- Persist across sessions
- Invalidate on file modification

### Option C: Hybrid Approach (High Complexity)
- Combine in-memory cache with session tracking
- L1: In-memory LRU
- L2: Session metadata
- L3: Filesystem

## Recommendation

**Implement Option A first**, then Option B:

### Option A: In-Memory Cache

```go
type fileContentCache struct {
    mu      sync.RWMutex
    entries map[string]*cachedFile
    maxSize int           // max entries
    maxAge  time.Duration // max age before refresh
}

type cachedFile struct {
    content    []byte
    totalLines int
    readAt     time.Time
    size       int64
    mtime      int64 // modification time for invalidation
}
```

**Integration with readHandler**:
```go
func readHandler(sb *Sandbox, input ReadInput, cache *fileContentCache) (ReadOutput, error) {
    // Check cache first
    if cache != nil {
        if cached := cache.get(input.FilePath); cached != nil {
            // Check if still valid (mtime matches)
            if sb.Stat(input.FilePath).ModTime().Unix() == cached.mtime {
                return cached.toReadOutput(input.Offset, input.Limit), nil
            }
        }
    }
    
    // Read from filesystem
    data, err := sb.ReadFile(input.FilePath)
    // ... rest of logic ...
    
    // Update cache
    if cache != nil {
        cache.put(input.FilePath, data, totalLines, mtime)
    }
}
```

### Option B: Session-Level Tracking

```go
// FileReadEntry in session state
type FileReadEntry struct {
    Path       string    `json:"path"`
    Lines      int       `json:"lines"`
    ReadAt     time.Time `json:"read_at"`
    Hash       string    `json:"hash,omitempty"` // content hash
    Truncated  bool      `json:"truncated"`
}

// Session state key
const KeyFileReads = "file_reads"
```

## Implementation Location

- **Option A**: `internal/tools/read.go` + `internal/tools/cache.go` (new)
- **Option B**: `internal/session/store.go` + `internal/tui/tui.go`

## Cache Invalidation

Critical for correctness:
1. **On write**: Invalidate cache entry for written file
2. **On edit**: Invalidate cache entry for edited file
3. **On delete**: Remove cache entry
4. **On mtime change**: Compare modification times

## Performance Considerations

1. **Memory usage**: Limit cache size (e.g., 100 files, 10MB total)
2. **Eviction policy**: LRU or LFU
3. **Concurrent access**: RWMutex for thread safety
4. **Hash computation**: xxhash or similar for fast hashing

## Test Strategy

1. Unit test cache hit/miss behavior
2. Unit test invalidation on write
3. Integration test with repeated reads
4. Performance test with large files

## Dependencies

- None - new implementation

## Risk Assessment

- **Risk Level**: Medium
- **Reasoning**: Cache invalidation bugs could cause stale reads
- **Testing**: Comprehensive testing required

## Relationship to Other Optimizations

Complements Optimization 4 (File Not Found Retry):
- Retry handles transient errors
- Cache reduces repeated reads
- Together provide reliability and performance

Also relates to Optimization 2 (Edit Retry):
- Cache should be invalidated after edit
- Consistent view of file state
