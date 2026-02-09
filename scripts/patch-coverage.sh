#!/usr/bin/env bash
# patch-coverage.sh — Local patch coverage check that mirrors codecov's patch target.
# Computes coverage of new/changed lines only and fails if below threshold.
# Compatible with bash 3.2+ (macOS default).
set -euo pipefail

THRESHOLD="${PATCH_COVERAGE_THRESHOLD:-80}"
COVERAGE_FILE="${COVERAGE_FILE:-coverage.out}"
BASE_BRANCH="${BASE_BRANCH:-main}"

TMPDIR_PC=$(mktemp -d)
trap 'rm -rf "$TMPDIR_PC"' EXIT

# ── Preflight ───────────────────────────────────────────────────────────────

if [ ! -f "$COVERAGE_FILE" ]; then
    echo "ERROR: $COVERAGE_FILE not found."
    echo "Run 'go test -coverprofile=coverage.out ./...' first."
    exit 1
fi

MERGE_BASE=$(git merge-base "$BASE_BRANCH" HEAD 2>/dev/null || true)
if [ -z "$MERGE_BASE" ]; then
    echo "SKIP: Could not determine merge base with $BASE_BRANCH."
    exit 0
fi

if [ "$MERGE_BASE" = "$(git rev-parse HEAD)" ]; then
    echo "SKIP: HEAD is the merge base (on $BASE_BRANCH or no new commits)."
    exit 0
fi

# ── Extract changed lines and coverage into flat files, then join with awk ──

MODULE=$(head -c 500 go.mod | grep '^module ' | awk '{print $2}')

# Step 1: Parse git diff into "file line" pairs (one per changed line).
# Only non-test .go files; skip pure-deletion hunks.
git diff --unified=0 "$MERGE_BASE"...HEAD | awk '
    /^\+\+\+ b\// {
        f = substr($0, 7)
        # skip non-Go and test files
        if (f !~ /\.go$/ || f ~ /_test\.go$/) f = ""
        next
    }
    f != "" && /^@@ / {
        # Find the +N or +N,M part in the hunk header.
        # Format: @@ -old[,oldcount] +new[,newcount] @@
        n = split($0, tokens, " ")
        plus_part = ""
        for (t = 1; t <= n; t++) {
            if (substr(tokens[t], 1, 1) == "+") {
                plus_part = substr(tokens[t], 2)  # strip leading +
                break
            }
        }
        if (plus_part == "") next

        # Split on comma: start[,count]
        nc = split(plus_part, sc, ",")
        start = sc[1] + 0
        count = (nc > 1) ? sc[2] + 0 : 1
        if (count == 0) next  # pure deletion
        for (i = start; i < start + count; i++) {
            print f, i
        }
    }
' | sort -u > "$TMPDIR_PC/changed.txt"

if [ ! -s "$TMPDIR_PC/changed.txt" ]; then
    echo "SKIP: No Go source file changes detected (test-only or deletion-only diff)."
    exit 0
fi

# Step 2: Parse coverage.out into "file line status" triples.
# status: 1 = covered (count>0), 0 = uncovered (count==0).
# A line that appears in multiple ranges is covered if ANY range has count>0.
awk -v module="$MODULE" '
    /^mode:/ { next }
    {
        # format: module/path/file.go:startLine.startCol,endLine.endCol numStmts count
        split($0, parts, ":")
        full_path = parts[1]
        rest = parts[2]

        # strip module prefix
        sub("^" module "/", "", full_path)

        # parse startLine.startCol,endLine.endCol
        split(rest, a, ",")
        split(a[1], sl, ".")
        start_line = sl[1] + 0

        # the second part: "endLine.endCol numStmts count"
        split(a[2], b, " ")
        split(b[1], el, ".")
        end_line = el[1] + 0
        count = b[3] + 0

        status = (count > 0) ? 1 : 0
        for (ln = start_line; ln <= end_line; ln++) {
            key = full_path SUBSEP ln
            # covered wins over uncovered
            if (status == 1 || !(key in seen)) {
                seen[key] = status
            }
        }
    }
    END {
        for (key in seen) {
            split(key, kp, SUBSEP)
            print kp[1], kp[2], seen[key]
        }
    }
