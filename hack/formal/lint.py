#!/usr/bin/env python3
"""Lint the TLA+ models under hack/formal for the mistakes that make a check pass
for the wrong reason.

Every check here exists because that mistake produced a false green while these
models were being written:

  unassigned-variable   A variable no action assigns. TLC reports it as
                        "Successor state is not completely specified", but only
                        for the paths it explores, and a rule over such a
                        variable is vacuously true -- an entire gate rule was
                        silently vacuous this way.
  duplicate-assignment  Two assignments to the same variable in one action. TLA+
                        conjoins them, so a contradicting branch can never fire.
                        This hid the bug the /goal model exists to find.
  dead-action           An action whose guards are unsatisfiable, because it
                        assigns a variable twice or constrains it two ways.
  vacuous-rule          An invariant that no reachable state can violate because
                        its premise is never true. Detected only by mutation; see
                        the models' skills.

Exit 1 on a finding. Run from the repo root, or pass model directories.
"""

from __future__ import annotations

import collections
import pathlib
import re
import sys

VARS_RE = re.compile(r"^VARIABLES\s*$(.*?)(?=^\S)", re.S | re.M)
# A parameterised action (ReadVerdict(outcome) ==) is a boundary too; missing
# it folds one action's assignments into the previous one and invents
# duplicate-assignment findings.
ACTION_RE = re.compile(r"^([A-Z]\w*)\s*(?:\([^)]*\))?\s*==", re.M)
ASSIGN_RE = re.compile(r"\b(\w+)'\s*=")
UNCHANGED_RE = re.compile(r"UNCHANGED\s+<<([^>]*)>>")
UNCHANGED_NAME_RE = re.compile(r"UNCHANGED\s+([A-Z]\w*)\s*$", re.M)
SET_RE = re.compile(r"^(\w+)\s*==\s*<<([^>]*)>>", re.M | re.S)
RESERVED = {"Init", "Next", "FairSpec", "Spec"}


def strip_comments(text: str) -> str:
    """Drop TLA+ comments so a variable named in prose is not read as code."""
    out = []
    for line in text.splitlines():
        in_string = False
        cut = len(line)
        for i, ch in enumerate(line):
            if ch == '"':
                in_string = not in_string
            elif not in_string and ch == "\\" and line[i : i + 2] == "\\*":
                cut = i
                break
        out.append(line[:cut])
    return "\n".join(out)


def declared_variables(code: str) -> list[str]:
    # VARIABLES a, b,   /  VARIABLES\n  a,  \* comment\n  b
    m = re.search(r"^VARIABLES\b(.*?)(?=^\S|\Z)", code, re.S | re.M)
    if not m:
        return []
    block = re.sub(r"\\\*.*", "", m.group(1))
    return [v for v in re.findall(r"[A-Za-z_]\w*", block)]


def actions(code: str) -> dict[str, str]:
    found: dict[str, str] = {}
    marks = [(m.group(1), m.start()) for m in ACTION_RE.finditer(code)]
    for i, (name, start) in enumerate(marks):
        end = marks[i + 1][1] if i + 1 < len(marks) else len(code)
        found[name] = code[start:end]
    return found


def named_sets(code: str) -> dict[str, set[str]]:
    """Resolve `V == <<a, b>>` so `UNCHANGED V` counts the same as the tuple.

    Scanned with balanced brackets rather than a regex: a multi-line tuple with a
    trailing definition on the next line makes a greedy pattern swallow it, and a
    one-line pattern misses the tuple entirely.
    """
    out: dict[str, set[str]] = {}
    for m in re.finditer(r"^(\w+)\s*==\s*<<", code, re.M):
        i = m.end() - 2
        depth = 0
        while i < len(code) - 1:
            if code[i : i + 2] == "<<":
                depth += 1
                i += 2
                continue
            if code[i : i + 2] == ">>":
                depth -= 1
                i += 2
                if depth == 0:
                    break
                continue
            i += 1
        body = code[m.end() - 2 : i]
        out[m.group(1)] = set(re.findall(r"[A-Za-z_]\w*", body))
    return out


