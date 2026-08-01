#!/usr/bin/env bash
# doc-check.sh — Documentation gates.
#
#   Hard gates (fail the build, run every time regardless of the diff):
#     1. No orphaned docs — every docs/**/*.md must be reachable from the
#        mkdocs.yml nav or matched by a not_in_nav / exclude_docs pattern. This
#        delegates to the authoritative Go gate (TestDocsPagesInNavOrExcluded),
#        which models MkDocs' gitignore-style exclusion semantics exactly, so
#        this check can never drift from what MkDocs actually excludes.
#     2. No retired tool references — a decommissioned/renamed tool name from
#        scripts/retired-tools.txt (e.g. `memory_recall`) must not appear in any
#        docs/**/*.md, bench doc, or README.md. This is what would have caught
#        the reference the deleted bench/LOCOMO.md carried.
#     3. Benchmark reference pages cite only registered tools — any
#        trino_/datahub_/s3_/api_/memory_ token in docs/reference/benchmarks.md
#        or benchmark-report.md must be a registered tool
#        (scripts/registered-tools.txt) or an acknowledged non-tool identifier
#        (scripts/doc-check-nontools.txt).
#
#   Soft gate (warning only): documentation-worthy code changes lacking doc
#   updates. Never fails on its own.
#
# Compatible with bash 3.2+ (macOS) and GNU bash (CI).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BASE_BRANCH="${BASE_BRANCH:-main}"
hard_fail=0

# ── Hard gate 1: orphaned documentation pages ───────────────────────────────
# Delegate to the authoritative Go gate rather than reimplementing MkDocs'
# gitignore-style exclusion matching in bash (which would inevitably drift and
# false-fail on valid not_in_nav/exclude_docs forms).
check_orphaned_docs() {
    if ! command -v go > /dev/null 2>&1; then
        echo "SKIP orphan check: go toolchain not available."
        return 0
    fi
    local out
    if out=$(go test -run '^TestDocsPagesInNavOrExcluded$' -count=1 . 2>&1); then
        echo "OK: no orphaned documentation pages (TestDocsPagesInNavOrExcluded)."
    else
        printf '%s\n' "$out"
        echo "FAIL: orphaned documentation page(s). Add each to the mkdocs.yml nav or not_in_nav."
        hard_fail=1
    fi
}

# ── Hard gate 2: retired tool references anywhere in the docs ────────────────
# Zero-false-positive denylist: matches only exact retired tool names, so it
# never trips on config keys or metrics that share a tool prefix.
check_retired_tools() {
    local retired="scripts/retired-tools.txt"
    if [ ! -f "$retired" ]; then
        echo "SKIP retired-tool check: $retired not found."
        return 0
    fi
    local names alt scan_files violations=0
    names=$(grep -vE '^[[:space:]]*(#|$)' "$retired" || true)
    if [ -z "$names" ]; then
        echo "OK: no retired tool names configured."
        return 0
    fi
    alt=$(printf '%s\n' "$names" | tr '\n' '|' | sed 's/|$//')
    # Operator/reference docs only; bench/results/ is generated run output.
    scan_files=$( { find docs bench -name '*.md' -not -path 'bench/results/*'; echo README.md; } 2>/dev/null)
    while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        echo "  RETIRED TOOL: $hit"
        violations=$((violations + 1))
    done < <(grep -HnoE "\\b(${alt})\\b" $scan_files 2>/dev/null | sort -u || true)

    if [ "$violations" -gt 0 ]; then
        echo "FAIL: $violations reference(s) to a retired tool name (see $retired)."
        echo "  Remove the reference, or rewrite it against the current tool set."
        hard_fail=1
    else
        echo "OK: no retired tool names referenced in the docs."
    fi
}

