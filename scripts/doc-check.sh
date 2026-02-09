#!/usr/bin/env bash
# doc-check.sh — Warn when documentation-worthy changes lack doc updates.
# Soft warning only (never fails). Compatible with bash 3.2+.
set -euo pipefail

BASE_BRANCH="${BASE_BRANCH:-main}"

# ── Preflight ───────────────────────────────────────────────────────────────

MERGE_BASE=$(git merge-base "$BASE_BRANCH" HEAD 2>/dev/null || true)
if [ -z "$MERGE_BASE" ]; then
    echo "SKIP: Could not determine merge base with $BASE_BRANCH."
    exit 0
fi

if [ "$MERGE_BASE" = "$(git rev-parse HEAD)" ]; then
    echo "SKIP: HEAD is the merge base (on $BASE_BRANCH or no new commits)."
    exit 0
fi

# ── Collect changed files ───────────────────────────────────────────────────

CHANGED_FILES=$(git diff --name-only "$MERGE_BASE"...HEAD)

# ── Check if docs were touched ──────────────────────────────────────────────

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

# ── Detect documentation-worthy changes ─────────────────────────────────────

warnings=""

# 1. New packages under pkg/ (new directories with .go files).
new_pkg_dirs=$(echo "$CHANGED_FILES" | grep -oE '^pkg/[^/]+(/[^/]+)*/' | sort -u || true)
for dir in $new_pkg_dirs; do
    # Check if this directory exists on the merge base.
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
if echo "$CHANGED_FILES" | grep -q '^Makefile$'; then
    new_targets=$(git diff "$MERGE_BASE"...HEAD -- Makefile | \
        grep '^+##' | grep -v '^+++' | sed 's/^+## /  - New target: /' || true)
    if [ -n "$new_targets" ]; then
        warnings="${warnings}${new_targets}\n"
    fi
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

# ── Report ───────────────────────────────────────────────────────────────────

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
