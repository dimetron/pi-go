# Issue 007: Data Loss — `write`/`edit` Truncate Files Before Writing, and Never Flush

## Summary

`Sandbox.WriteFile` — the single funnel for both the `write` and the `edit` tool — opens the
target with `O_TRUNC` and only then writes the new content. The original file is destroyed at
`open()`, before a single new byte lands. Anything that stops the process in that window (quit,
Ctrl-C, panic, OOM, `kill`, crash) leaves the file **empty or partially written, with the
original unrecoverable**.

Separately, there is **not a single `.Sync()` call anywhere in the non-vendor tree**. Nothing the
agent writes is ever flushed to disk, so even a cleanly completed write can be lost to power loss
within the writeback window.

This is not theoretical. A user reported losing a file around app quit, which is exactly the
failure this produces.

## Incident

**Reported:** a file edited by the agent was lost after the app quit.
**Reproduced:** the destructive window is observable on every single write.

### What happens

`internal/tools/sandbox.go:304-325`:

```go
f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)  // ← original destroyed HERE
if err != nil {
    return err
}
_, writeErr := f.Write(data)   // ← new bytes land here
closeErr := f.Close()          // ← no Sync() anywhere
```

Both tools reach it:

- `internal/tools/write.go:39` → `sb.WriteFile(input.FilePath, []byte(input.Content), 0o644)`
- `internal/tools/edit.go:116` → `sb.WriteFile(input.FilePath, []byte(result), 0o644)`

The write is therefore **non-atomic** (a reader or a crash can observe a torn file) and
**non-durable** (a completed write is not on disk).

### Evidence

**1. The file is observably empty mid-write.** A probe that polls the target's size while
`Sandbox.WriteFile` replaces a 25-byte file with a 64 MB payload:

```
original size: 25 bytes
smallest size observed during WriteFile: 0 bytes
file was observably EMPTY (0 bytes) mid-write: true
```

A crash at that instant loses the file. There is no copy of the original anywhere.

**2. Nothing is ever fsynced.**

```
$ grep -rn "\.Sync()" --include="*.go" . | grep -v _test.go | grep -v /vendor/
(no matches)
```

**3. The codebase already knows the correct pattern** — it just isn't used by the tools:

- `internal/atif/writer.go:119-135` — `os.CreateTemp` + `os.Rename`
- `internal/session/store.go:913` — `os.Rename` over a temp file

## Root Cause

POSIX has no atomic "replace the contents of this file" operation. `O_TRUNC` + `write` is a
two-step, interruptible sequence with a window in which the file holds neither the old content nor
the new. `rename()` is the only primitive that swaps a file's contents indivisibly.

### Why `fsync` alone does not fix it

This is the important part, and it is counter-intuitive: **flushing does not close the window.**

`fsync` can only run *after* `Write()` returns — but the original is already gone at `open()`.
Adding a flush adds a line of code *after* the point of no return.

```
open(O_TRUNC)  ← original destroyed HERE
write(data)    ← new bytes land here
fsync()        ← never reached if the process dies above
close()
```

The two mechanisms protect against different failures:

| Failure                                    | Fixed by `fsync`? | Fixed by atomic rename? |
|--------------------------------------------|-------------------|-------------------------|
| Process quits / crashes / killed mid-write | **No**            | **Yes**                 |
| Power loss *after* a successful write      | **Yes**           | No (alone)              |

The reported incident is row one — the row `fsync` does not cover. Atomicity is the required fix;
durability is a cheap and worthwhile addition on top.

### Middle grounds that do not work

- **Drop `O_TRUNC`, write in place, `ftruncate` to the new length at the end.** Avoids the
  zero-length window, but mid-write the file holds *new prefix + old suffix* — silently corrupt
  rather than obviously empty. Strictly worse: the loss goes unnoticed.
- **Copy to `.bak` first, then write in place.** The target is still torn on crash; recovery
  becomes a manual step and the tree accumulates litter.
- **`O_TMPFILE` + `linkat`.** Linux-only; the primary dev platform here is macOS.

## Fix

Replace the body of `Sandbox.WriteFile` with a write-temp → fsync → rename → fsync-parent
sequence. `os.Root` exposes `OpenFile`, `Rename`, `Chmod`, `Stat`, `Lstat`, `Readlink` and
`Remove` (Go ≥ 1.25), so the whole operation stays inside the sandbox — verified against Go 1.26.

**Prioritization: the rename is the fix; the fsync is a bonus.** They cover different failures
(see table above) and must stay separable in the implementation. If write latency ever becomes a
problem, the flush is the part to make configurable or drop — never the rename.

### Design

Extract the sequence into an unexported `atomicWriteFile(root *os.Root, rel string, data []byte,
perm os.FileMode) error` helper so the follow-up sweep (see Scope) can reuse it, and have
`Sandbox.WriteFile` call it after the existing resolve/`MkdirAll` steps.

