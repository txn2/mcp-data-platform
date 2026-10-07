#!/usr/bin/env bash
# Runs deployments/observability/alert-rules.test.yaml against the alert rules
# in deployments/observability/alert-rules.yaml with promtool.
#
# The rules ship as a Kubernetes ConfigMap, which promtool cannot load, so the
# rule groups are extracted into a plain rules file beside the test file first.
# promtool comes from the Prometheus image dev/docker-compose.yml already pins,
# so nothing is installed on the machine; a local promtool is used when PATH
# has one.
set -euo pipefail

cd "$(dirname "$0")/.."
DIR=deployments/observability
# The image is run by its tag: docker run refuses a reference carrying both a
# tag and a digest, and the pin itself is held by dev/docker-compose.yml, which
# `make dev` pulls.
PINNED=$(grep -oE 'prom/prometheus:[^@ ]+@sha256:[0-9a-f]+' dev/docker-compose.yml | head -1)
IMAGE=${PINNED%@*}
EXTRACTED="$DIR/alert-rules.extracted.yaml"
trap 'rm -f "$EXTRACTED"' EXIT

python3 - "$DIR/alert-rules.yaml" > "$EXTRACTED" <<'PY'
import sys

# The ConfigMap's data value is the rules document, indented under its key.
lines = open(sys.argv[1]).read().splitlines()
start = next(i for i, l in enumerate(lines) if l.strip() == "alert-rules.yaml: |") + 1
indent = len(lines[start]) - len(lines[start].lstrip())
for l in lines[start:]:
    if l.strip() and (len(l) - len(l.lstrip())) < indent:
        break
    print(l[indent:] if l.strip() else "")
PY

if command -v promtool > /dev/null 2>&1; then
  promtool test rules "$DIR/alert-rules.test.yaml"
else
  docker image inspect "$IMAGE" > /dev/null 2>&1 || docker pull -q "$IMAGE" > /dev/null
  docker run --rm -v "$PWD/$DIR:/rules:ro" -w /rules --entrypoint promtool "$IMAGE" test rules alert-rules.test.yaml
fi