# ── Hard gate 3: benchmark reference pages cite only registered tools ────────
check_benchmark_tool_refs() {
    local reg="scripts/registered-tools.txt"
    local nontools="scripts/doc-check-nontools.txt"
    if [ ! -f "$reg" ]; then
        echo "SKIP benchmark-tool check: $reg not found."
        return 0
    fi
    local files="docs/reference/benchmarks.md docs/reference/benchmark-report.md docs/reference/benchmark-report-knowledge-use.md"
    local allow tok f violations=0
    allow=$(grep -vE '^[[:space:]]*(#|$)' "$reg")
    if [ -f "$nontools" ]; then
        allow="$allow"$'\n'"$(grep -vE '^[[:space:]]*(#|$)' "$nontools")"
    fi
    for f in $files; do
        [ -f "$f" ] || continue
        while IFS= read -r tok; do
            [ -z "$tok" ] && continue
            grep -qxF "$tok" <<<"$allow" && continue
            echo "  UNREGISTERED TOOL: '$tok' referenced in $f"
            violations=$((violations + 1))
        done < <(grep -hoE '\b(trino|datahub|s3|api|memory)_[a-z][a-z0-9_]*' "$f" | sort -u)
    done

    if [ "$violations" -gt 0 ]; then
        echo "FAIL: $violations reference(s) to a tool not in $reg."
        echo "  If the name is a real registered tool, add it to $reg."
        echo "  If it is a non-tool identifier (Go symbol/file, metric, config key), add it to $nontools."
        hard_fail=1
    else
        echo "OK: benchmark reference pages cite only registered tools."
    fi
}

# ── Hard gate 4: engineering-posture claims still hold ──────────────────────
# README.md and docs/llms.txt state the test posture in prose. Delegated to a
# standalone script so `make posture-check` can run it alone while `make
# verify` still enforces it through this gate.
# Invoked through `bash` rather than executed directly, so a lost exec bit
# cannot turn a hard gate into a silent pass. A missing script is a failure,
# not a skip: the gates above skip only on a missing external toolchain, never
# on the state of a file that is part of this repository.
check_posture_claims() {
    if [ ! -f scripts/posture-check.sh ]; then
        echo "FAIL: scripts/posture-check.sh is missing; the posture gate cannot run."
        hard_fail=1
        return 0
    fi
    if ! bash scripts/posture-check.sh; then
        echo "FAIL: engineering-posture claims are stale (see above)."
        hard_fail=1
    fi
}

echo "=== Documentation Gates (hard) ==="
check_orphaned_docs
check_retired_tools
check_benchmark_tool_refs
check_posture_claims
echo "=== End Documentation Gates ==="
echo ""

if [ "$hard_fail" -ne 0 ]; then
    echo "Documentation gates FAILED — fix the issues above."
    exit 1
fi

# ── Soft gate: documentation-worthy changes lacking doc updates ─────────────
# Diff-based; warns only, never fails.

MERGE_BASE=$(git merge-base "$BASE_BRANCH" HEAD 2>/dev/null || true)
if [ -z "$MERGE_BASE" ]; then
    echo "SKIP: Could not determine merge base with $BASE_BRANCH."
    exit 0
fi

if [ "$MERGE_BASE" = "$(git rev-parse HEAD)" ]; then
    echo "SKIP: HEAD is the merge base (on $BASE_BRANCH or no new commits)."
    exit 0
fi

CHANGED_FILES=$(git diff --name-only "$MERGE_BASE"...HEAD)

docs_touched=0
if echo "$CHANGED_FILES" | grep -qE '^(README\.md|docs/)'; then
    docs_touched=1
fi

llms_touched=0
if echo "$CHANGED_FILES" | grep -qE '^docs/llms(-full)?\.txt$'; then
    llms_touched=1
fi

# If docs were updated but llms.txt wasn't, that's its own warning (CLAUDE.md item 11).
if [ "$docs_touched" -eq 1 ] && [ "$llms_touched" -eq 0 ]; then
    docs_md_touched=$(echo "$CHANGED_FILES" | grep -cE '^docs/.*\.md$' || true)
    if [ "$docs_md_touched" -gt 0 ]; then
        echo "WARNING: docs/*.md files changed but docs/llms.txt and docs/llms-full.txt were not updated."
        echo "  Per CLAUDE.md item 11, LLM-readable files must be kept in sync."
        echo ""
    fi
