#!/bin/sh
# Refuse a pinned Go tool built with a newer Go than CI installs (#2147).
#
# CI installs each pinned tool with `GOTOOLCHAIN=local go install <tool>@<pin>`
# under the go-version its workflow sets up. A local `go install` runs under the
# default GOTOOLCHAIN=auto, which fetches a newer toolchain when the tool's
# go.mod asks for one, so a pin CI cannot build installs and passes every local
# gate (osv-scanner v2.6.0 needs go 1.27; CI had 1.26.9, #2146). The binary
# records the Go it was built with, and that is what this reads.
#
# Usage: tools-go-check.sh <workflow.yml> <tool>=<install path> ...
# Prints one line per refused tool and exits 1 when there is one. A tool not on
# PATH is left to tools-check's presence check.
set -eu

workflow=$1
shift

ci_go=$(grep -oE "go-version: *['\"]?[0-9]+\.[0-9]+(\.[0-9]+)?" "$workflow" |
	grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | sort -V | head -1)
if [ -z "$ci_go" ]; then
	echo "  tools-go-check: no go-version found in $workflow" >&2
	exit 1
fi

status=0
for pair in "$@"; do
	tool=${pair%%=*}
	install=${pair#*=}
	path=$(command -v "$tool" 2>/dev/null) || continue
	built=$(go version "$path" 2>/dev/null | awk '{print $NF}' | sed 's/^go//')
	[ -n "$built" ] || continue
	newest=$(printf '%s\n%s\n' "$ci_go" "$built" | sort -V | tail -1)
	if [ "$built" != "$ci_go" ] && [ "$newest" = "$built" ]; then
		echo "  $tool: built with go$built, CI installs go$ci_go -- GOTOOLCHAIN=local go install $install (fails here when the pin needs a newer Go than CI has)"
		status=1
	fi
done
exit $status
