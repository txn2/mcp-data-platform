#!/usr/bin/env bash
# Confirmatory matrix for the graph-completion study (#1251), exactly as the
# stage-3 design doc pre-registers it: graph vs stripped arms over the
# generated corpora at scales 50/500/5000, search on, k=5 x 3 cells per
# cell of the matrix, plus the auxiliary graph/no-search manipulation check
# at scale 5000 (k=3). Per scale: certify -> plant -> wait for embeddings
# (live pages joined to their chunks; deletes are soft, so a bare chunk
# count lies) -> gate -> run -> reset, one store at a time. The graphstudy
# binary is rebuilt at start, and episode runs go through `make
# bench-gs-run` (which rebuilds again and stamps the manifest commit,
# -dirty when the tree is), so an archive can never claim a commit whose
# instrument did not execute.
#
# Scale 50 is the within-enumeration-ceiling control: certification is
# unsatisfiable by construction and the sweep gate records discontinuity
# hits on purpose; both exits are readings, not failures, and the run mode
# itself still refuses a leak or a missing entry there.
#
# The design's power audit is honored in-line: if either 500-scale arm's
# per-episode off-entry grounded coverage shows SD above 0.30, both 500
# arms are topped up to k=8 (one replant-gate-run-reset round of k=3 each)
# before scale 5000 runs, and scale 5000 runs at k=8 across the board.
#
# Run from the repo root with the gt stack already up (make bench-gt-up),
# ollama serving nomic-embed-text, and the claude CLI on PATH; wrap the
# invocation in caffeinate -is. Every artifact lands in
# build/bench-results/ and is kept.
set -uo pipefail

