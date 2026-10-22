#!/usr/bin/env python3
"""Sample the real-database lane's PostgreSQL connections while it runs (#2249).

make test-realdb starts one server and runs every package's RealDB tests
against it, four packages at once. The server's max_connections is set from
what those packages open, and a lane that outgrows it fails with
"sorry, too many clients already" in whichever test asks next
(TestOAuthStore_Migration78HashesPlaintext_RealDB did under make verify's load).

  realdb-connections.py <container> <report-file>

polls pg_stat_activity in the container twice a second until it receives
SIGTERM, then writes the peak number of client connections, the
max_connections setting, and the five packages that held the most at once. -p 4 runs at most four
packages together, so the four largest bound what the lane can hold.
A test database is named testdb_<pid>_<n> (internal/testdb), and the pid is
the test binary's, which names the package. Sampling never fails the lane: a
poll that does not answer is skipped.
"""

from __future__ import annotations

import os
import re
import signal
import subprocess
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
TEST_DB = re.compile(r"^testdb_(\d+)_\d+$")
QUERY = "SELECT datname FROM pg_stat_activity WHERE backend_type = 'client backend'"


def psql(container: str, sql: str) -> list[str] | None:
    try:
        out = subprocess.run(
            ["docker", "exec", container, "psql", "-U", "test", "-d", "postgres", "-Atc", sql],
            capture_output=True, text=True, timeout=5,
        )
    except (subprocess.TimeoutExpired, OSError):
        return None
    if out.returncode != 0:
        return None
    return out.stdout.splitlines()


def package(pid: str, names: dict[str, str]) -> str:
    """The package a test binary runs for: go test starts it in the package's
    directory. Read while it still runs; the binary's name when the directory
    cannot be read (several packages share a name, such as postgres.test)."""
    if pid not in names:
        out = subprocess.run(["lsof", "-a", "-p", pid, "-d", "cwd", "-Fn"], capture_output=True, text=True)
        cwd = next((line[1:] for line in out.stdout.splitlines() if line.startswith("n")), "")
        if cwd:
            names[pid] = os.path.relpath(cwd, REPO_ROOT)
        else:
            comm = subprocess.run(["ps", "-o", "comm=", "-p", pid], capture_output=True, text=True)
            names[pid] = Path(comm.stdout.strip()).name or f"pid {pid}"
    return names[pid]


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(f"usage: {argv[0]} <container> <report-file>", file=sys.stderr)
        return 2
    container, report = argv[1], Path(argv[2])
    stop = False

    def on_term(*_):
        nonlocal stop
        stop = True

    signal.signal(signal.SIGTERM, on_term)
    peak, per_binary, names = 0, {}, {}
    while not stop:
        rows = psql(container, QUERY)
        if rows is not None:
            peak = max(peak, len(rows))
            now: dict[str, int] = {}
            for datname in rows:
                m = TEST_DB.match(datname)
                key = package(m.group(1), names) if m else "template and admin"
                now[key] = now.get(key, 0) + 1
            for key, n in now.items():
                per_binary[key] = max(per_binary.get(key, 0), n)
        time.sleep(0.5)

    limit = (psql(container, "SHOW max_connections") or ["?"])[0]
    top = sorted(per_binary.items(), key=lambda kv: -kv[1])[:5]
    lines = [f"realdb connections: peak {peak} of max_connections {limit}"]
    lines += [f"  {n:4d}  {name}" for name, n in top]
    report.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
