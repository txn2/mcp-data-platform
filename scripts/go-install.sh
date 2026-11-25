#!/usr/bin/env bash
# go-install.sh -- `go install` a pinned tool in CI, tried again when the
# module proxy drops the download.
#
# A tool's install downloads its whole dependency tree from proxy.golang.org on
# every run. On 2026-11-24 one acceptance job of the E2E Nightly failed before
# any criterion ran because the proxy reset one stream while air's install
# fetched a hugo dependency (#2789). A failed download leaves nothing behind
# that a second attempt trips on, so the install is run again after a pause.
# A compile error fails each attempt the same way and fails the step.
#
# Usage: scripts/go-install.sh <module path>@<version> ...
# The environment passes through, so `GOTOOLCHAIN=local scripts/go-install.sh`
# installs as `GOTOOLCHAIN=local go install` does.
#
# Environment: GO_INSTALL_ATTEMPTS (default 3), GO_INSTALL_PAUSE seconds before
# the first retry, doubled after each (default 10).
set -euo pipefail

if [ $# -eq 0 ]; then
	echo "usage: $0 <module path>@<version> ..." >&2
	exit 2
fi

attempts=${GO_INSTALL_ATTEMPTS:-3}
pause=${GO_INSTALL_PAUSE:-10}

for ((i = 1; i <= attempts; i++)); do
	if go install "$@"; then
		exit 0
	fi
	if ((i < attempts)); then
		echo "go-install: attempt $i of $attempts failed: go install $*; trying again in ${pause}s" >&2
		sleep "$pause"
		pause=$((pause * 2))
	fi
done
echo "go-install: go install $* failed $attempts times" >&2
exit 1
