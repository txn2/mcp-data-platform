#!/usr/bin/env bash
# acceptance-check.sh — Two gates over the acceptance suite (test/acceptance),
# where a ticket's criteria are executed through the real tool surface against
# a running platform (`make acceptance`, required by `make verify-release`).
#
# A change to production Go under pkg/, internal/ or cmd/ with no acceptance
# file beside it WARNS, so a feature does not reach a release having never been
# run. A changed acceptance file whose criteria have no passing run on record
# FAILS: the evidence half is delegated to scripts/acceptance-evidence.py and
# reads the go test -json stream, not the transcript's prose (#1680).
#
# It also fails when the base branch cannot be resolved: a check that silently
# skips is a check that was not run.
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

# The evidence half of the gate reads results, not prose.
#
# Two things are checked for every changed test/acceptance/issue_<n>_test.go.
# First, every `func TestIssue<n>_...` it declares has a terminal pass event in
# build/<n>/acceptance.jsonl, the go test -json stream `make acceptance` writes:
# a criterion that was absent, skipped or failed fails the gate, so a transcript
# section saying a criterion did not run is a failed gate by construction
# (#1663 shipped behind exactly that paragraph, and 1.131.0 shipped the tool it
# covered unreachable -- #1675, #1680). Second, MCP is JSON-RPC and a parameter
# whose schema is untyped accepts more than one JSON form (#1548), so the run's
# transcript at build/<n>/acceptance.md must still carry a "Wire forms:"
# line naming the forms sent. A tool the diff registers with no criterion
# calling it fails the gate too: #1277 registered three tools and closed with
# criteria for two.
if [ -n "$acceptance" ]; then
    # shellcheck disable=SC2086 # the file list is newline-separated by construction
    exec python3 scripts/acceptance-evidence.py check --merge-base "$MERGE_BASE" $acceptance
fi

count="$(printf '%s\n' "$production_go" | grep -c . || true)"
cat <<EOF
WARNING acceptance-check: ${count} production Go file(s) changed against ${BASE_BRANCH} and no test/acceptance/*_test.go changed.
  Every ticket's acceptance criteria are executed through the real tool surface against a running platform before the change is declared ready:
    1. write test/acceptance/issue_<n>_test.go from the ticket's Acceptance section,
    2. run it with \`make dev\` up: \`make acceptance ISSUE=<n>\`, which records the run at build/<n>/acceptance.jsonl,
    3. keep the transcript under build/<n>/acceptance.md, carrying a "Wire forms:" line.
  A change that touches no user-facing behavior (a refactor, a log line) may leave this warning standing and say so in the PR.
EOF
exit 0
