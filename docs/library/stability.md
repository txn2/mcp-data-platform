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

The set is bounded rather than open-ended. A build gate
(`TestPublicSurfacePolicy`) fails when a package is added under `pkg/` that is
outside the table above and has a single first-party importer, since that shape
is an implementation seam rather than an integration point and belongs under
`internal/`. What remains under `pkg/` outside the supported table is a short,
justified list: reference implementations of the interfaces the supported
surface exposes — the Postgres stores and the Trino and S3 provider adapters you
pass to `platform.WithSessionStore`, `WithQueryProvider`, `WithStorageProvider`
and their siblings — plus `pkg/admin`, whose router you can mount on your own
server, and `pkg/database/migrate`, which carries the embedded schema
migrations you run against your own database.

## Facade-internal packages are not importable

The facade's private implementation seams (middleware assembly, field
encryption, the IAM resolver, session synchronization, the prompt and memory
layers, the background index consumers, MCP Apps support, and similar) live
under `internal/platform/`. The HTTP adapters the server mounts — the gateway
REST shim, the prompt attachment, version and mention endpoints, the DataHub
catalog API, the MCP transport auth gate, the persona access gate and the health
handlers — live under `internal/httpserver/`, and the portal's own seams under
`internal/portal/`: its domain types and store contracts, its PostgreSQL and
no-database stores, its authorization core, its feedback surface (threads,
activity, worklists, sign-off, validation), and its public-viewer templates,
rate limiter and share cache. Every name the portal's domain move touched is
aliased back in `pkg/portal`, so `portal.Asset`, `portal.Collection`,
`portal.User` and the store constructors are spelled exactly as before. Go's
`internal/` rule makes all of them unimportable from outside this module. They
were never a supported integration surface; the location now enforces that so
their evolution cannot break an external build.

If you were importing one of these while it still lived under `pkg/`, the
package moved but its API did not: the type and function names are unchanged,
and the functionality is reachable through the supported surface, which
assembles and mounts all of them. The move is called out in the release notes
for the version that introduces it. Open an issue if a specific one has no
equivalent for your use case.

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
