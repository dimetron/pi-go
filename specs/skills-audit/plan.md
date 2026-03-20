# Implementation Plan - Skills Audit

## Checklist

- [ ] Step 1: Core scanner with character classification
- [ ] Step 2: Report formatters (text, JSON, markdown)
- [ ] Step 3: Strip dangerous characters
- [ ] Step 4: CLI `pi audit` command
- [ ] Step 5: Integration with `extension.LoadSkills()`
- [ ] Step 6: End-to-end testing and documentation

---

## Step 1: Core Scanner with Character Classification

**Objective:** Build the detection engine that scans text for hidden Unicode characters.

**Implementation guidance:**
- Create `internal/audit/scanner.go` with types: `Severity`, `ScanFinding`, `ScanResult`
- Create `internal/audit/chars.go` with the `charTable` lookup map, initialized in `init()`
- Implement `ScanText(content, filename)` with ASCII fast-path using `strings.IndexFunc`
- Implement `ScanFile(path)` that reads UTF-8 and delegates to `ScanText`
- Implement `ScanSkillDirs(dirs...)` that discovers and scans all SKILL.md files
- Implement helper: `HasCritical(findings)`, `Classify(findings)`
- Smart context: ZWJ between emoji → info, BOM at position 0 → info

**Test requirements:**
- `internal/audit/scanner_test.go`:
  - Test each critical character category (tag chars, BiDi overrides, variation selectors)
  - Test each warning character category (zero-width, BiDi marks, invisible math)
  - Test info characters (unusual whitespace, BOM at start)
  - Test ASCII fast-path returns empty findings
  - Test ZWJ emoji context downgrade
  - Test mid-file BOM as warning
  - Test file with mixed categories returns correct severity counts
  - Test `ScanFile` with non-existent file, permission error, non-UTF-8 binary

**Integration notes:** This is a standalone package with no dependencies on other pi-go packages.

**Demo:** Run scanner on test fixtures, print findings to stdout.

---

## Step 2: Report Formatters

**Objective:** Render scan results in terminal, JSON, and markdown formats.

**Implementation guidance:**
- Create `internal/audit/report.go`
- `FormatText(result, verbose)`: table with columns [Severity, File, Line:Col, Codepoint, Description]. Sort by severity (critical first). Filter info unless verbose.
- `FormatJSON(result)`: structured JSON with `summary` and `findings` arrays
- `FormatMarkdown(result)`: GFM table with severity sorting

**Test requirements:**
- `internal/audit/report_test.go`:
  - Test each format produces valid output
  - Test text format filters info without verbose
  - Test text format includes info with verbose
  - Test JSON output unmarshals cleanly
  - Test empty results produce clean-state messages

**Integration notes:** Uses `ScanResult` from Step 1.

**Demo:** Run scanner + formatter on a test fixture, display formatted output.

---

## Step 3: Strip Dangerous Characters

**Objective:** Auto-remove critical and warning characters from skill files.

**Implementation guidance:**
- Add `StripDangerous(content string) string` to `internal/audit/scanner.go`
- Iterate runes, keep only those not in charTable at critical/warning level
- Preserve ZWJ in emoji context (check surrounding runes)
- Preserve info-level characters
- Add `StripFile(path string) error` that reads, strips, writes back
- Create `.bak` backup before modifying

**Test requirements:**
- `internal/audit/strip_test.go`:
  - Test critical chars removed
  - Test warning chars removed
  - Test info chars preserved
  - Test emoji ZWJ sequences preserved (e.g. family emoji)
  - Test backup file created
  - Test idempotent (stripping clean content returns same content)

**Integration notes:** Reuses `charTable` from Step 1.

**Demo:** Create a test file with hidden chars, run strip, show before/after diff.

---

## Step 4: CLI `pi audit` Command

**Objective:** Add `pi audit` as a cobra subcommand.

**Implementation guidance:**
- Create `internal/cli/audit.go`
- Register with `cmd.AddCommand(newAuditCmd())` in `newRootCmd()`
- Flags: `--dir`, `--file`, `--strip`, `--dry-run`, `--force`, `--verbose/-v`, `--format`, `--output`
- Default behavior: scan all skill directories (same list as `LoadSkills`)
- Exit codes: 0 (clean/info), 1 (critical), 2 (warnings)
- `--strip` + `--dry-run`: preview mode showing what would be removed
- `--output`: write to file, auto-detect format from extension

**Test requirements:**
- `internal/cli/audit_test.go`:
  - Test command registration and flag parsing
  - Test exit code 0 for clean files
  - Test exit code 1 for critical findings
  - Test exit code 2 for warning-only findings
  - Test `--format json` produces valid JSON
  - Test `--dry-run` does not modify files

**Integration notes:** Imports `internal/audit` package. Follows existing cobra patterns from `ping.go`.

**Demo:** `pi audit` on the project's own skills, showing clean results or findings.

---

## Step 5: Integration with `extension.LoadSkills()`

**Objective:** Gate skill loading with security scanning.

**Implementation guidance:**
- Add `LoadOptions` struct to `internal/extension/skills.go` with `AuditMode` field
- Modify `LoadSkills` signature: `LoadSkills(opts LoadOptions, dirs ...string) ([]Skill, *audit.ScanResult, error)`
- After parsing each SKILL.md, run `audit.ScanFile()`
- If critical + mode "block": skip skill, add to result
- If critical + mode "warn": load skill, log warning
- If mode "skip": no scanning (backward compat for tests)
- Update call site in `cli.go` (~line 308) to pass options and handle result
- Print stderr warning if any skills were blocked

**Test requirements:**
- Update `internal/extension/skills_test.go`:
  - Test clean skills load normally
  - Test skill with critical chars is blocked in "block" mode
  - Test skill with critical chars loads in "warn" mode
  - Test "skip" mode loads all skills without scanning
  - Test warning-only skills always load
- Create `internal/extension/testdata/` with fixture SKILL.md files containing hidden chars

**Integration notes:** This changes the `LoadSkills` API. All callers must be updated (cli.go, tui.go reload).

**Demo:** Add a hidden char to a test skill, show it being blocked on `pi` startup.

---

## Step 6: End-to-End Testing and Documentation

**Objective:** Validate the full flow and update docs.

**Implementation guidance:**
- Create test fixtures in `internal/audit/testdata/` with various hidden character combinations
- Run `make test` — all existing tests must pass
- Run `make lint` — zero issues
- Verify `pi audit` works on real skill directories
- Update README.md: add `pi audit` to architecture, commands table, and usage

**Test requirements:**
- All tests from Steps 1-5 pass
- Race detection clean (`-race`)
- Coverage for `internal/audit/` >= 85%

**Integration notes:** Final validation gate.

**Demo:** Full walkthrough: `pi audit`, `pi audit --format json`, `pi audit --strip --dry-run`, then normal `pi` startup showing gate in action.
