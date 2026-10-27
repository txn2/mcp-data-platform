#!/usr/bin/env python3
"""The acceptance environments of test/acceptance/environments.txt (#2219).

  flags [env] [--shard i/N]
                the go test flags that run one environment's criteria: with no
                env (or "dev"), -skip every environment's criteria and say on
                stderr which were left out and where they run; with an env,
                -run its criteria. --shard runs the plain stack's group i of N:
                the tickets in test/acceptance, sorted by number and dealt
                round robin, so the E2E Nightly runs the suite in N jobs at once
                (#2218). --tickets n,n runs those tickets' plain-stack criteria,
                which is how CI runs the criteria a pull request changes (#2341).
  vars <env>    the dev stack's variables for env, one NAME=value per line.
  services <env>  the services the run starts beside the stack, one per line.
  nightly       the environments the E2E Nightly runs, one per line.

An unknown environment is an error naming the known ones.
"""

from __future__ import annotations

import argparse
import re
import shlex
import sys
from dataclasses import dataclass, field
from pathlib import Path

MANIFEST = Path(__file__).resolve().parent.parent / "test" / "acceptance" / "environments.txt"
DEV = "dev"
CRITERION = re.compile(r"^func TestIssue(\d+)_", re.MULTILINE)


@dataclass
class Environment:
    name: str
    pattern: str
    variables: list[str] = field(default_factory=list)
    services: list[str] = field(default_factory=list)
    manual: bool = False


def load(path: Path = MANIFEST) -> list[Environment]:
    envs: list[Environment] = []
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        tokens = line.split()
        if len(tokens) < 2:
            raise ValueError(f"{path.name}:{number}: an environment needs a name and a -run pattern")
        env = Environment(name=tokens[0], pattern=tokens[1])
        for token in tokens[2:]:
            if token == "manual":
                env.manual = True
            elif token.startswith("+"):
                env.services.append(token[1:])
            elif "=" in token:
                env.variables.append(token)
            else:
                raise ValueError(f"{path.name}:{number}: {token!r} is neither NAME=value, +service nor manual")
        if env.name == DEV or any(e.name == env.name for e in envs):
            raise ValueError(f"{path.name}:{number}: environment {env.name!r} is reserved or named twice")
        envs.append(env)
    return envs


def find(envs: list[Environment], name: str) -> Environment:
    for env in envs:
        if env.name == name:
            return env
    known = ", ".join([DEV] + [e.name for e in envs])
    raise SystemExit(f"acceptance: no environment {name!r} in {MANIFEST.name}; the environments are {known}")


def flags(envs: list[Environment], name: str) -> str:
    if name in ("", DEV):
        if not envs:
            return ""
        for env in envs:
            where = "they run by hand" if env.manual else "the E2E Nightly runs them"
            print(f"acceptance: left out {env.pattern}, which needs the {env.name} environment "
                  f"(ACCEPTANCE_ENV={env.name}; {where})", file=sys.stderr)
        return "-skip " + shlex.quote("|".join(e.pattern for e in envs))
    return "-run " + shlex.quote(find(envs, name).pattern)


def tickets(directory: Path) -> list[int]:
    """The ticket numbers the acceptance files under directory hold criteria for."""
    found: set[int] = set()
    for path in directory.glob("*_test.go"):
        found.update(int(n) for n in CRITERION.findall(path.read_text()))
    return sorted(found)


def shard(spec: str, numbers: list[int]) -> list[int]:
    """The tickets group i of N runs, from a spec written i/N."""
    match = re.fullmatch(r"(\d+)/(\d+)", spec)
    if not match or not 1 <= int(match.group(1)) <= int(match.group(2)):
        raise SystemExit(f"acceptance: shard {spec!r} is not i/N with 1 <= i <= N")
    index, count = int(match.group(1)) - 1, int(match.group(2))
    return numbers[index::count]


def group_flags(envs: list[Environment], group: list[int]) -> str:
    """The plain stack's flags limited to the criteria of the tickets in group."""
    run = "-run " + shlex.quote("^TestIssue(" + "|".join(str(n) for n in group) + ")_")
    skip = flags(envs, DEV)
    return f"{run} {skip}".strip()


def shard_flags(envs: list[Environment], spec: str) -> str:
    """The plain stack's flags limited to one group of tickets."""
    group = shard(spec, tickets(MANIFEST.parent))
    if not group:
        raise SystemExit(f"acceptance: shard {spec} holds no ticket")
    return group_flags(envs, group)


def ticket_flags(envs: list[Environment], spec: str) -> str:
    """The plain stack's flags limited to the tickets a spec written n,n names."""
    if not re.fullmatch(r"\d+(,\d+)*", spec):
        raise SystemExit(f"acceptance: tickets {spec!r} is not n,n,...")
    return group_flags(envs, sorted({int(n) for n in spec.split(",")}))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    flags_cmd = sub.add_parser("flags")
    flags_cmd.add_argument("env", nargs="?", default="")
    flags_cmd.add_argument("--shard", default="")
    flags_cmd.add_argument("--tickets", default="")
    sub.add_parser("vars").add_argument("env")
    sub.add_parser("services").add_argument("env")
    sub.add_parser("nightly")
    args = parser.parse_args(argv)
    envs = load()
    if args.command == "flags" and (args.shard or args.tickets) and args.env not in ("", DEV):
        raise SystemExit(f"acceptance: --shard and --tickets select the plain stack's criteria, not {args.env}'s")
    if args.command == "flags" and args.tickets:
        print(ticket_flags(envs, args.tickets))
    elif args.command == "flags" and args.shard:
        print(shard_flags(envs, args.shard))
    elif args.command == "flags":
        print(flags(envs, args.env))
    elif args.command == "nightly":
        print("\n".join([DEV] + [e.name for e in envs if not e.manual]))
    elif args.env in ("", DEV):
        pass
    elif args.command == "vars":
        print("\n".join(find(envs, args.env).variables))
    else:
        print("\n".join(find(envs, args.env).services))
    return 0


if __name__ == "__main__":
    sys.exit(main())
