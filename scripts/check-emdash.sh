#!/usr/bin/env bash
# check-emdash.sh — Fail when a change ADDS an em dash to user-facing text.
#
# Scope is deliberately narrow: user documentation (docs/) and UI source
# (ui/src/). Em dashes read as AI-generated in customer-facing copy. Code
# comments are NOT checked — em dashes there are harmless and hunting them
# wastes effort.
#
# Diff-based: only newly added (+) lines are inspected, so pre-existing em
# dashes never fail the build and no repo-wide scrub is required.
# Compatible with bash 3.2+.
set -euo pipefail

BASE_BRANCH="${BASE_BRANCH:-main}"
EM_DASH=$'\xe2\x80\x94' # U+2014

MERGE_BASE=$(git merge-base "$BASE_BRANCH" HEAD 2>/dev/null || true)
if [ -z "$MERGE_BASE" ]; then
    echo "check-emdash: SKIP (no merge base with $BASE_BRANCH)."
    exit 0
fi

# Added lines in user-facing paths only. Include staged/working changes so the
# check runs the same locally and in CI.
ADDED=$(git diff "$MERGE_BASE" -- docs/ ui/src/ | grep '^+' | grep -v '^+++' || true)

HITS=$(printf '%s\n' "$ADDED" | grep -F "$EM_DASH" || true)

if [ -n "$HITS" ]; then
    echo "ERROR: em dash (—) added to user-facing text (docs/ or ui/src/)."
    echo "Replace with a comma, colon, semicolon, hyphen, or two sentences:"
    echo ""
    printf '%s\n' "$HITS"
    exit 1
fi

echo "check-emdash: OK (no new em dashes in docs/ or ui/src/)."
