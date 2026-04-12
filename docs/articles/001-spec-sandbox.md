# macOS `sandbox-exec` and Modern Replacements

> **Status:** Research note · April 2026
> **Context:** Agent sandbox isolation for KAgent / KBX ecosystem

---

## What is `sandbox-exec`?

`sandbox-exec` is a macOS CLI utility that runs a process inside Apple's **Seatbelt** sandbox framework. Available since
Leopard (10.5), it uses a Scheme-based policy language (SBPL) to define allow/deny rules for file access, networking,
IPC, syscalls, and more.

```bash
# Built-in profile
sandbox-exec -n no-internet /usr/bin/curl https://example.com

# Custom profile file
sandbox-exec -D HOME="$HOME" -D CWD="$PWD" -f pi-profile.sb pi --model minimax-m2.7:cloud

# Inline profile
sandbox-exec -p '(version 1)(allow default)(deny network*)' /usr/bin/curl https://example.com
```

### SBPL Profile Basics

```scheme
(version 1)
(deny default)                                ; deny-all baseline
(allow process-exec)                           ; allow the process to exec
(allow file-read* (subpath "/usr"))            ; read anything under /usr
(allow file-read* (literal "/etc/resolv.conf"))
(allow network-outbound (remote tcp "example.com:443"))
(deny file-write*)                             ; no writes anywhere
```

**Key filter operations:** `file-read*`, `file-write*`, `network-outbound`, `network-inbound`, `process-exec`,
`process-fork`, `mach-lookup`, `sysctl-read`, `signal`, `ipc-posix-shm`

**Path matchers:** `literal` (exact), `subpath` (recursive), `regex`, `prefix`

**Built-in profiles:** `/usr/share/sandbox/` and `/System/Library/Sandbox/Profiles/`

### Deprecation Status

Apple has marked `sandbox-exec` as deprecated since ~2016. The man page recommends adopting App Sandbox (
entitlements-based). However:

- **Still functional** through macOS Sequoia and macOS 26 (Tahoe)
- **No documented replacement** for the CLI use case
- Apple's own Quinn "The Eskimo" (DTS) confirmed: App Sandbox is not a direct replacement — it's designed for apps
  opting in, not for sandboxing arbitrary third-party binaries
- The SBPL language was never publicly documented; Apple's own profiles are the best reference

---

## Why There Is No Single Replacement

Apple deprecated `sandbox-exec` without providing an equivalent CLI tool. The gap exists because:

1. **App Sandbox** requires entitlements embedded via `codesign` — designed for App Store apps, not ad-hoc CLI isolation
2. **Endpoint Security framework** is for monitoring, not sandboxing
3. **SBPL was never documented** for third-party use, making it unsupportable
4. **macOS lacks Linux-style namespaces** — no `unshare`, no cgroups, no Landlock

This has led to a fragmented ecosystem of workarounds.

---

## Modern Alternatives

### 1. `sandbox-exec` (Still in Production Use)

Despite deprecation, this remains the most widely used approach for native macOS process isolation.

**Who uses it:**

- **Claude Code** (`/sandbox` command) — Anthropic's open-source `sandbox-runtime` uses `sandbox-exec` on macOS,
  bubblewrap on Linux
- **OpenAI Codex CLI** — hardcodes `/usr/bin/sandbox-exec` in `codex-rs/core/src/seatbelt.rs`
- **Google Gemini CLI** — also relies on `sandbox-exec`
- **ai-jail** — cross-platform sandbox using `sandbox-exec` on macOS, bubblewrap on Linux

**Risk:** Apple could remove it in any future macOS release.

### 2. Apple Container (macOS 26+ / Tahoe)

The closest thing to an official successor from Apple.