fi

# If docs were already touched, no need to nag about missing docs.
if [ "$docs_touched" -eq 1 ]; then
    echo "OK: Documentation was updated in this branch."
    exit 0
fi

warnings=""

# 1. New packages under pkg/ (new directories with .go files).
new_pkg_dirs=$(echo "$CHANGED_FILES" | grep -oE '^pkg/[^/]+(/[^/]+)*/' | sort -u || true)
for dir in $new_pkg_dirs; do
    if ! git ls-tree --name-only "$MERGE_BASE" -- "$dir" > /dev/null 2>&1 || \
       [ -z "$(git ls-tree --name-only "$MERGE_BASE" -- "$dir" 2>/dev/null)" ]; then
        has_go=$(echo "$CHANGED_FILES" | grep "^${dir}.*\.go$" | grep -v '_test\.go$' | head -1 || true)
        if [ -n "$has_go" ]; then
            warnings="${warnings}  - New package: ${dir}\n"
        fi
    fi
done

# 2. Config struct changes (config.go files modified).
config_changes=$(echo "$CHANGED_FILES" | grep -E 'config\.go$' | grep -v '_test\.go$' | tr '\n' ',' | sed 's/,$//' | sed 's/,/, /g' || true)
if [ -n "$config_changes" ]; then
    warnings="${warnings}  - Configuration changes: ${config_changes}\n"
fi

# 3. New Makefile targets.
#
# A target counts as documented when its NAME appears in a documentation
# file, which is the thing actually worth checking. The blanket
# "did you touch README.md or docs/" test above cannot see this one: the
# benchmark harness documents its own targets in bench/README.md and
# bench/docs/, so harness work would warn forever while platform docs it
# has no business editing sat untouched.
if echo "$CHANGED_FILES" | grep -q '^Makefile$'; then
    doc_corpus=$( { find docs bench -name '*.md' -not -path 'bench/results/*'; echo README.md; } 2>/dev/null)
    while IFS= read -r line; do
        [ -n "$line" ] || continue
        target_name=${line%%:*}
        if [ -n "$doc_corpus" ] && echo "$doc_corpus" | tr '\n' '\0' | xargs -0 grep -lF -- "$target_name" >/dev/null 2>&1; then
            continue
        fi
        warnings="${warnings}  - New target: ${line}\n"
    done <<EOF
$(git diff "$MERGE_BASE"...HEAD -- Makefile | grep '^+##' | grep -v '^+++' | sed 's/^+## //' || true)
EOF
fi

# 4. New CLI flags or commands in main.go.
if echo "$CHANGED_FILES" | grep -q 'cmd/.*main\.go$'; then
    warnings="${warnings}  - CLI entry point modified: cmd/mcp-data-platform/main.go\n"
fi

# 5. New toolkit registrations.
new_toolkits=$(echo "$CHANGED_FILES" | grep -E '^pkg/toolkits/[^/]+/toolkit\.go$' || true)
for tk in $new_toolkits; do
    if ! git show "$MERGE_BASE":"$tk" > /dev/null 2>&1; then
        warnings="${warnings}  - New toolkit: ${tk}\n"
    fi
done

# 6. New or modified migration files.
migration_changes=$(echo "$CHANGED_FILES" | grep -E '^pkg/database/migrate/migrations/.*\.sql$' || true)
if [ -n "$migration_changes" ]; then
    warnings="${warnings}  - Database migration changes\n"
fi

echo ""
echo "=== Documentation Check ==="

if [ -z "$warnings" ]; then
    echo "OK: No documentation-worthy changes detected (or docs already updated)."
else
    echo "WARNING: Documentation-worthy changes detected but no docs/ or README.md updates found."
    echo ""
    echo "Changes that may need documentation:"
    printf "%b" "$warnings"
    echo ""
    echo "Consider updating: README.md, docs/*.md, docs/llms.txt, docs/llms-full.txt"
fi

echo "=== End Documentation Check ==="
