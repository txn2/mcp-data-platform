#!/usr/bin/env bash
# Builds the dev server to the path given (#2727): the embedded portal first
# (scripts/dev-embed.sh), then the binary with the commit stamped, so the build
# every answer names (internal/buildinfo Build) is the same on both dev
# replicas and differs between two builds of different commits. dev/start.sh's
# first build, air's rebuilds (dev/.air.toml) and the second replica's all
# build through here, so no replica is built without the commit.
set -euo pipefail
out="${1:?usage: dev-build.sh <output binary>}"
bash scripts/dev-embed.sh
commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
exec go build -ldflags "-X github.com/txn2/mcp-data-platform/internal/buildinfo.Commit=${commit}" -o "$out" ./cmd/mcp-data-platform
