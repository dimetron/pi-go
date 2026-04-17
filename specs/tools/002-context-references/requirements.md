# Requirements

## Questions & Answers

### Q1: What does "context-references" mean?

Use same idea as https://hermes-agent.nousresearch.com/docs/user-guide/features/context-references

A feature that lets users inject content into their messages using `@` syntax references that get expanded before
sending to the agent.

### Q2: Implementation approach

Should be available inside user input in TUI mode.

### Q3: How should expanded content be displayed?

Visible inline - appended in the message shown to user.

### Q4: What should happen if expansion is too large?

Truncate with indicator - show first N lines + "... truncated"

### Q5: Autocomplete/path suggestions?

Yes - show file/folder path completion.

### Q6: Web fetch (@url:) support?

Yes - support `@url:https://...`

### Q7: Security checks included?

Yes - full security: sensitive path blocking, path traversal protection, binary file detection.

### Q8: Git-related references?

All three - `@diff`, `@staged`, `@git:N` all supported.

## Summary

**Context References** is a TUI feature that allows users to inject file, folder, git, and web content into messages
using `@` reference syntax.

### Supported References

| Syntax                        | Description                                       |
|-------------------------------|---------------------------------------------------|
| `@file:path/to/file.py`       | Inject file contents                              |
| `@file:path/to/file.py:10-25` | Inject specific line range (1-indexed, inclusive) |
| `@folder:path/to/dir`         | Inject directory tree listing with file metadata  |
| `@diff`                       | Inject `git diff` (unstaged working tree changes) |
| `@staged`                     | Inject `git diff --staged` (staged changes)       |
| `@git:N`                      | Inject last N commits with patches (max 10)       |
| `@url:https://example.com`    | Fetch and inject web page content                 |

### Key Requirements

1. **Visible expansion** - Expanded content shown inline in message
2. **Truncation** - Large content truncated with indicator
3. **Path autocomplete** - File/folder path completion in TUI
4. **Security** - Sensitive path blocking, path traversal protection, binary file detection
5. **All git refs** - `@diff`, `@staged`, `@git:N`

### Security Requirements

- **Sensitive Path Blocking**: Block SSH keys, shell profiles, credential files, Hermes .env
- **Directory Blocking**: Full block on `~/.ssh/`, `~/.aws/`, `~/.gnupg/`, `~/.kube/`, `$HERMES_HOME/skills/.hub/`
- **Path Traversal**: Reject paths outside workspace root
- **Binary Detection**: Reject binary files with MIME type or null-byte scanning
