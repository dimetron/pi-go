#!/usr/bin/env bash
# Run one TLA+ model under hack/formal and report whether its rules hold.
#
#   hack/formal/check.sh goal        # one model
#   hack/formal/check.sh all         # every model
#   hack/formal/check.sh goal --chart # also draw the statechart
#
# TLC and a JDK are found, not installed. Set TLA2TOOLS to a tla2tools.jar and
# JAVA to a java binary to override the search. The jar version is pinned to the
# one the /statechart skill pins, and its sha256 is verified, so a run cannot
# silently use a different checker.
set -uo pipefail

TLC_VERSION="1.7.4"
TLC_SHA256="936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"
TLC_URL="https://github.com/tlaplus/tlaplus/releases/download/v${TLC_VERSION}/tla2tools.jar"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cache="${XDG_CACHE_HOME:-$HOME/.cache}/pi-go-formal"

die() {
    printf 'check: %s\n' "$*" >&2
    exit 2
}

# macOS ships /usr/bin/java as a stub that exists but exits non-zero, so a
# candidate counts only if it actually runs.
works() {
    [ -x "$1" ] || return 1
    "$1" -version >/dev/null 2>&1
}

find_java() {
    if [ -n "${JAVA:-}" ]; then
        works "$JAVA" && { printf '%s' "$JAVA"; return 0; }
        return 1
    fi
    local candidate
    if candidate="$(command -v java 2>/dev/null)" && works "$candidate"; then
        printf '%s' "$candidate"
        return 0
    fi
    # Homebrew's openjdk is keg-only and deliberately absent from PATH.
    if command -v brew >/dev/null 2>&1; then
        if candidate="$(brew --prefix openjdk 2>/dev/null)"; then
            candidate="$candidate/bin/java"
            works "$candidate" && { printf '%s' "$candidate"; return 0; }
        fi
    fi
    for candidate in /Library/Java/JavaVirtualMachines/*/Contents/Home/bin/java; do
        works "$candidate" && { printf '%s' "$candidate"; return 0; }
    done
    return 1
}

find_jar() {
    if [ -n "${TLA2TOOLS:-}" ]; then
        if [ ! -f "$TLA2TOOLS" ]; then
            die "TLA2TOOLS=$TLA2TOOLS does not exist"
        fi
        printf '%s' "$TLA2TOOLS"
        return 0
    fi
    local jar="$cache/tla2tools-$TLC_VERSION.jar"
    if [ ! -f "$jar" ]; then
        mkdir -p "$cache" || die "cannot create $cache"
        printf 'check: downloading tla2tools %s\n' "$TLC_VERSION" >&2
        curl -fsSL -o "$jar.tmp" "$TLC_URL" || die "cannot download $TLC_URL"
        mv "$jar.tmp" "$jar"
    fi
    local got
    got="$(shasum -a 256 "$jar" | cut -d' ' -f1)"
    if [ "$got" != "$TLC_SHA256" ]; then
        die "checksum mismatch for $jar
  want $TLC_SHA256
  got  $got
Delete it and rerun, or point TLA2TOOLS at a jar you trust."
    fi
    printf '%s' "$jar"
}

# run_model <dir> [--chart]
run_model() {
    local dir="$1" chart="${2:-}"
    local name
    name="$(basename "$dir")"
    local spec cfg
    spec="$(ls "$dir"/*.tla 2>/dev/null | head -1)"
    cfg="${spec%.tla}.cfg"
    [ -n "$spec" ] || die "no .tla in $dir"
    [ -f "$cfg" ] || die "no config beside $spec"

    printf '\n== %s ==\n' "$name"
    local out rc
    out="$(cd "$dir" && "$JAVA_BIN" -XX:+UseParallelGC -cp "$JAR" tlc2.TLC \
        -workers 1 -fp 0 -lncheck final -config "$cfg" "$spec" 2>&1)"
    rc=$?

    # TLC's exit codes: 0 clean, 11/12/13 a rule broke, anything else an error.
    case "$rc" in
        0) : ;;
        11|12|13)
            printf '%s\n' "$out" | grep -E '^Error: Invariant|^Error: The following|^Error: Temporal' || true
            printf 'RESULT %s: A RULE BROKE (tlc exit %s)\n' "$name" "$rc"
            return 1
            ;;
        *)
            printf '%s\n' "$out" | tail -25
            printf 'RESULT %s: TLC FAILED (exit %s)\n' "$name" "$rc"
            return 2
            ;;
    esac

    local states
    # The summary line is "N states generated, M distinct states found, ...";
    # the initial-state line also says "distinct state", so match the summary.
    states="$(printf '%s\n' "$out" |
        sed -n 's/^[0-9][0-9,]* states generated, \([0-9][0-9,]*\) distinct states found.*/\1/p' |
        tail -1)"
    printf 'RESULT %s: all rules hold (%s distinct states)\n' "$name" "${states:-?}"

    if [ "$chart" = "--chart" ]; then
        draw_chart "$dir" "$spec" || return $?
    fi
    return 0
}

# draw_chart shells out to the /statechart skill, whose path is not fixed across
# releases or machines, so it is located rather than hard-coded.
draw_chart() {
    local dir="$1" spec="$2"
    local skill=""
    local root
    for root in "${PSTACK_SKILLS:-}" "$HOME/.pi-go/plugins/pstack/skills" "$HOME/.claude/plugins/cache"; do
        [ -n "$root" ] || continue
        if [ -f "$root/statechart/scripts/statechart.ts" ]; then
            skill="$root/statechart/scripts/statechart.ts"
            break
        fi
        skill="$(find "$root" -maxdepth 4 -path '*statechart/scripts/statechart.ts' 2>/dev/null | head -1)"
        [ -n "$skill" ] && break
    done
    if [ -z "$skill" ]; then
        printf 'CHART %s: /statechart not found; set PSTACK_SKILLS to the skills dir\n' "$(basename "$dir")" >&2
        return 0
    fi
    local view=""
    [ -f "$dir/$(basename "$spec" .tla | tr '[:upper:]' '[:lower:]').view.ts" ] &&
        view="$dir/$(basename "$spec" .tla | tr '[:upper:]' '[:lower:]').view.ts"
    [ -n "$view" ] || view="$(ls "$dir"/*.view.ts 2>/dev/null | head -1)"

    local out="$dir/out"
    rm -rf "$out"
    local args=("$spec" --out "$out")
    [ -n "$view" ] && args+=(--view "$view")
    TLA2TOOLS="$JAR" JAVA="$JAVA_BIN" bun run "$skill" "${args[@]}"
}

[ $# -ge 1 ] || die "usage: check.sh <model|all> [--chart]

models: $(cd "$here" && ls -d */ 2>/dev/null | tr -d / | tr '\n' ' ')"

JAVA_BIN="$(find_java)" || die "no java found. Install one (brew install openjdk) or set JAVA."
JAR="$(find_jar)" || exit $?

target="$1"
want_chart=""
for a in "$@"; do [ "$a" = "--chart" ] && want_chart="--chart"; done

if [ "$target" = "all" ]; then
    failed=0
    for dir in "$here"/*/; do
        [ -f "$(ls "$dir"*.tla 2>/dev/null | head -1)" ] || continue
        run_model "${dir%/}" "$want_chart" || failed=1
    done
    exit $failed
fi

dir="$here/$target"
[ -d "$dir" ] || die "no model named '$target'"
run_model "$dir" "$want_chart"