SCALES=${SCALES:-"50 500 5000"}
KEY=${BENCH_KEY:-bench-admin-key}
URL=${BENCH_URL:-http://localhost:8098}
PG=${BENCH_PG_CONTAINER:-e2e-postgres}
DB=${BENCH_GT_DB:-mcp_bench_gt}
BIN=${BENCH_GS_BIN:-./build/bench-graphstudy}
MODEL=${BENCH_GS_MODEL:-opus}
K_PRIMARY=${BENCH_GS_K:-5}
K_AUX=${BENCH_GS_K_AUX:-3}
K_TOPUP=3
SD_LIMIT="0.30"

k_5000=$K_PRIMARY

log() { echo "[gs-confirm $(date +%H:%M:%S)] $*"; }

psql_count() {
    docker exec "$PG" psql -U platform -d "$DB" -Atc "$1" 2>/dev/null
}

# assert_empty_store fails fast when the store already holds live pages.
# The planter would refuse anyway, but checking here names the fix, and it
# is what makes wait_embeddings' whole-store count mean "this plant": with
# an empty store at plant time, every live page is ours.
assert_empty_store() {
    local live
    live=$(psql_count "SELECT COUNT(*) FROM portal_knowledge_pages WHERE deleted_at IS NULL")
    if [ "${live:-0}" -ne 0 ]; then
        log "ERROR: the store already holds $live live page(s); reset the previous plant before running the matrix"
        return 1
    fi
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

# within_ceiling mirrors graphgen.WithinCeiling (2*EffectiveTopK(n) >= n,
# horizon floored at 25): true only for n <= 50. The tolerance below must
# agree with what -mode run will actually accept, or the driver would log a
# gate failure as "recorded" and then die at the run step.
within_ceiling() { [ "$1" -le 50 ]; }

# certify runs the offline embedding certification; at the within-ceiling
# scale a non-zero exit with horizon_exceeds_corpus recorded is the
# pre-stated reading, anything else aborts.
certify() {
    local scale=$1
    local report="build/bench-results/graph-study-cert-$scale.json"
    log "=== scale $scale: embedding certification"
    if "$BIN" -mode certify -scale "$scale"; then
        return 0
    fi
    if [ ! -f "$report" ]; then
        log "ERROR: certify at scale $scale exited without writing $report"
        return 1
    fi
    if within_ceiling "$scale" && grep -q '"horizon_exceeds_corpus": true' "$report"; then
        log "certification unsatisfiable at scale $scale: the within-ceiling reading (recorded)"
        return 0
    fi
    log "ERROR: certification failed at certified scale $scale"
    return 1
}

plant_arm() {
    local scale=$1 arm=$2 strip_flag=""
    [ "$arm" = "stripped" ] && strip_flag="-strip"
    log "=== scale $scale: plant ($arm arm)"
    assert_empty_store || return 1
    # shellcheck disable=SC2086
    "$BIN" -mode plant -url "$URL" -credential "$KEY" -scale "$scale" $strip_flag || {
        log "ERROR: plant failed at scale $scale ($arm arm)"
        return 1
    }
    wait_embeddings "$scale"
}

gate_arm() {
    local scale=$1
    local report="build/bench-results/graph-study-gate-$scale.json"
    log "=== scale $scale: sweep gate"
    rm -f "$report"
    if "$BIN" -mode gate -url "$URL" -credential "$KEY" -scale "$scale"; then
        return 0
    fi
    if [ ! -f "$report" ]; then
        log "ERROR: gate at scale $scale exited without writing $report"
        return 1
    fi
    if within_ceiling "$scale"; then
        log "gate did not pass at scale $scale: the within-ceiling reading (recorded); the run mode still refuses leaks"
        return 0
    fi
    log "ERROR: gate failed at certified scale $scale"
    return 1
}

# run_cells executes one episode run through the make target (the single
# owner of run-dir naming, flag assembly, and commit stamping) and echoes
# the results directory the target printed.
run_cells() {
    local scale=$1 arm=$2 search=$3 k=$4 ns="" out dir
    [ "$search" = "nosearch" ] && ns="NOSEARCH=1"
    log "=== scale $scale: run ($arm/$search, k=$k)" >&2
    # shellcheck disable=SC2086
    out=$(make bench-gs-run BENCH_GS_SCALE="$scale" K="$k" MODEL="$MODEL" $ns 2>&1 | tee /dev/stderr) || {
        log "ERROR: run failed at scale $scale ($arm/$search)" >&2
        return 1
    }
    dir=$(printf '%s\n' "$out" | sed -n 's/^Results dir: //p' | tail -1)
    if [ -z "$dir" ] || [ ! -f "$dir/results.json" ]; then
        log "ERROR: bench-gs-run did not leave a results archive (dir: ${dir:-none})" >&2
        return 1
    fi
    echo "$dir"
}

# coverage_sd prints the per-episode off-entry grounded coverage SD of a
# run, or "nan" when fewer than two episodes survived (SD is undefined).
coverage_sd() {
    python3 - "$1" <<'EOF'
import json, math, sys
d = json.load(open(sys.argv[1] + "/results.json"))
vals = [a["coverage"]["off_entry_grounded"] / a["coverage"]["off_entry_total"]
        for a in d["attempts"] if not a.get("error")]
if len(vals) < 2:
    print("nan")
else:
    m = sum(vals) / len(vals)
    print(f"{math.sqrt(sum((v - m) ** 2 for v in vals) / (len(vals) - 1)):.4f}")
EOF
}

reset_arm() {
    local scale=$1
    log "=== scale $scale: reset"
    "$BIN" -mode reset -url "$URL" -credential "$KEY" -scale "$scale" || {
        log "ERROR: reset failed at scale $scale"
        return 1
    }
}

# sd_exceeds reports whether a run's coverage SD is above the audit limit.
# An unreadable SD (fewer than two surviving episodes) counts as exceeding:
# raising k is the conservative branch when the first certified scale gave
# almost no data.
sd_exceeds() {
    local arm=$1 dir=$2 sd
    sd=$(coverage_sd "$dir") || {
        log "ERROR: could not read coverage SD from $dir"
        exit 1
    }
    log "scale 500 $arm arm off-entry grounded coverage SD = $sd (limit $SD_LIMIT)"
    if [ "$sd" = "nan" ]; then
        log "scale 500 $arm arm has fewer than two graded episodes; treating as above the limit"
        return 0
    fi
    python3 -c "import sys; sys.exit(0 if float('$sd') > float('$SD_LIMIT') else 1)"
}

# arm_sequence runs one plant-gate-run-reset round for one (scale, arm) at
# one k, echoing the results directory. The auxiliary graph/no-search check
# rides the scale-5000 graph plant.
arm_sequence() {
    local scale=$1 arm=$2 k=$3 dir
    plant_arm "$scale" "$arm" >&2 || return 1
    gate_arm "$scale" >&2 || return 1
    dir=$(run_cells "$scale" "$arm" search "$k") || return 1
    if [ "$scale" -eq 5000 ] && [ "$arm" = "graph" ]; then
        run_cells 5000 graph nosearch "$K_AUX" >/dev/null || return 1
    fi
    reset_arm "$scale" >&2 || return 1
    echo "$dir"
}

# power_audit applies the pre-registered k rule after both 500 arms ran: SD
# above the limit in either arm tops both up to k=8 (one k=3 round each)
# and raises scale-5000 k to 8.
power_audit() {
    local graph_dir=$1 stripped_dir=$2 exceeded=0
    sd_exceeds graph "$graph_dir" && exceeded=1
    sd_exceeds stripped "$stripped_dir" && exceeded=1
    [ "$exceeded" -eq 1 ] || return 0
    log "power audit: SD above $SD_LIMIT; topping both 500 arms up to k=8 and raising scale-5000 k to 8"
    k_5000=8
    for arm in graph stripped; do
        arm_sequence 500 "$arm" "$K_TOPUP" >/dev/null || return 1
    done
}

log "building bench-graphstudy from the working tree"
mkdir -p build/bench-results
(cd bench && go build -o "../${BIN#./}" ./graphstudy) || {
    log "ERROR: could not build bench-graphstudy"
    exit 1
}

for scale in $SCALES; do
    certify "$scale" || exit 1
    k=$K_PRIMARY
    [ "$scale" -eq 5000 ] && k=$k_5000
    graph_dir=""
    stripped_dir=""
    for arm in graph stripped; do
        dir=$(arm_sequence "$scale" "$arm" "$k") || exit 1
        if [ "$arm" = "graph" ]; then graph_dir=$dir; else stripped_dir=$dir; fi
    done
    if [ "$scale" -eq 500 ]; then
        power_audit "$graph_dir" "$stripped_dir" || exit 1
    fi
done
log "confirmatory matrix complete"
