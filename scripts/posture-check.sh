#!/usr/bin/env bash
# posture-check.sh — Keep the engineering-posture claims in README.md and
# docs/llms.txt true.
#
# README.md and docs/llms.txt state the project's test posture in prose,
# because badges are visually skippable and automated readers treat them as
# decoration. Prose about numbers goes stale silently, and a precise-but-wrong
# figure is worse than none: a reader who recomputes it, finds drift, and
# concludes the documentation is marketing has been given a reason to distrust
# every other claim on the page.
#
# So the claims are deliberately directional ("more than 1.25:1", "above 2:1")
# rather than exact, and this gate asserts the direction still holds. It fails
# when reality crosses the stated line, which is the moment the prose needs
# rewriting — not on every commit that moves a number.
#
# Checks:
#   1. Test-to-production Go line ratio is above the claimed floor.
#   2. The two packages named as carrying the highest ratios still clear 2:1.
#   3. Fuzz suites live in exactly the packages the prose names.
#   4. Every grounding page states the claim, in the claimed DIRECTION, naming
#      the same packages. Checks 1-3 validate this script's constants against
#      the tree; this one validates the prose against those constants, so a
#      page cannot drift from the others or assert the opposite and pass.
#   5. No page claims a SLSA level. release.yml ships
#      actions/attest-build-provenance instead of the slsa-github-generator
#      job; see the NOTE in that workflow.
#
# Compatible with bash 3.2+ (macOS) and GNU bash (CI).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# The claimed floors. These are the numbers the prose commits to; raising a
# claim in the prose means raising it here in the same change.
RATIO_FLOOR="1.25"
PKG_FLOOR="2.0"
HIGH_RATIO_PKGS="pkg/oauth pkg/middleware"
FUZZ_PKGS="pkg/auth pkg/middleware pkg/oauth pkg/platform"
# Every page that carries the posture block. All three are grounding sources an
# agent may read instead of the repository, so none may drift from the others.
CLAIM_PAGES="README.md docs/llms.txt docs/llms-full.txt"
# The exact phrase the prose must use, so the check pins the direction of the
# claim and not merely the presence of the digits.
RATIO_PHRASE="more than ${RATIO_FLOOR} lines of test code"

fail=0

# go_lines <dir> <test|prod> — lines of Go in the main module.
#
# Excludes ui/ (TypeScript tree, no Go) and the two harnesses that are their
# own Go modules, bench/ and test/load/. A reader who clones this repository
# and measures "the project" measures the module that builds the server, so
# counting the harnesses here would inflate both sides of a ratio the prose
# attributes to the platform.
go_lines() {
    local dir="$1" kind="$2" files
    if [ "$kind" = "test" ]; then
        files=$(find "$dir" -name '*_test.go' \
            ! -path './ui/*' ! -path './bench/*' ! -path './test/load/*' 2>/dev/null)
    else
        files=$(find "$dir" -name '*.go' ! -name '*_test.go' \
            ! -path './ui/*' ! -path './bench/*' ! -path './test/load/*' 2>/dev/null)
    fi
    [ -z "$files" ] && { echo 0; return 0; }
    printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 cat | wc -l | tr -d ' '
}

# ── Check 1: overall test-to-production ratio ───────────────────────────────
prod=$(go_lines . prod)
test=$(go_lines . test)
if [ "$prod" -eq 0 ]; then
    echo "FAIL: found no production Go files; the ratio check cannot run."
    exit 1
fi
ratio=$(awk -v t="$test" -v p="$prod" 'BEGIN {printf "%.3f", t/p}')
if awk -v r="$ratio" -v f="$RATIO_FLOOR" 'BEGIN {exit !(r > f)}'; then
    echo "OK: test-to-production ratio ${ratio}:1 is above the claimed ${RATIO_FLOOR}:1 (${test} test / ${prod} production lines)."
else
    echo "FAIL: test-to-production ratio ${ratio}:1 has fallen to or below the claimed ${RATIO_FLOOR}:1."
    echo "  ${test} test lines / ${prod} production lines."
    echo "  Either add tests, or lower the claim in: ${CLAIM_PAGES} and RATIO_FLOOR in this script."
    fail=1
fi

