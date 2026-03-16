---
name: pi-rebuild
description: Rebuild, reinstall and restart the pi binary from source. Use after making changes to pi-go code.
---

# Pi Rebuild

Rebuild and reinstall the pi binary from source, then restart the process.

## Steps

1. Run from the project root:

       go build ./cmd/pi && go install ./cmd/pi/

2. If build succeeds, call the `restart` tool to relaunch pi with the updated binary.

3. If build fails, show the errors so the user can fix them. Do not restart on failure.

## Examples

- `/pi-rebuild` — Rebuild and restart pi after code changes

## Guidelines

- Always build before installing to catch compile errors early
- Only call `restart` after a successful build
- Show full build output on failure so the user can diagnose issues
