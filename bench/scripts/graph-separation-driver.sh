#!/usr/bin/env bash
# Live separation demonstration for the graph-completion study, stage 3
# (#1250): plant the generated corpus at each scale (graph arm), wait for
# embeddings (live pages joined to their chunks — deletes are soft, so a
# bare chunk count lies), run the sweep gate with the discontinuity
# requirement on, reset, and leave every reading in build/bench-results/.
#
# Scale 50's gate failure is the pre-stated within-enumeration-ceiling
# reading and is recorded, not fatal; any other failure aborts the sequence.
# Run from the repo root with the gt stack already up (make bench-gt-up).
set -uo pipefail

SCALES=${SCALES:-"50 500 5000"}
KEY=${BENCH_KEY:-bench-admin-key}
URL=${BENCH_URL:-http://localhost:8098}
PG=${BENCH_PG_CONTAINER:-e2e-postgres}
DB=${BENCH_GT_DB:-mcp_bench_gt}
BIN=${BENCH_GS_BIN:-./build/bench-graphstudy}

log() { echo "[gs-driver $(date +%H:%M:%S)] $*"; }

psql_count() {
    docker exec "$PG" psql -U platform -d "$DB" -Atc "$1" 2>/dev/null
}

wait_embeddings() {
    local want=$1 have
    log "waiting for $want live pages to embed (reconciler sweeps every 5 minutes)"
    for _ in $(seq 1 360); do # up to 90 minutes
        have=$(psql_count "SELECT COUNT(DISTINCT p.id) FROM portal_knowledge_pages p JOIN portal_knowledge_page_embedding_chunks c ON c.page_id = p.id WHERE p.deleted_at IS NULL")
        if [ "${have:-0}" -ge "$want" ]; then
            log "embeddings ready ($have live pages)"
            return 0
        fi
        sleep 15
    done
    log "ERROR: embeddings did not become ready (last count: ${have:-0}/$want)"
    return 1
}

for scale in $SCALES; do
    log "=== scale $scale: plant (graph arm)"
    "$BIN" -mode plant -url "$URL" -credential "$KEY" -scale "$scale" || {
        log "ERROR: plant failed at scale $scale"
        exit 1
    }
    wait_embeddings "$scale" || exit 1
    log "=== scale $scale: sweep gate"
    gate_report="build/bench-results/graph-study-gate-$scale.json"
    rm -f "$gate_report"
    if ! "$BIN" -mode gate -url "$URL" -credential "$KEY" -scale "$scale"; then
        # A failed gate is a reading only if the sweep actually ran and wrote
        # its report; a transport or record error before that is a lost run,
        # never the within-ceiling reading.
        if [ ! -f "$gate_report" ]; then
            log "ERROR: gate at scale $scale exited without writing $gate_report"
            exit 1
        fi
        if [ "$scale" -le 100 ]; then
            log "gate did not pass at scale $scale: the pre-stated within-ceiling reading (recorded)"
        else
            log "ERROR: gate failed at certified scale $scale"
            exit 1
        fi
    fi
    log "=== scale $scale: reset"
    "$BIN" -mode reset -url "$URL" -credential "$KEY" -scale "$scale" || {
        log "ERROR: reset failed at scale $scale"
        exit 1
    }
done
log "separation demonstration complete"
