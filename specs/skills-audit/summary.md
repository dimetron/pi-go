# Skills Audit - Summary

## Artifacts

| File | Purpose |
|------|---------|
| `specs/skills-audit/rough-idea.md` | Original idea |
| `specs/skills-audit/requirements.md` | Q&A record |
| `specs/skills-audit/research/apm-audit-architecture.md` | Microsoft APM audit system analysis |
| `specs/skills-audit/design.md` | Detailed design with architecture, interfaces, acceptance criteria |
| `specs/skills-audit/plan.md` | 6-step implementation plan with TDD |
| `README.md` | Updated with audit feature, command, and usage docs |

## Overview

A security scanning system for pi-go skills, inspired by Microsoft APM's audit architecture. Detects hidden Unicode characters (tag chars, BiDi overrides, zero-width chars, variation selectors) that could embed invisible malicious instructions in SKILL.md files.

**Key components:**
- `internal/audit/` package — scanner, report formatters, strip functionality
- `pi audit` CLI command — standalone scanning with multiple output formats
- Pre-load gate in `extension.LoadSkills()` — blocks dangerous skills automatically

**Scope:** 6 implementation steps, each building on the previous with TDD practices.

## Next Steps

- Execute the implementation plan (Steps 1-6)
- Optionally create a PROMPT.md for autonomous implementation via Ralph
