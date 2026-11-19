#!/usr/bin/env bash
# release-check-isolated.sh: the GoReleaser dry run on an export of the working
# tree (#2711).
#
# The dry run's before hook runs `make frontend-build`, which empties and
# rewrites internal/ui/dist. Run in the checkout, inside verify's UI lane, it
# did that while the Go lane and the Docker lane compiled internal/ui, whose
# `//go:embed all:dist` read a directory listing and then failed to open a
# file the build had removed meanwhile ("embed dist/mockServiceWorker.js: no
# such file or directory"). Here the build writes the export's dist, which no
# other lane reads, and the checkout is left as it was.
#
# The export is the working tree as `git add -A` sees it (tracked and
# untracked, ignored files left out), built the way scripts/codeql-local.sh
# builds its database, with the UI's installed node_modules cloned in and a
# throwaway repository GoReleaser reads its version and commit from.
set -euo pipefail

work=$(mktemp -d "${TMPDIR:-/tmp}/mcp-dp-release.XXXXXX")
trap 'rc=$?; rm -rf "$work"; exit $rc' EXIT
src="$work/src"

cp "$(git rev-parse --git-path index)" "$work/index"
GIT_INDEX_FILE="$work/index" git add -A
GIT_INDEX_FILE="$work/index" git checkout-index -a --prefix="$src/"

# The before hook's npm ci reinstalls node_modules either way; a clone of the
# installed tree (a copy-on-write clone where the filesystem has one) lets it
# start from the same lockfile state the checkout is in.
if [ -d ui/node_modules ]; then
  cp -c -R ui/node_modules "$src/ui/" 2> /dev/null || cp -R --reflink=auto ui/node_modules "$src/ui/" 2> /dev/null || cp -R ui/node_modules "$src/ui/"
fi

(
  cd "$src"
  git init -q
  git add -A
  git -c user.name=release-check -c user.email=release-check@localhost commit -q -m "release-check export"
  # A GITLAB_TOKEN or GITEA_TOKEN in the shell makes GoReleaser pick that
  # forge's client, and its homebrew step then blocks with no CPU and no
  # output; CI's runners carry neither. The dry run publishes nothing.
  env -u GITLAB_TOKEN -u GITEA_TOKEN goreleaser release --snapshot --clean --skip=publish,sign,sbom
)
