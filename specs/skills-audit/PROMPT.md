# Skills Audit - Implementation Prompt

## Objective

Implement a security scanning system that detects hidden Unicode characters in SKILL.md files before they are loaded into the pi-go agent. Add `pi audit` CLI command and integrate scanning into the skill loading pipeline.

## Key Requirements

1. Create `internal/audit/` package with character-by-character Unicode scanner
2. Classify findings as critical/warning/info using pre-built lookup table
3. ASCII fast-path optimization (skip scanning for pure-ASCII files)
4. Smart context: ZWJ between emoji is info, BOM at file start is info
5. Report formatters: text (terminal table), JSON, markdown
6. `StripDangerous()` to remove critical+warning chars, preserve emoji sequences
7. `pi audit` cobra subcommand with flags: `--strip`, `--dry-run`, `--verbose`, `--format`, `--output`, `--file`, `--dir`
8. Exit codes: 0=clean, 1=critical, 2=warnings
9. Integrate into `extension.LoadSkills()` as pre-load gate — block skills with critical findings
10. TDD: test fixtures with embedded Unicode chars, >=85% coverage on `internal/audit/`

## Acceptance Criteria

- Given a SKILL.md with Unicode tag chars (U+E0001-E007F), When scanned, Then critical finding reported and skill blocked from loading
- Given a SKILL.md with BiDi overrides (U+202A-202E, U+2066-2069), When scanned, Then critical finding reported
- Given a SKILL.md with variation selectors SMP (U+E0100-E01EF), When scanned, Then critical finding reported (Glassworm vector)
- Given a SKILL.md with zero-width chars (U+200B-200D), When scanned, Then warning finding reported, skill still loads
- Given a pure ASCII SKILL.md, When scanned, Then empty findings via fast-path
- Given ZWJ (U+200D) between emoji characters, When scanned, Then classified as info (not warning)
- Given `pi audit --strip`, When run, Then dangerous chars removed, `.bak` backup created
- Given `pi audit --format json`, When run, Then valid JSON with summary and findings arrays
- Given `pi audit --dry-run --strip`, When run, Then preview shown, no files modified
- Given `extension.LoadSkills()` with default audit mode, When skill has critical findings, Then skill is NOT loaded and stderr warning printed

## Reference

- Design: `specs/skills-audit/design.md`
- Research: `specs/skills-audit/research/apm-audit-architecture.md`
- Plan: `specs/skills-audit/plan.md`
- Existing skills code: `internal/extension/skills.go`
- CLI patterns: `internal/cli/cli.go`, `internal/cli/ping.go`