# ── Check 2: the packages named as highest-ratio still clear 2:1 ────────────
for p in $HIGH_RATIO_PKGS; do
    if [ ! -d "$p" ]; then
        echo "FAIL: ${p} is named in the posture prose but does not exist."
        fail=1
        continue
    fi
    pp=$(go_lines "$p" prod)
    pt=$(go_lines "$p" test)
    if [ "$pp" -eq 0 ]; then
        echo "FAIL: ${p} has no production Go files."
        fail=1
        continue
    fi
    pr=$(awk -v t="$pt" -v p="$pp" 'BEGIN {printf "%.2f", t/p}')
    if awk -v r="$pr" -v f="$PKG_FLOOR" 'BEGIN {exit !(r >= f)}'; then
        echo "OK: ${p} at ${pr}:1 clears the claimed ${PKG_FLOOR}:1."
    else
        echo "FAIL: ${p} at ${pr}:1 no longer clears the claimed ${PKG_FLOOR}:1."
        echo "  Either add tests, or stop naming it in the posture prose in: ${CLAIM_PAGES}."
        fail=1
    fi
done

# ── Check 3: fuzz suites live exactly where the prose says ─────────────────
# The `|| true` matters: grep exits 1 on no match, and under `set -e` with
# `pipefail` that would abort the script before the diagnostic below is
# printed, leaving an operator who deleted every fuzz suite with a bare
# non-zero exit and no explanation.
actual_fuzz=$( { grep -rl '^func Fuzz' --include='*_test.go' pkg internal cmd 2>/dev/null || true; } \
    | sed 's|/[^/]*$||' | sort -u | tr '\n' ' ' | sed 's/ $//')
expected_fuzz=$(printf '%s\n' $FUZZ_PKGS | sort -u | tr '\n' ' ' | sed 's/ $//')
if [ "$actual_fuzz" = "$expected_fuzz" ]; then
    echo "OK: fuzz suites are in exactly the packages the prose names (${expected_fuzz})."
else
    echo "FAIL: the fuzz-suite claim is stale."
    echo "  prose names: ${expected_fuzz}"
    echo "  tree has:    ${actual_fuzz}"
    echo "  Update the sentence in ${CLAIM_PAGES} and FUZZ_PKGS in this script."
    fail=1
fi

# ── Check 4: every grounding page states the claim, in the claimed direction ─
# An agent grounding on the docs site must get the same facts as one reading
# the README. Matching the bare number would accept a page asserting the
# opposite ("fewer than 1.25..."), so this matches the directional phrase and
# requires every package the prose names to actually appear on the page.
for page in $CLAIM_PAGES; do
    if [ ! -f "$page" ]; then
        echo "FAIL: ${page} not found."
        fail=1
        continue
    fi
    page_ok=1
    if ! grep -qi "${RATIO_PHRASE}" "$page"; then
        echo "FAIL: ${page} does not state the ratio claim as \"${RATIO_PHRASE}\"."
        page_ok=0
    fi
    # The two lists overlap; dedupe so an omission is reported once.
    for p in $(printf '%s\n' $HIGH_RATIO_PKGS $FUZZ_PKGS | sort -u); do
        if ! grep -q "$p" "$page"; then
            echo "FAIL: ${page} omits ${p}, which the posture claim names."
            page_ok=0
        fi
    done
    if ! grep -qi 'fuzz' "$page"; then
        echo "FAIL: ${page} is missing the fuzz-coverage sentence."
        page_ok=0
    fi
    if [ "$page_ok" -eq 1 ]; then
        echo "OK: ${page} states the posture claim in the claimed direction."
    else
        echo "  Every one of these pages states it, so an agent grounding on any of them gets identical facts: ${CLAIM_PAGES}"
        fail=1
    fi
done

# ── Check 5: no SLSA level is claimed ──────────────────────────────────────
# release.yml deliberately does not run the slsa-github-generator job.
slsa_hits=$(grep -ilE 'slsa' $CLAIM_PAGES 2>/dev/null || true)
if [ -z "$slsa_hits" ]; then
    echo "OK: no SLSA level claimed."
else
    echo "FAIL: SLSA is referenced in: ${slsa_hits}"
    echo "  release.yml ships actions/attest-build-provenance, not slsa-github-generator."
    echo "  Say 'build-provenance attestations', never a SLSA level."
    fail=1
fi

if [ "$fail" -ne 0 ]; then
    exit 1
fi
