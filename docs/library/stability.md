---
description: What the Go module promises across minor releases. The supported import surface, the change policy for everything else under pkg/, and the config-file compatibility policy.
---

# API Stability

The module is published as `github.com/txn2/mcp-data-platform` at major version 1
with no `/vN` suffix. In Go semantics that promises compatibility across
everything importable. This page narrows that promise to a surface the project
can actually keep, and states the policy for everything outside it.

## Supported import surface

These packages are the intended integration points for building a custom server
on top of the platform. Breaking changes to their exported identifiers are made
only in a major release.

| Package | What you import it for |
|---------|------------------------|
| `pkg/platform` | The facade: construct, configure, and run the platform (options and lifecycle) |
| `pkg/toolkit` | Shared types every toolkit implements |
| `pkg/registry` | Register and manage toolkits |
| `pkg/semantic` | Semantic provider interface (swap the semantic layer) |
| `pkg/query` | Query execution provider interface (swap the query engine) |
| `pkg/middleware` | Request/response middleware contracts |
| `pkg/toolkits/*` | The toolkit adapters' exported config types (Trino, DataHub, S3, and the others) |

## Everything else under `pkg/`

Other exported packages under `pkg/` are importable and you may build against
them, but they are implementation packages rather than a committed integration
surface. Their exported API may change in a minor release. When it does, the
change is called out in the release notes for that version, the same way a
breaking configuration change is (see below).

If you depend on one of these packages and want it promoted to the supported
surface, open an issue describing the use case. Pinning a specific version in
your `go.mod` is the reliable way to insulate a build from these changes.

## Facade-internal packages are not importable

The facade's private implementation seams (middleware assembly, field
encryption, the IAM resolver, session synchronization, the prompt and memory
layers, and similar) live under `internal/platform/`. Go's `internal/` rule
makes them unimportable from outside this module. They were never a supported
integration surface; the location now enforces that so their evolution cannot
break an external build.

## Configuration-file compatibility

Configuration keys follow a separate, more conservative policy because a running
deployment depends on its config file across upgrades. Config changes are
handled as follows:

- **Additive changes** (new optional keys, new default-on behavior behind a
  `*bool`) ship in minor releases and require no action.
- **Breaking renames or removals** are called out in the release notes for the
  version that introduces them, with an admonition describing the old key, the
  new key, and the runtime effect of upgrading without changing the file.

!!! note "Precedent"
    `workflow.require_search` replaced the former
    `workflow.require_discovery_before_query` as a hard, non-aliased rename, and
    the release notes documented the behavior change (a deployment that never
    configured workflow gating begins refusing `trino_query`/`trino_execute`
    until `search` is called once per session). That is the pattern every
    breaking configuration change follows.

Setting `config.strict: true` turns an unknown or stale key into a hard startup
error instead of a silent no-op, which surfaces a rename you have not yet
accounted for at the earliest possible point.
