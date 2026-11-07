#!/usr/bin/env bash
# host-sampler.sh writes one line of host state every few seconds until it is
# killed: memory, swap traffic, I/O wait, load, and each container's CPU and
# memory. The nightly acceptance runs keep it beside the platform's logs, so a
# stall that stops every container at once (#2516: 197 s on a runner, with the
# database, the renderer and the collector all silent) can be read as memory
# pressure, an I/O stall or a host freeze rather than guessed at. A gap in its
# own timestamps is itself the answer for a freeze of the host.
#
# Usage: scripts/host-sampler.sh <output file> [interval seconds]
set -u
out=${1:?usage: host-sampler.sh <output file> [interval seconds]}
interval=${2:-5}
mkdir -p "$(dirname "$out")"
while true; do
  {
    printf '=== %s load=%s\n' "$(date -u +%FT%TZ)" "$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null)"
    free -m 2>/dev/null | awk 'NR>1{print "mem " $0}'
    # One vmstat interval: si/so are swap in/out, wa is I/O wait.
    vmstat 1 2 2>/dev/null | tail -1 | awk '{print "vmstat r=" $1 " b=" $2 " si=" $7 " so=" $8 " bi=" $9 " bo=" $10 " us=" $13 " sy=" $14 " id=" $15 " wa=" $16}'
    timeout 4 docker stats --no-stream --format 'ctr {{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}} io={{.BlockIO}}' 2>/dev/null
  } >>"$out"
  sleep "$interval"
done
