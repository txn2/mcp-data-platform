#!/usr/bin/env bash
# codeql-local.sh: CodeQL's security-and-quality suite over an export of the
# working tree (#2548).
#
#   codeql-local.sh [traced|autobuild] [<sarif out>]
#
# The tree is exported first, tracked and untracked files as they stand (an
# ignored file is not part of the tree), and the database is built from the
# export, so the checkout is never written: CodeQL's autobuild runs this
# Makefile's default goal, and a local run once removed tracked files under
# ui/vendor/maplibre from the working tree.
#
# traced (the default) builds the database from one `go build ./...` per Go
# module, which is all the Go extractor needs: it extracts what a compile
# reads. autobuild leaves the build to CodeQL, which runs `make` (all: build
# test lint), so the whole unit suite and the lint, twice. CI's CodeQL job uses
# autobuild; make codeql-parity runs both over one tree and requires the same
# findings (test/gates/codeql_parity_test.go).
set -euo pipefail

mode=${1:-traced}
out=${2:-codeql-results.sarif}
case "$mode" in
  traced | autobuild) ;;
  *) echo "usage: $0 [traced|autobuild] [<sarif out>]" >&2; exit 2 ;;
esac
command -v codeql > /dev/null || { echo "codeql-local: the codeql CLI is not installed" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/mcp-dp-codeql.XXXXXX")
# The status is kept: bash 3.2 otherwise exits with the trap's own status, so
# a failed step read as success.
trap 'rc=$?; rm -rf "$work"; exit $rc' EXIT
src="$work/src"
db="$work/db"

# A copy of the index, so the export holds untracked files too and the real
# index is left as it was.
cp "$(git rev-parse --git-path index)" "$work/index"
GIT_INDEX_FILE="$work/index" git add -A
GIT_INDEX_FILE="$work/index" git checkout-index -a --prefix="$src/"

echo "codeql-local: building the database ($mode) from an export of the working tree"
if [ "$mode" = traced ]; then
  # Every directory holding a go.mod: the root module, test/load and bench.
  # ui/go.mod only fences node_modules off the root module and holds no Go.
  steps=""
  for m in $(cd "$src" && find . -name go.mod -not -path './ui/*' -exec dirname {} \; | sort); do
    steps="${steps:+$steps && }(cd $m && go build ./...)"
  done
  codeql database create "$db" --language=go --source-root="$src" --overwrite --command="sh -c \"$steps\""
else
  codeql database create "$db" --language=go --source-root="$src" --overwrite
fi
codeql database analyze "$db" --format=sarif-latest --output="$out" \
  codeql/go-queries:codeql-suites/go-security-and-quality.qls
[ -s "$out" ] || { echo "codeql-local: CodeQL wrote no results to $out" >&2; exit 1; }
