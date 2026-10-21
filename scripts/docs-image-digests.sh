#!/bin/sh
# Resolve every image the documentation pins to a digest (#2224).
#
# A digest written in the docs is checked by nothing else: three counting
# patterns (sha256:acf5271b...e1f2a3b) shipped in the deployment guide's
# compose example, and anyone who copied it got "manifest unknown". This asks
# each image's registry for the digest as written and fails on one it does not
# serve. It needs network access and Docker, so it runs in the E2E Nightly,
# not in verify.
#
# Usage: docs-image-digests.sh [dir]   (default: docs)
# Prints one line per image that does not resolve and exits 1 when there is one.
set -eu

dir=${1:-docs}
refs=$(grep -rhoE '[a-z0-9][a-z0-9./_-]*(:[A-Za-z0-9._-]+)?@sha256:[0-9a-zA-Z]+' "$dir" | sort -u)
if [ -z "$refs" ]; then
	echo "docs-image-digests: no image pinned to a digest under $dir"
	exit 0
fi

failed=0
for ref in $refs; do
	digest=${ref##*@sha256:}
	if ! printf '%s' "$digest" | grep -qE '^[0-9a-f]{64}$'; then
		echo "docs-image-digests: $ref: a digest is 64 lowercase hex characters"
		failed=1
		continue
	fi
	if ! docker buildx imagetools inspect "$ref" >/dev/null 2>&1; then
		echo "docs-image-digests: $ref: the registry does not serve this digest"
		failed=1
		continue
	fi
	echo "docs-image-digests: $ref"
done
exit $failed
