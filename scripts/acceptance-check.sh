#!/usr/bin/env bash
# acceptance-check.sh — Soft gate: a change to the platform's non-test Go code
# with no acceptance test beside it.
#
# The acceptance suite (test/acceptance) is where a ticket's acceptance
# criteria are executed through the real tool surface against a running
# platform (`make acceptance`, required by `make verify-release`). This check
# warns when the working tree changes production Go under pkg/, internal/ or
# cmd/ relative to the base branch and touches no test/acceptance/*_test.go, so
# a feature does not reach a release having never been run.
#
# It never fails on the diff's contents. It does fail when the base branch
# cannot be resolved: a check that silently skips is a check that was not run.
#
# Compatible with bash 3.2+ (macOS) and GNU bash (CI).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BASE_BRANCH="${BASE_BRANCH:-main}"

resolve_base() {
    local ref
    for ref in "origin/${BASE_BRANCH}" "${BASE_BRANCH}"; do
        if git rev-parse --verify --quiet "${ref}^{commit}" > /dev/null; then
            git merge-base HEAD "$ref"
            return 0
        fi
    done
    return 1
}

if ! MERGE_BASE="$(resolve_base)"; then
    echo "FAIL acceptance-check: cannot resolve base branch '${BASE_BRANCH}' (tried origin/${BASE_BRANCH} and ${BASE_BRANCH}); fetch it or set BASE_BRANCH." >&2
    exit 1
fi

# Everything that differs from the base: committed, staged, unstaged and
# untracked, so the check reads the same tree `make verify` reads.
changed="$( { git diff --name-only "$MERGE_BASE"; git ls-files --others --exclude-standard; } | sort -u )"

production_go="$(printf '%s\n' "$changed" | grep -E '^(pkg|internal|cmd)/.*\.go$' | grep -vE '_test\.go$' || true)"
acceptance="$(printf '%s\n' "$changed" | grep -E '^test/acceptance/.*_test\.go$' || true)"

if [ -z "$production_go" ]; then
    echo "acceptance-check: no production Go changed against ${BASE_BRANCH}; nothing to check."
    exit 0
fi

# MCP is JSON-RPC, and a parameter whose schema is untyped accepts more than
# one JSON form. #1548 reached a release with its acceptance test green
# because every check sent api_invoke_endpoint.body as an object and the
# client sent a string. The acceptance test sends every form the schema
# admits, and the transcript of the run lives at build/<n>/acceptance.md,
# opening with a "Wire forms:" line naming the forms sent. Missing or
# unmarked, this check fails: no transcript, no check.
if [ -n "$acceptance" ]; then
    count="$(printf '%s\n' "$acceptance" | grep -c . || true)"
    missing=""
    for file in $acceptance; do
        n="$(basename "$file" | sed -nE 's/^issue_([0-9]+)_test\.go$/\1/p')"
        [ -n "$n" ] || continue
        transcript="build/${n}/acceptance.md"
        if [ ! -s "$transcript" ] || ! grep -q '^Wire forms:' "$transcript"; then
            missing="${missing}  ${file} -> ${transcript}\n"
        fi
    done
    if [ -n "$missing" ]; then
        printf 'FAIL acceptance-check: %s acceptance file(s) changed, but the run transcript is missing or does not open with a "Wire forms:" line:\n' "$count" >&2
        printf '%b' "$missing" >&2
        echo '  Run the ticket'"'"'s acceptance against the dev stack (make acceptance) sending every JSON form each touched parameter accepts, and keep the transcript at build/<n>/acceptance.md starting with "Wire forms: <the forms sent>".' >&2
        exit 1
    fi
    echo "acceptance-check: ${count} acceptance file(s) changed beside the Go changes, each with a run transcript under build/<n>/."
    exit 0
fi

count="$(printf '%s\n' "$production_go" | grep -c . || true)"
cat <<EOF
WARNING acceptance-check: ${count} production Go file(s) changed against ${BASE_BRANCH} and no test/acceptance/*_test.go changed.
  Every ticket's acceptance criteria are executed through the real tool surface against a running platform before the change is declared ready:
    1. write test/acceptance/issue_<n>_test.go from the ticket's Acceptance section,
    2. run it with \`make dev\` up: \`make acceptance\`,
    3. keep the transcript under build/<n>/acceptance.md.
  A change that touches no user-facing behavior (a refactor, a log line) may leave this warning standing and say so in the PR.
EOF
exit 0
