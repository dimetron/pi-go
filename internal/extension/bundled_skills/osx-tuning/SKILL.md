---
name: osx-tuning
description: Tune macOS resource limits and sysctls for best performance with Go development, Docker/OrbStack, and Linux VMs. Use this skill whenever the user asks to "tune macOS", "raise file descriptor limits", "fix ulimit", "optimize for Docker/Go/VM performance", "increase maxfiles/maxproc/somaxconn", or reports EMFILE/too-many-open-files, connection refused under load, or slow container/VM networking. Covers the full chain: launchd maxfiles, per-shell ulimit, and boot-time sysctl tuning via LaunchDaemons, with verification and revert steps.
---

# macOS Performance Tuning (Go / Docker / Linux VMs)

Raise macOS resource limits and tune TCP sysctls for heavy local development:
Go servers, Docker/OrbStack containers, and Linux VMs. Everything here is
reversible and verified.

## Why the defaults are low

macOS ships conservative soft limits. The two that bite developers:

- **`maxfiles` soft limit = 256** — set by launchd for every process. Go's
  `net/http` servers, Docker, and build tools open far more than 256 file
  descriptors. Hitting it surfaces as `EMFILE: too many open files` or
  `socket: too many open files`.
- **`kern.ipc.somaxconn = 128`** — max pending TCP connections per listen
  socket. Go servers and the Docker API socket queue up behind this under load.

The hard limits are already `unlimited`/high, so nothing is truly capped — the
soft values just need raising.

## The three layers

Tuning must happen at three levels because each only affects a subset of
processes:

| Layer | Affects | Mechanism |
|---|---|---|
| launchd `maxfiles` | all new processes | LaunchDaemon plist |
| per-shell `ulimit` | interactive shells | `~/.zshrc` |
| sysctls | kernel-wide, all processes | LaunchDaemon plist |

## 1. launchd maxfiles (system-wide)

Create `/Library/LaunchDaemons/limit.maxfiles.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>limit.maxfiles</string>
    <key>ProgramArguments</key>
    <array>
      <string>launchctl</string>
      <string>limit</string>
      <string>maxfiles</string>
      <string>65536</string>
      <string>unlimited</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>ServiceIPC</key>
    <false/>
  </dict>
</plist>
```

Install and load:

```bash
sudo cp /tmp/limit.maxfiles.plist /Library/LaunchDaemons/limit.maxfiles.plist
sudo chown root:wheel /Library/LaunchDaemons/limit.maxfiles.plist
sudo chmod 644 /Library/LaunchDaemons/limit.maxfiles.plist
sudo launchctl load -w /Library/LaunchDaemons/limit.maxfiles.plist
```

Verify:

```bash
launchctl limit maxfiles   # expect: maxfiles 65536 unlimited
```

## 2. Per-shell ulimit (interactive shells)

The launchd change only affects **newly launched** processes. Already-open
shells keep their old soft limit, so add a line to `~/.zshrc`:

```zsh
# Raise open file descriptor soft limit (hard limit is unlimited)
ulimit -n 65536
```

Takes effect in new shells, or `source ~/.zshrc` in open ones.

## 3. Boot-time sysctls

macOS does not reliably honor `/etc/sysctl.conf` on modern versions. Use a
LaunchDaemon that runs `sysctl -w` at boot.

Create `/Library/LaunchDaemons/com.local.sysctl.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>com.local.sysctl</string>
    <key>ProgramArguments</key>
    <array>
      <string>/bin/sh</string>
      <string>-c</string>
      <string>sysctl -w kern.maxprocperuid=4000 kern.ipc.somaxconn=1024 net.inet.tcp.sendspace=262144 net.inet.tcp.recvspace=262144 net.inet.tcp.win_scale_factor=5 net.inet.tcp.mssdflt=1460</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>ServiceIPC</key>
    <false/>
  </dict>
</plist>
```

Install and load (same pattern as above, target `com.local.sysctl.plist`).

### What each sysctl does

| sysctl | Default | Tuned | Why |
|---|---|---|---|
| `kern.maxprocperuid` | 2666 | 4000 | Max processes per user. Go goroutines, Docker/VM threads, build workers all count. 2666 is low for heavy dev. |
| `kern.ipc.somaxconn` | 128 | 1024 | Max pending TCP connections per listen socket. Go `net/http` and the Docker API socket hit this under load. |
| `net.inet.tcp.sendspace` | 131072 | 262144 | Larger TCP send buffer — better throughput for Go servers and VM/container networking. |
| `net.inet.tcp.recvspace` | 131072 | 262144 | Larger receive buffer, same reason. |
| `net.inet.tcp.win_scale_factor` | 3 | 5 | Enables larger TCP window scaling — matters for high-bandwidth transfers (Docker pulls, VM traffic). |
| `net.inet.tcp.mssdflt` | 512 | 1460 | Default MSS. 512 is the conservative fallback; 1460 matches Ethernet MTU 1500, reducing packet overhead. |

## Verification

```bash
launchctl limit maxfiles          # maxfiles 65536 unlimited
ulimit -n                          # 65536 in a new shell
sysctl kern.maxprocperuid kern.ipc.somaxconn \
       net.inet.tcp.sendspace net.inet.tcp.recvspace \
       net.inet.tcp.win_scale_factor net.inet.tcp.mssdflt
sudo launchctl list | grep -iE "sysctl|maxfiles"   # both loaded, exit 0
```

## Reverting

```bash
sudo launchctl unload -w /Library/LaunchDaemons/limit.maxfiles.plist
sudo rm /Library/LaunchDaemons/limit.maxfiles.plist
sudo launchctl unload -w /Library/LaunchDaemons/com.local.sysctl.plist
sudo rm /Library/LaunchDaemons/com.local.sysctl.plist
# remove the ulimit line from ~/.zshrc
```

## Notes / caveats

- **`kern.maxproc` (4000) and `kern.maxfiles` (2147483647)** are already at
  their caps — no change needed.
- **`net.inet.ip.forwarding`** should stay 0 unless you're building a router.
- **`vm.overcommit`** is not a macOS knob; memory is managed by the VM/compressor.
  High swap usage is normal compressed-memory behavior, not a leak.
- **Docker/OrbStack VM CPU/RAM** is configured in OrbStack's settings GUI, not
  via sysctl — that's where you bump resources for the Linux VM itself.
- **`win_scale_factor` and `mssdflt`** are the most opinionated changes. Safe on
  a modern network, but revert those first if you ever see odd TCP behavior.
- **`sudo` is required** for the LaunchDaemon installs and `sysctl -w`. The
  sandbox blocks localhost listeners and some privileged ops — run these outside
  a sandbox if a step fails with a permission error.