' "$COVERAGE_FILE" | sort > "$TMPDIR_PC/coverage.txt"

# Step 3: Join changed lines with coverage data and compute results.
# For each changed line, look up whether it's executable (in coverage.out)
# and whether it's covered.
awk '
    # First file: coverage.txt (file line status)
    NR == FNR {
        cov[$1 ":" $2] = $3
        next
    }
    # Second file: changed.txt (file line)
    {
        key = $1 ":" $2
        if (key in cov) {
            exec_count[$1]++
            total_exec++
            if (cov[key] == 1) {
                cov_count[$1]++
                total_cov++
            } else {
                uncov[$1] = uncov[$1] " " $2
            }
        }
        # Track all files for reporting
        if (!($1 in seen_file)) {
            files[++nf] = $1
            seen_file[$1] = 1
        }
    }
    END {
        # Sort file names
        for (i = 1; i <= nf; i++) {
            for (j = i + 1; j <= nf; j++) {
                if (files[i] > files[j]) {
                    tmp = files[i]; files[i] = files[j]; files[j] = tmp
                }
            }
        }

        for (i = 1; i <= nf; i++) {
            f = files[i]
            e = exec_count[f] + 0
            c = cov_count[f] + 0
            if (e == 0) {
                printf "  %-60s  (no executable changed lines)\n", f
            } else {
                pct = (c / e) * 100
                printf "  %-60s  %d/%d (%.1f%%)", f, c, e, pct
                if (uncov[f] != "") {
                    printf "  uncovered lines:%s", uncov[f]
                }
                printf "\n"
            }
        }

        printf "\n"
        if (total_exec + 0 == 0) {
            print "SKIP: No executable changed lines found in diff."
            # Signal skip to the shell
            print "RESULT:SKIP" > "/dev/stderr"
            exit 0
        }

        patch_pct = (total_cov / total_exec) * 100
        printf "Patch coverage: %d/%d executable changed lines = %.1f%%\n", total_cov, total_exec, patch_pct
        printf "RESULT:%.1f\n", patch_pct > "/dev/stderr"
    }
' "$TMPDIR_PC/coverage.txt" "$TMPDIR_PC/changed.txt" \
    >"$TMPDIR_PC/report.txt" 2>"$TMPDIR_PC/result.txt"

# ── Final verdict ────────────────────────────────────────────────────────────

echo ""
echo "=== Patch Coverage Report ==="
echo "Base: $BASE_BRANCH (merge-base: $(echo "$MERGE_BASE" | cut -c1-10))"
echo ""

# Print the awk file details + summary.
cat "$TMPDIR_PC/report.txt"

RESULT_LINE=$(grep '^RESULT:' "$TMPDIR_PC/result.txt" 2>/dev/null || echo "RESULT:SKIP")
RESULT_VAL="${RESULT_LINE#RESULT:}"

if [ "$RESULT_VAL" = "SKIP" ]; then
    echo "SKIP: No executable changed lines found in diff."
    echo "=== End Patch Coverage ==="
    exit 0
fi

echo "Threshold: ${THRESHOLD}%"
FAIL=$(awk "BEGIN {print ($RESULT_VAL < $THRESHOLD) ? 1 : 0}")
if [ "$FAIL" -eq 1 ]; then
    echo ""
    echo "FAIL: Patch coverage ${RESULT_VAL}% is below ${THRESHOLD}% threshold."
    echo "Add tests for the uncovered lines listed above."
    echo "=== End Patch Coverage ==="
    exit 1
fi

echo "PASS"
echo "=== End Patch Coverage ==="