def branches(body: str) -> list[str]:
    """Split an action on TLA+ IF/ELSE so assignments in different branches are
    not read as a contradiction. A duplicate within one branch is still a bug."""
    lines = body.splitlines()
    out: list[list[str]] = [[]]
    for line in lines:
        stripped = line.strip()
        # THEN/ELSE start an IF branch; a leading \/ starts a disjunct, which is
        # the other way TLA+ writes alternatives (ReadVerdict uses three).
        if stripped.startswith("THEN") or stripped.startswith("ELSE") or stripped.startswith("\\/"):
            out.append([])
        out[-1].append(line)
    return ["\n".join(b) for b in out if b]


def lint(path: pathlib.Path) -> list[str]:
    raw = path.read_text()
    code = strip_comments(raw)
    sets = named_sets(code)
    problems: list[str] = []
    declared = declared_variables(code)
    acts = actions(code)
    if not declared or not acts:
        return [f"{path}: could not find VARIABLES or actions"]

    assigned_anywhere: set[str] = set()
    for name, body in acts.items():
        if name in RESERVED or name in sets:
            # A named set (V == <<a, b>>) is not an action.
            continue
        # An UNCHANGED tuple lists the variables it preserves; those are not
        # assignments, so they are counted separately and never as duplicates.
        preserved = set()
        for group in UNCHANGED_RE.findall(body):
            preserved.update(re.findall(r"[A-Za-z_]\w*", group))
        for setName in UNCHANGED_NAME_RE.findall(body):
            preserved.update(sets.get(setName, set()))
        primed = collections.Counter(ASSIGN_RE.findall(body))
        for var, n in primed.items():
            assigned_anywhere.add(var)
            # An UNCHANGED tuple does not excuse an assignment. `x' = v /\ UNCHANGED
            # <<x>>` is a conjunction that pins x twice, so either the value must be
            # v or the action is unsatisfiable. This is how a bulk edit made every
            # action in the session model dead while the lint stayed silent.
            if var in preserved and "THEN" not in body and "ELSE" not in body:
                problems.append(
                    f"{path}:{name}: assigned-and-unchanged: {var} is both assigned "
                    f"and listed in UNCHANGED (TLA+ conjoins them; the action is dead "
                    f"or the value is forced)"
                )
            # A duplicate inside one branch is a real contradiction; duplicates
            # spread across IF/ELSE branches are just alternatives.
            worst = max(
                (collections.Counter(ASSIGN_RE.findall(b)).get(var, 0) for b in branches(body)),
                default=0,
            )
            if worst > 1:
                problems.append(
                    f"{path}:{name}: duplicate-assignment: {var} assigned {worst} times "
                    f"in one branch (TLA+ conjoins them; a contradicting assignment "
                    f"cannot fire)"
                )
        assigned_anywhere.update(preserved)

    for var in declared:
        if var not in assigned_anywhere:
            problems.append(
                f"{path}: unassigned-variable: {var} is never assigned by any action "
                f"(TLC stops with 'not completely specified', and a rule over it is vacuous)"
            )

    # An invariant whose premise is a variable no action can make true is not a
    # proof of anything. This catches the easy case: a premise on a literal that
    # no action assigns.
    for m in re.finditer(r"^(\w+)\s*==\s*\n?\s*(\w+)\s*=\s*\"([^\"]*)\"\s*=>", code, re.M):
        rule, var, value = m.groups()
        if var not in declared:
            continue
        assigns = re.findall(rf"\b{var}'\s*=\s*(\"[^\"]*\"|\w+)", code)
        literal_only = all(a.startswith('"') for a in assigns) and assigns
        if literal_only and f'"{value}"' not in assigns:
            problems.append(
                f"{path}:{rule}: vacuous-rule: premise needs {var} = \"{value}\", "
                f"which no action assigns"
            )
    return problems


def main(argv: list[str]) -> int:
    roots = [pathlib.Path(a) for a in argv[1:]] or [pathlib.Path("hack/formal")]
    specs: list[pathlib.Path] = []
    for root in roots:
        specs.extend(sorted(root.rglob("*.tla")))
    if not specs:
        print("formal-lint: no .tla files found", file=sys.stderr)
        return 2
    problems: list[str] = []
    for spec in specs:
        problems.extend(lint(spec))
    for p in problems:
        print(p)
    if problems:
        print(f"\nformal-lint: {len(problems)} finding(s) in {len(specs)} spec(s)")
        return 1
    print(f"formal-lint: {len(specs)} spec(s) clean")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