- **What:** Open-source tool for running Linux containers in lightweight VMs, written in Swift
- **How:** Each container gets its own micro-VM (unlike Docker's shared VM)
- **Startup:** Sub-second on Apple Silicon
- **Status:** v0.9.0 (Feb 2026), pre-1.0 but runtime is solid
- **Install:** `.pkg` from GitHub releases
- **Limitation:** Linux guests only — cannot sandbox native macOS binaries

```bash
container run -it --rm \
  --cpus 2 --memory 4G \
  -v "$WORKSPACE:/workspace" \
  -e ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY}" \
  claude-sandbox \
  claude --dangerously-skip-permissions "$@"
```

### 3. SandVault — User Account + Seatbelt (Defense in Depth)

GitHub: `webcoyote/sandvault`

- **What:** Creates a limited macOS user account (`sandvault-$USER`) and runs commands as that user, *plus* applies
  `sandbox-exec` policies
- **Designed for:** AI agents (Claude Code, Codex, Gemini)
- **Isolation model:**
    - Writable: `/Users/Shared/sv-$USER` (shared workspace)
    - Writable: `/Users/sandvault-$USER` (sandvault home)
    - Readable: `/usr`, system dirs
    - Everything else: denied
- **No VM overhead:** Instant user switching via `su`/`sudo`

```bash
sv claude                          # sandboxed Claude Code
sv --no-sandbox codex              # user isolation only (no sandbox-exec)
sv shell ~/projects/my-app         # sandboxed shell
```

### 4. Alcoholless — Lightweight User-Based Sandbox

GitHub: `AkihiroSuda/alcless` (NTT Labs, Akihiro Suda — also creator of nerdctl/rootless containers)

- **What:** Runs commands as a separate restricted user with a copy of the working directory
- **How:** Uses `su`, `sudo`, `rsync` — 1990s Unix primitives, no kernel extensions
- **Changed files sync back** to original directory on exit

```bash
alclessctl create default          # one-time setup
alcless brew install xz            # sandboxed Homebrew
alcless xz SOME_FILE               # sandboxed compression
```

### 5. Birdcage (Rust Crate)

Crate: `birdcage` (by Phylum)

- **What:** Unified Rust API wrapping Landlock on Linux and `sandbox-exec` on macOS
- **Limitation:** Designed for restricting the *calling* process, not launching a child in an isolated environment with
  custom mounts
- **Use case:** Embedding sandbox restrictions into your own Rust binary

### 6. Full VM Isolation

| Tool                 | Type                           | Notes                                                |
|----------------------|--------------------------------|------------------------------------------------------|
| **Docker Sandboxes** | microVM (Docker Desktop 4.58+) | Strongest isolation; `docker sandbox run claude`     |
| **Lima**             | Linux VM (CNCF project)        | ~20k stars, good for dev environments                |
| **Colima**           | Docker-on-Lima wrapper         | Open-source Docker Desktop alternative               |
| **Tart**             | macOS/Linux VM                 | Native macOS guests, Softnet for network filtering   |
| **OrbStack**         | Docker replacement             | Does NOT support Docker Sandboxes (open issue #2295) |

---

## Comparison Matrix

| Approach         | Isolation Level | Startup | RAM Overhead | macOS Native    | Agent-Ready               |
|------------------|-----------------|---------|--------------|-----------------|---------------------------|
| `sandbox-exec`   | ACL only        | instant | zero         | ✅               | ✅ (Claude, Codex, Gemini) |
| Apple Container  | microVM         | <1s     | low          | ✅ (26+)         | ✅                         |
| SandVault        | user + ACL      | ~1s     | minimal      | ✅               | ✅                         |
| Alcoholless      | user separation | ~1s     | minimal      | ✅               | partial                   |
| Docker Sandboxes | microVM         | seconds | moderate     | ❌ (Linux guest) | ✅                         |
| Tart             | full VM         | seconds | high         | ✅ (macOS guest) | partial                   |
| Lima/Colima      | full VM         | seconds | moderate     | ❌ (Linux guest) | partial                   |

---

## Linux Comparison (for Reference)

On Linux, the sandbox story is much richer:

- **bubblewrap (bwrap):** Namespace-based isolation (used by Flatpak, Claude Code on Linux)
- **Landlock (5.13+):** Capability-based filesystem ACLs, ~50 lines to add
- **seccomp-BPF:** System call filtering
- **Namespaces:** Process, network, mount, user isolation
- **Firecracker:** microVM with <125ms startup (used by AWS Lambda)

The `ai-jail` project recommends: bwrap as primary + Landlock as defense-in-depth layer.

---

### Key Risk

`sandbox-exec` could be removed in any macOS release. Mitigation: abstract the sandbox interface so the backend can be
swapped (Apple Container, bwrap, or Firecracker) without changing agent orchestration logic.

---

## pi-go Sandbox Profile (`pi-profile.sb`)

The `pi-sandbox` wrapper (`cmd/pi-sandbox`) embeds the profile, resolves `HOME`/`CWD` params at runtime, launches `pi`
under `sandbox-exec`, and tails denial logs to stderr + `sandbox.log`.

### Network Rules

| Rule     | Host                | Port    | Purpose                                      |
|----------|---------------------|---------|----------------------------------------------|
| allow    | unix-socket         | —       | DNS resolution (mDNSResponder)               |
| allow    | localhost           | 11434   | Ollama API (local)                           |
| allow    | *                   | 443     | HTTPS — LLM APIs (Anthropic, OpenAI, Gemini) |
| **deny** | **everything else** | **any** | **Blocked** (HTTP:80, SSH:22, etc.)          |

> **Note:** macOS sandbox `remote tcp` only accepts `*` or `localhost` as host — no DNS-based filtering. Port 443 is the
> tightest constraint possible for external HTTPS APIs.

### Filesystem Rules

| Rule       | Path                                    | Purpose                                   |
|------------|-----------------------------------------|-------------------------------------------|
| read       | `/usr`, `/System`, `/opt/homebrew`      | System binaries, frameworks, Go toolchain |
| read       | `/private/etc`, `/private/var/run`      | DNS config, mDNSResponder socket          |
| read       | `/dev/tty`, `/dev/urandom`, `/dev/null` | TUI terminal, crypto entropy              |
| read       | `$HOME/go`                              | Go bin + GOPATH                           |
| read+write | `$HOME/.pi-go`                          | Config, sessions, SQLite memory DB        |
| read+write | `$CWD`                                  | Project working directory                 |
| write      | `/tmp`                                  | Temp files                                |
| **deny**   | **everything else**                     | **Blocked**                               |

### IPC / Process Rules

| Rule     | Operation                      | Purpose                            |
|----------|--------------------------------|------------------------------------|
| allow    | `process-exec`, `process-fork` | Run pi and spawn tool subprocesses |
| allow    | `ipc-posix-shm*`               | SQLite WAL shared memory           |
| allow    | `mach-lookup`, `mach-register` | macOS IPC (DNS, system services)   |
| allow    | `sysctl-read`                  | Go runtime CPU/memory info         |
| **deny** | **everything else**            | **Blocked**                        |

---

## References

- [Alcoholless (NTT Labs)](https://github.com/AkihiroSuda/alcless)
- [SandVault](https://github.com/webcoyote/sandvault)
- [ai-jail sandbox alternatives](https://github.com/akitaonrails/ai-jail/blob/master/docs/sandbox-alternatives.md)
- [Sandboxing Claude Code on macOS (Infralovers)](https://www.infralovers.com/blog/2026-02-15-sandboxing-claude-code-macos/)
- [Apple Container on macOS Tahoe](https://www.ses.box/posts/sandbox-claude-apple-container)
- [Deep Dive on Agent Sandboxes (Pierce Freeman)](https://pierce.dev/notes/a-deep-dive-on-agent-sandboxes)
- [Apple Developer Forums: sandbox-exec replacement](https://developer.apple.com/forums/thread/661939)
- [Run code in a macOS Sandbox (myByways)](https://mybyways.com/blog/run-code-in-a-macos-sandbox)