1. **Resolve symlinks first** — a bounded loop (~40 hops, matching the kernel's limit) over
   `root.Lstat` + `root.Readlink`, resolving each link *inside the root* so sandbox escapes stay
   blocked. Rewrites `rel` to the real target; a dangling link resolves to its target path
   (creating the file there, as today).
2. **Capture the mode to apply** — `root.Stat(rel)`: use the original's `Mode().Perm()` when the
   file exists, else `perm`.
3. **Create the temp in the same directory** as the target, `O_WRONLY|O_CREATE|O_EXCL`, with a
   random suffix (e.g. `.pi-tmp-<rand>`); same directory means same filesystem, so the later
   `rename` cannot fail with `EXDEV`. If creation fails with a permission error, **fall back to
   the current in-place write** (read-only-dir case, see semantics below).
4. **`defer root.Remove(tmp)`** — a no-op after a successful rename; guarantees no litter on any
   failure path.
5. **Write the data, `f.Sync()`, `f.Close()`** — content durability. Any error aborts before the
   target is touched: the original is intact on every failure path up to here.
6. **`root.Rename(tmp, rel)`** — the atomic step. This is the only instant at which the target
   changes, and it changes indivisibly from fully-old to fully-new.
7. **`Sync()` the parent directory** (open it via `root.Open`, `Sync`, `Close`) so the rename
   itself survives power loss — **best-effort**: directory fsync fails on some filesystems and
   must not fail the write.

Sketch:

```go
func atomicWriteFile(root *os.Root, rel string, data []byte, perm os.FileMode) error {
    rel, err := resolveSymlinks(root, rel) // step 1; bounded, in-root
    if err != nil {
        return err
    }
    mode := perm
    if fi, err := root.Stat(rel); err == nil { // step 2: preserve mode (0755 stays 0755)
        mode = fi.Mode().Perm()
    }
    tmp := rel + ".pi-tmp-" + randSuffix() // step 3: same dir → same fs → no EXDEV
    f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
    if err != nil {
        if errors.Is(err, fs.ErrPermission) {
            return writeInPlace(root, rel, data, perm) // read-only dir: current behavior
        }
        return err
    }
    defer root.Remove(tmp) // step 4: no-op after rename; cleanup on every failure path

    if _, err := f.Write(data); err != nil { // step 5
        f.Close()
        return err
    }
    if err := f.Sync(); err != nil { // content durability
        f.Close()
        return err
    }
    if err := f.Close(); err != nil {
        return err
    }
    if err := root.Rename(tmp, rel); err != nil { // step 6: THE atomic step
        return err
    }
    syncDir(root, filepath.Dir(rel)) // step 7: rename durability; best-effort by design
    return nil
}
```

`writeInPlace` is the current `O_TRUNC` body, kept only as the permission-error fallback.

### Semantics this changes — all three measured against current behavior

**1. File mode must be explicitly preserved.** Today, overwriting an existing `0755` script with
`perm=0644` **keeps `0755`** (measured) — `O_CREATE` only applies the mode at creation. After a
rename the *new* file's mode wins, so an executable would silently lose `+x`. Mitigation: `Stat`
the original and create the temp with that mode (design step 2); fall back to `perm` when the
file does not yet exist. Note the process umask masks the mode at `O_CREATE` — if the preserved
mode has bits the umask would strip, an explicit `Chmod` on the temp is required after creation.

Not preservable by rename: **ownership, ACLs and extended attributes** (on macOS, quarantine
flags). These are dropped. Accepted.

**2. Symlinks must be resolved first.** Today, writing to a symlink **follows it** — the link
survives and the target's content is updated (measured). `rename()` would **replace the symlink
with a regular file**, destroying it. Mitigation: resolve the link and write to the real target.

**3. Read-only directories — an unavoidable regression.** Today, overwriting a writable file
inside a **non-writable directory succeeds** (measured, `err=nil`). Atomic rename fundamentally
requires write permission on the *directory*, and creating the temp there fails with
`permission denied` (measured). Mitigation: fall back to the current in-place write when temp
creation fails with `EPERM`, so nothing that works today starts failing — accepting
non-atomicity in that narrow case.

### Other consequences

- **The inode changes.** Hard links break (the other link keeps the old content) and anything
  holding an open fd keeps reading the old content. File watchers observe `RENAME`/`CREATE`
  rather than `WRITE`; hot-reloaders keying on `WRITE` may need attention.
- **`fsync` costs latency** (~ms per file). An agent writing many files gets measurably slower.
  Atomicity and durability are separable: `rename` alone fixes the reported data loss at no fsync
  cost. If the latency proves painful, the flush is the part to drop — not the rename.
- **Transient 2× disk usage**, and leftover temp files if the process dies between create and
  rename (litter, not data loss — hence the `defer` cleanup).

## Scope

`Sandbox.WriteFile` is the fix that matters, because it backs the two tools the agent uses to
mutate the user's source. The same non-durable `os.WriteFile` pattern appears in ~20 other places
(config, session store, theme, guardrail, `internal/lsp/hooks.go:118` writing formatter output
back over source files). Those are **out of scope here** but worth a follow-up sweep — the LSP
formatter hook is the most concerning, as it also overwrites user source. The `atomicWriteFile`
helper is deliberately shaped so that sweep is a mechanical call-site swap.

## Acceptance criteria

- [ ] A crash at any point during `write`/`edit` leaves the target either fully old or fully new —
  never empty or torn.
- [ ] Overwriting an existing `0755` file preserves `0755`.
- [ ] Writing through a symlink still updates the link's target and leaves the symlink intact.
- [ ] Overwriting a writable file in a non-writable directory still succeeds (in-place fallback).
- [ ] No temp files are left behind on the success path, or on a failed write.
- [ ] A regression test asserts the file is never observed at zero length during a replace.

## Verification

- Unit tests for each acceptance criterion above (mode, symlink, read-only dir, temp cleanup).
- The size-polling probe used as evidence, inverted into a regression test: during a
  `WriteFile` that replaces a large file, the target must **never** be observed at 0 bytes.
- `go test ./internal/tools/`, `go vet`, `golangci-lint run`.
