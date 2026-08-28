---
name: go-127
description: >
  What changed in Go 1.27 (released August 2026) and how it changes the way Go is
  written in pi-go. Use this skill when writing or reviewing Go that could use a
  1.27 feature, when bumping the `go` directive in go.mod, when a build or test
  behaves differently after a toolchain upgrade, or when `code-guidelines-go`
  points here. Covers generic methods, struct-literal field selectors, generalized
  type inference, the go command and go fix changes, the size-specialized
  allocator, the `goroutineleak` profile, `encoding/json/v2`, the new `uuid` and
  `crypto/mldsa` packages, `strings.CutLast`, `maphash.Hasher`,
  `synctest.Sleep`, `httptest.NewTestServer`, and every removal that can break a
  build.
---

# Go 1.27 — What Changed and What To Do About It

**Released**: August 2026, six months after Go 1.26. Go 1 compatibility holds; almost
all programs compile and run unchanged.

**Sources**: [go.dev/doc/go1.27](https://go.dev/doc/go1.27) (authoritative) ·
[victoriametrics.com/blog/go-1-27](https://victoriametrics.com/blog/go-1-27/) (worked examples).
Every claim below is from one of those two, or from reading
`$(go env GOROOT)/src` on the installed 1.27.0 toolchain.

Companion skill: `code-guidelines-go` holds the standing style rules. This skill holds
only what 1.27 *changed*.

---

## 0. Read this first — `GOEXPERIMENT=none` is set on this machine

`go env` on this host persists `GOEXPERIMENT=none`, which does **not** mean "no extra
experiments" — it clears the 1.27 *baseline*, which enables `JSONv2`, `GreenTeaGC`,
`SizeSpecializedMalloc`, `Dwarf5` and `RandomizedHeapBase64`
(`src/internal/buildcfg/exp.go:81`). Verified consequences here:

```
$ go build ./...                      # importing encoding/json/v2
imports encoding/json/v2: build constraints exclude all Go files in .../encoding/json/v2

$ golangci-lint --version
golangci-lint has version 2.13.1 built with go1.27.0-X:nojsonv2,nogreenteagc,
norandomizedheapbase64,nosizespecializedmalloc
```

So on this machine `encoding/json/v2` will not import, and the 1.27 allocator and GC
work are switched off. Before benchmarking anything or trying a v2 snippet:

```bash
go env GOEXPERIMENT          # prints "none" -> baseline is cleared
go env -u GOEXPERIMENT       # restore the default baseline (user decision — ask first)
GOEXPERIMENT=jsonv2 go build ./...   # or scope it to one command
```

Do not silently change the user's `go env` config. Flag it, and scope the experiment to
the command you are running.

---

## 1. Language changes

### 1.1 Generic methods — methods may declare their own type parameters

The headline change. Before 1.27 a generic operation on a type had to be a
package-level function; now it can live on the type.

```go
// ✗ Before 1.27 — generic op stranded at package scope
type Box[T any] struct{ v T }
func MapBox[T, U any](b Box[T], f func(T) U) Box[U] { return Box[U]{v: f(b.v)} }

// ✓ Go 1.27 — the method declares its own type parameter U
type Box[T any] struct{ v T }
func (b Box[T]) Map[U any](f func(T) U) Box[U] { return Box[U]{v: f(b.v)} }

b := Box[int]{v: 21}
label := b.Map(func(n int) int { return n * 2 }).
    Map(func(n int) string { return fmt.Sprintf("value=%d", n) })
```

Two hard restrictions, both compiler-enforced:

- **Interfaces may not declare type-parameterized methods.** `Map[U any](...)` inside an
  `interface` fails with `interface method must have no type parameters`.
- **A generic method cannot implement an interface method.** So a generic method is
  never part of a type's interface satisfaction set.

**Prescription for pi-go**: this is the right tool for transforms on a concrete generic
container (a typed cache, a typed result set). It is the *wrong* tool anywhere the type
is consumed through an interface — and pi-go's design rule is "define interfaces at the
consumer" (`code-guidelines-go` §1). If a call site takes an interface, keep the
package-level generic function.

### 1.2 Struct literal keys may be any valid field selector

A key in a struct literal is no longer restricted to a top-level field name, so promoted
fields from an embedded struct can be set directly.

```go
type Base struct{ ID int }
type User struct {
    Base
    Name string
}

u := User{Base: Base{ID: 7}, Name: "Mittens"}  // ✗ before 1.27, the only way
u := User{ID: 7, Name: "Mittens"}              // ✓ Go 1.27
```

`go fix`'s new `embedlit` modernizer rewrites the old form for you (see §2.4).

### 1.3 Function type inference generalized

Inference now applies in *every* context where a generic function is assigned to or
converted to a matching function type — not just plain assignment. Composite literals
and conversions now infer too.

```go
func first[T any](s []T) T { return s[0] }
func last[T any](s []T) T  { return s[len(s)-1] }

ops := []func([]int) int{first[int], last[int]}  // ✗ before 1.27: explicit instantiation
ops := []func([]int) int{first, last}            // ✓ Go 1.27: element type drives T=int
```

The old failure was `cannot use generic function without instantiation`
([VM blog](https://victoriametrics.com/blog/go-1-27/)). If you see leftover `[int]`
instantiations in a slice or map literal of function types, they can now be deleted.

---

## 2. Toolchain, `go` command, modules

### 2.1 `go test` runs `stdversion` by default

`go test` now invokes the `stdversion` vet check, which reports use of stdlib symbols
newer than the `go` directive in `go.mod` (adjusted by build tags on the file). This is
the guard that catches "works on my machine": calling `strings.CutLast` from a module
still declaring `go 1.26` is now a **test failure**, not a silent success.

**Prescription**: when you use a 1.27 stdlib symbol, bump `go.mod` in the same change.
Do not `//nolint` around it.

### 2.2 GODEBUG settings that were removed still parse

From 1.27 the go command accepts a removed GODEBUG setting in `go.mod` (`godebug`
lines) and `//go:debug` comments **if it is set to the final default value** established
before removal. Set to an old value, the go command **fails**. So
`//go:debug asynctimerchan=0` still builds; `asynctimerchan=1` is now a hard error.

### 2.3 `go mod tidy` enforces a two-block `require` layout

For modules declaring `go 1.27` or later, `go mod tidy` merges duplicate `require`
blocks into at most two — direct and indirect — preserving attached comment blocks. A
comment block spanning a mixed set of directives is merged onto the direct block.

**Expect a large `go.mod` diff** on the first `go mod tidy` after the version bump.
Review it, do not fight it.

### 2.4 `go fix` modernizers

Verified against `go tool fix help` on 1.27.0:

| Analyzer | Rewrites |
|---|---|
| `atomictypes` | basic types in `sync/atomic` calls → atomic types |
| `embedlit` | references to embedded fields in composite literals (see §1.2) |
| `slicesbackward` | backward loops over slices → `slices.Backward` |
| `unsafefuncs` | unsafe pointer arithmetic → function calls |
| `waitgroupgo` | `wg.Add(1)`/`go`/`wg.Done()` → `wg.Go` — **renamed** from `waitgroup` |

`fmtappendf` was **removed** (stylistic concerns). If a script or CI job names
`waitgroup` or `fmtappendf`, it breaks.

### 2.5 `go doc`

- `go doc example.com/pkg@v1.2.3` — documentation at a specific module version.
- `go doc -ex <pkg>` — list a package's runnable examples (verified: `go doc -ex strings`
  interleaves `func ExampleTrimSpace()` lines under each symbol).
- `go doc bytes.ExampleBuffer` — prints the example's source with comments.

### 2.6 Other tool changes

- **`bzr` support removed** from the go command; modules on Bazaar servers can no longer
  be fetched directly.
- **`go tool trace -http=:6060` now binds localhost only**, matching `go tool pprof`.
  Pass `-http=0.0.0.0:6060` to listen on all interfaces. Relevant to the `go-pprof`
  workflow in `CLAUDE.md`.
- **Response files (`@file`)** are accepted by `compile`, `link`, `asm`, `cgo`, `cover`
  and `pack`, in GCC's format.
- **Linker**: `-macos` and `-macsdk` set OS/SDK versions in `LC_BUILD_VERSION`.
  Defaults: macOS 13.0.0, SDK 26.2.0.

---

## 3. Runtime, GC, performance, profiling

### 3.1 Faster small allocations — free, but off on this machine

The compiler emits calls to size-specialized allocation routines, cutting the cost of
some allocations under 80 bytes by **up to 30%**, ~1% overall in allocation-heavy
programs, at ~60 KB more binary size (workload-independent). Nothing to change in code.

Opt-out is `GOEXPERIMENT=nosizespecializedmalloc`, **expected to be removed in Go 1.28**.
Per §0, `GOEXPERIMENT=none` on this host already disables it — so any allocation
benchmark run here is measuring the *old* allocator.

### 3.2 `goroutineleak` profile is now GA

The goroutine leak profile, experimental in 1.26, is generally available. No
`GOEXPERIMENT` needed; the `goroutineleakprofile` GOEXPERIMENT setting is **deleted**.

```go
// scrape in-process
pprof.Lookup("goroutineleak").WriteTo(os.Stdout, 1)
```

Or the `net/http/pprof` endpoint `/debug/pprof/goroutineleak` — which is what pi-go
should use, since it already serves pprof on `:6060` under `pi --pprof true`.

A goroutine counts as leaked when it is blocked on a concurrency primitive (channel,
`sync.Mutex`, `sync.Cond`, …) that the GC proves unreachable from any runnable goroutine
or anything those could unblock — so it can never be unblocked. **Known blind spot**:
leaks where the primitive is reachable through a global, or through the locals of a
runnable goroutine, are *not* detected. Long-lived registries in pi-go fall exactly in
that gap, so a clean profile is not proof of no leak. Keep `goleak.VerifyTestMain(m)` in
tests.

### 3.3 Goroutine labels in tracebacks

For modules with `go 1.27` or later, `runtime/pprof` goroutine labels appear in the
traceback header line — in panics, SIGQUIT dumps and `runtime.Stack` output:

```
goroutine 1 [running] {request: 42}:
```

Anything already wrapped in `pprof.Do(ctx, pprof.Labels(...), ...)` now leaks that
context into crash output. **Check pi-go's labels for anything sensitive** (session ids
are fine; API keys or prompt text are not) before bumping the `go` directive. Opt-out is
`GODEBUG=tracebacklabels=0`, added in 1.26 and expected to be kept indefinitely.

### 3.4 Compiler changes with observable effects

- Function literal (closure) names are now **simpler and inlining-independent**, and the
  compiler may share one compiled body across multiple inlined instances. Tests that
  assert on symbol names may need updating — better, stop asserting on them.
- Consequently, **comparing function code pointers for equality is more broken than
  before**: distinct closures with different captured data can now share a code pointer.
  Go never defined func comparison beyond `== nil`; 1.27 just exposes it more.
- Relative filenames in `//line` / `/*line*/` directives resolve against the containing
  file's directory ([#70478](https://go.dev/issue/70478)), matching `go/scanner`.

---

## 4. Standard library

### 4.1 `encoding/json/v2` and `encoding/json/jsontext` (new)

```go
import (
    json "encoding/json/v2"
    "encoding/json/jsontext"
)

data, err := json.Marshal(Point{X: 1, Y: 2}, opts...)   // variadic Options
err = json.MarshalWrite(w, v, opts...)                   // to an io.Writer
err = json.MarshalEncode(enc, v, opts...)                // to a *jsontext.Encoder
err = json.Unmarshal(data, &v, opts...)
err = json.UnmarshalRead(r, &v, opts...)
err = json.UnmarshalDecode(dec, &v, opts...)
```

`jsontext` is the syntactic layer: `Encoder`, `Decoder`, `Token`, `Value`, with a state
machine enforcing valid JSON.

v2 chooses stricter defaults than v1: it **rejects invalid UTF-8 in strings** and
**rejects duplicate object names**. It also **does not sort map keys** (v1 always did) —
pass `json.Deterministic(true)` when you need stable output for golden files.

**`encoding/json` (v1) is now backed by v2.** Behavior is preserved; **error message
text may differ**. If any pi-go test asserts on a JSON error *string*, that is the thing
most likely to break — assert on error identity or a substring you control instead.
Marshal is at parity; unmarshal is significantly faster. Escape hatch:
`GOEXPERIMENT=nojsonv2` (expected to be removed in a future release).

**Prescription**: no migration is required and v1 stays supported. Keep pi-go on v1
unless a call site actually wants v2's strictness or its options; do not do a
mechanical sweep.

### 4.2 `uuid` (new, top-level stdlib package)

Implements RFC 9562, seeded from a cryptographically secure RNG. `UUID` is `[16]byte`,
so it is **comparable with `==`** and usable as a map key.

```go
id := uuid.New()      // suitable for most purposes; currently == NewV4
r  := uuid.NewV4()    // 122 bits of randomness
k  := uuid.NewV7()    // 48-bit timestamp prefix — sorts by creation time
u, err := uuid.Parse("f81d4fae-7dec-11d0-a765-00a0c91e6bf6")
m := uuid.MustParse(s)          // panics on bad input
uuid.Nil(); uuid.Max()          // functions, not vars
u.String(); u.Compare(v)        // Compare is big-endian, RFC 9562 §6.11
```

`UUID` implements `encoding.TextMarshaler`, `TextAppender` and `TextUnmarshaler`.

**Prescription for pi-go**: drop `github.com/google/uuid` for new code. For anything
written to `$HOME/.pi-go/sessions/` or used as a database/session key, prefer `NewV7` —
time-ordered ids sort chronologically, which is exactly what the session directory
listing wants.

### 4.3 `crypto/mldsa` (new) — post-quantum signatures

FIPS 204 ML-DSA, in three parameter sets returned by functions: `mldsa.MLDSA44()`,
`MLDSA65()`, `MLDSA87()`. `Parameters` exposes `PublicKeySize()`, `SignatureSize()`,
`String()`.

```go
priv, err := mldsa.GenerateKey(mldsa.MLDSA65())
sig, err := priv.Sign(rand.Reader, msg, crypto.Hash(0))
err = mldsa.Verify(priv.PublicKey(), msg, sig, nil)
```

`crypto/x509` handles ML-DSA keys and signatures; `crypto/tls` gains the `MLDSA44`,
`MLDSA65`, `MLDSA87` `SignatureScheme` values for TLS 1.3. `crypto.MLDSAMu` is a new
`Hash` sentinel for external-μ signing (it panics if passed to `RegisterHash`).

### 4.4 `simd` and `simd/archsimd` (experimental)

`GOEXPERIMENT=simd` at build time. `simd` is portable and vector-size-**agnostic**
(`Int8s`, `Int32s`, `Float32s`, …; width varies by machine), supporting a scalable
subset of `simd/archsimd`. `archsimd` is architecture-specific and unstable: 128-bit on
wasm/arm64/amd64, 256-/512-bit on some amd64. **Not for pi-go** — no hot vector loops
here, and the API is not stable.

### 4.5 New APIs worth using

Signatures below were read from the 1.27.0 GOROOT, not from the release notes prose.

```go
// strings / bytes — replaces the LastIndex dance
func CutLast(s, sep string) (before, after string, found bool)   // strings
func CutLast(s, sep []byte) (before, after []byte, found bool)   // bytes

before, after, found := strings.CutLast("a/b/c", "/")  // "a/b", "c", true
before, after, found  = strings.CutLast("nosep", "/")  // "nosep", "", false
```

```go
// testing/synctest — time.Sleep + synctest.Wait in one call
func Sleep(d time.Duration)
```

```go
// net/http/httptest — in-memory network, no real port, registers t.Cleanup
func NewTestServer(t testing.TB, handler http.Handler) *Server
```

```go
// hash/maphash — the contract for future hash-based containers
type Hasher[T any] interface {
    Hash(*Hash, T)
    Equal(x, y T) bool
}
type ComparableHasher[T comparable] struct{ /* Equal is == */ }
```

```go
// math/big — quotient and remainder with an explicit rounding mode
func (z *Int) Divide(x, y, r *Int, mode RoundingMode) (*Int, *Int)
// modes: big.Trunc, big.Floor, big.Round, big.Ceil
// 7/2 with Ceil  -> q=4, r=-1
// 7/2 with Floor -> q=3, r=1
```

```go
// math/rand/v2 — the top-level N, now a method (first stdlib generic method)
func (r *Rand) N[Int intType](n Int) Int

r := rand.New(rand.NewPCG(1, 2))
r.N(100)                  // int in [0, 100)
r.N(5 * time.Second)      // works for any integer-kind type, incl. Duration
```

```go
// net/url
func (u *URL) Clone() *URL
func (vs Values) Clone() Values
```

Also new: `database/sql.ConvertAssign(scanCtx driver.ScanContext, dest any, src driver.Value) error`;
`database/sql/driver.RowsColumnScanner`; `go/constant.StringLen(x Value) int64`;
`(*go/scanner.Scanner).End() token.Pos`; `(*go/token.File).String()`;
`go/types.Hasher` and `HasherIgnoreTags`.

**Prescriptions**:

- `strings.CutLast` / `bytes.CutLast` replace `LastIndex` + two slice expressions.
  Grep pi-go for `strings.LastIndex` when touching those call sites.
- `synctest.Sleep(d)` replaces the `time.Sleep(d); synctest.Wait()` pair that
  `code-guidelines-go` §6 currently spells out. Prefer it inside a bubble: when the test
  and the code under test sleep the same duration, plain `time.Sleep` leaves the wake
  order unpredictable.
- `httptest.NewTestServer(t, h)` binds no TCP port and self-cleans, so `defer srv.Close()`
  goes away and round-trips can run in synthetic time under `testing/synctest`. Worth
  trying against the `internal/cli` sandbox trap in `CLAUDE.md`, where
  `httptest.NewServer` panics in `newLocalListener` — but that has not been demonstrated
  here, so verify before relying on it.
- `maphash.Hasher[T]` is a contract for *future* containers; there is no stdlib hash set
  yet. Use it only if pi-go writes its own hash-based container, and honour the rule that
  `Equal` values must `Hash` identically.

### 4.6 Behaviour changes that can bite

| Package | Change | What to do |
|---|---|---|
| `net/http` | HTTP/1 `Response.Body` **auto-drains** unread content on `Close` (to a conservative limit) so the connection can be reused | Usually a win. If you `Close` early to abort a large download, set `Transport.DisableKeepAlives = true` |
| `net/http` | HTTP/2 server honours **RFC 9218 client priorities** | `Server.DisableClientPriority = true` restores round-robin |
| `net/http` | New `Server.MaxHeaderValueCount` caps accepted header values; default `http.DefaultMaxHeaderValueCount` (500) | Set it on pi-go's servers if a client legitimately sends many values |
| `net/http` | ALPN negotiation now works on a user-supplied `net.Conn` that has `ConnectionState() tls.ConnectionState` | — |
| `compress/flate` | Compression speed improved; **exact encoded bytes differ from 1.26** — propagates to `archive/zip`, `compress/gzip`, `compress/zlib`, `image/png` | Any golden file holding compressed bytes will fail. Regenerate; do not assert on compressed output |
| `unicode` | Unicode 15 → **17** | Code points unassigned before (e.g. U+1FADC) now report `IsSymbol`/`IsGraphic` true. Golden files of width-measured or classified text may shift |
| `net` | `UnixConn` read methods return `io.EOF` directly instead of wrapping in `net.OpError` | Replace `net.OpError` unwrapping with `errors.Is(err, io.EOF)` |
| `crypto/x509` | `SystemCertPool` respects `SSL_CERT_FILE`/`SSL_CERT_DIR` on **Windows and Darwin**, switching to the Go verifier | `GODEBUG=x509sslcertoverrideplatform=0` restores platform APIs |
| `crypto/x509/pkix` | `RDNSequence.String` / `Name.String` render string-typed values as strings even for unrecognized OIDs (previously hex DER) — [#33093](https://go.dev/issue/33093) | Certificate-subject golden strings may change |
| `crypto/ecdsa` | `PrivateKey.Sign` validates hash length when `SignerOpts` is non-nil | Passing a mismatched digest now errors |
| `runtime/secret` | Goroutines created in secret mode now run in secret mode | — |

---

## 5. Deprecations and removals

**Removed permanently — these now fail the build or the go command:**

| Removed | Since | Replacement / effect |
|---|---|---|
| `asynctimerchan` GODEBUG | 1.23 | `time` channels are **always unbuffered**. `asynctimerchan=1` is now a hard error; `=0` still parses (§2.2) |
| `gotypesalias` GODEBUG | 1.22 | `go/types` always produces an `Alias` node for alias declarations |
| `tlsunsafeekm`, `tlsrsakex`, `tls10server` | 1.22 | no escape hatch left |
| `tls3des`, `x509keypairleaf` | 1.23 | no escape hatch left |
| `goroutineleakprofile` GOEXPERIMENT | 1.26 | the profile is GA (§3.2) |
| `bzr` support in the go command | — | vendor the module, or move it |
| `fmtappendf` go fix analyzer | — | none (dropped on style grounds) |

**Renamed**: go fix `waitgroup` → `waitgroupgo`.

**Newly deprecated**: `crypto/tls.Config.Rand` — for deterministic testing use
`testing/cryptotest.SetGlobalRandom`. Post-quantum hybrid key exchanges can now be
enabled explicitly via `Config.CurvePreferences` even under `tlsmlkem=0` /
`tlssecpmlkem=0`; those GODEBUGs were only ever meant to affect the default set used when
`CurvePreferences` is nil.

**Temporary opt-outs, on a clock** — do not build a workflow on these:
`GOEXPERIMENT=nosizespecializedmalloc` (expected gone in 1.28) and
`GOEXPERIMENT=nojsonv2` (expected gone in a future release).

---

## 6. Porting and compatibility checklist

Work through this when bumping the `go` directive to `1.27`:

1. **`go.mod`**: bump the `go` line in the same change as the first 1.27 stdlib symbol —
   `go test` now runs `stdversion` and will fail otherwise (§2.1).
2. **`go mod tidy`**: expect the `require` blocks to be consolidated into two (§2.3).
   Review the diff once, then move on.
3. **GODEBUG audit**: grep `go.mod` `godebug` lines and `//go:debug` comments for
   `asynctimerchan`, `gotypesalias`, `tlsunsafeekm`, `tlsrsakex`, `tls3des`,
   `tls10server`, `x509keypairleaf`. Any of them set to a non-default value now fails.
4. **Golden files**: regenerate anything holding DEFLATE/gzip/zip/PNG bytes, JSON error
   strings, certificate subject strings, or Unicode-classified text (§4.6).
5. **pprof labels**: confirm no `pprof.Labels` value carries a secret before tracebacks
   start printing them (§3.3).
6. **Symbol-name tests**: closure names changed; func-pointer equality is less reliable
   (§3.4).
7. **macOS**: 1.27 requires **macOS 13 Ventura or later** — announced in the 1.26 notes.
8. **linux/ppc64 (big-endian)**: now targets the **ELFv2** ABI (kernel 3.13+; RHEL7
   backported to 3.10). cgo, PIE and external linking are supported but need an
   ELFv2-compatible runtime. Pure-Go builds still default to static internal linking;
   set `CGO_ENABLED=0` for a static binary from a cgo-optioned program.
9. **`//go:linkname`**: 1.27 tightens unsanctioned linknames (a `linknamestd` directive
   restricts some to the stdlib, and `cmd/link` now checks linkname access to assembly
   symbols). A dependency reaching into runtime internals is the likely breakage; test
   the dependency tree early. *(This one is from the VM blog's "hidden gems", not the
   release notes — treat it as a heads-up to verify, not as a documented contract.)*
10. **Tooling**: `golangci-lint` v2.13.1 parses generic methods and struct-literal field
    selectors cleanly — verified locally on both constructs, 0 issues, exit 0.

---

## 7. Quick reference

| Concern | Go 1.27 | Was |
|---|---|---|
| Generic op on a concrete container | method with own type params | package-level generic func |
| Setting a promoted field | `User{ID: 7}` | `User{Base: Base{ID: 7}}` |
| Generic func in a slice literal | `[]func([]int) int{first, last}` | `{first[int], last[int]}` |
| Split on last separator | `strings.CutLast` / `bytes.CutLast` | `strings.LastIndex` + slicing |
| Sleep inside a synctest bubble | `synctest.Sleep(d)` | `time.Sleep(d); synctest.Wait()` |
| HTTP test server | `httptest.NewTestServer(t, h)` | `httptest.NewServer(h)` + `defer Close()` |
| UUIDs | stdlib `uuid` (`NewV7` for keys) | `github.com/google/uuid` |
| Bounded random from own source | `r.N(100)` | `rand.N(100)` (global) |
| Rounded integer division | `big.Int.Divide(x, y, r, big.Floor)` | `Quo`/`Mod` + manual adjust |
| Deep-copy a URL | `u.Clone()` | manual struct copy |
| Leaked goroutines in prod | `/debug/pprof/goroutineleak` | experiment / `goleak` in tests only |
| Deterministic JSON map order | `json.Deterministic(true)` (v2) | implicit in v1 |
| `wg.Add`/`Done` modernizer | `go fix -waitgroupgo` | `-waitgroup` |
