# Session Externalization

Externalize MCP session state to PostgreSQL so server restarts do not invalidate active client sessions. Clients continue working transparently after a rolling deployment with no manual reconnection.

## Problem

When the MCP server restarts, all active client sessions are destroyed. The Go MCP SDK's Streamable HTTP transport manages sessions in-process memory. Clients hold stale `Mcp-Session-Id` references and every subsequent tool call fails until the user manually disconnects and reconnects.

## Solution

The platform supports two session store backends:

| Store | Use Case | Survives Restart | Multi-Replica |
|-------|----------|------------------|---------------|
| `memory` (default) | Development, single instance | No | No |
| `database` | Production, zero-downtime deploys | Yes | Yes |

### Memory Store (Default)

No configuration needed. The SDK manages sessions internally. Identical behavior to previous versions.

### Database Store

```yaml
database:
  dsn: "${DATABASE_URL}"

sessions:
  store: database
  ttl: 30m
  cleanup_interval: 1m
```

When `store: database` is set:

1. The platform creates a `sessions` table in PostgreSQL (via automatic migration)
2. Forces `server.streamable.stateless: true` on the SDK
3. Wraps the Streamable HTTP handler with a `SessionAwareHandler` that validates sessions against the database
4. On shutdown, flushes enrichment dedup state to the session store
5. On startup, restores enrichment dedup state from persisted sessions

Clients see no change. The `Mcp-Session-Id` header works transparently.

Session termination is served by the same handler: `DELETE /` with an `Mcp-Session-Id` removes the row and answers `204 No Content`, and a `DELETE` without that header is a `400`. The request is not forwarded to the SDK, whose stateless handler serves `POST` only and would answer `405 Method Not Allowed` for a termination that had already succeeded.

## Configuration

```yaml
sessions:
  store: memory               # "memory" (default) or "database"
  ttl: 30m                    # session lifetime (defaults to streamable.session_timeout)
  cleanup_interval: 1m        # cleanup routine frequency
```

See [Configuration](../server/configuration.md#session-configuration) for full parameter details.

## Kubernetes Rolling Update

For zero-downtime deploys with database session store:

```yaml
apiVersion: apps/v1
kind: Deployment
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  template:
    spec:
      terminationGracePeriodSeconds: 60
```

Combined with the platform's shutdown sequence:

```yaml
server:
  shutdown:
    grace_period: 25s          # drain in-flight requests
    pre_shutdown_delay: 2s     # wait for LB deregistration

sessions:
  store: database
```

The shutdown sequence:

```
SIGTERM received
  -> health check returns 503 (readiness probe fails)
  -> sleep(pre_shutdown_delay) for LB deregistration
  -> drain HTTP connections (grace_period)
  -> flush enrichment dedup state to session store
  -> close session store cleanup routine
  -> close remaining resources
  -> exit 0
```

New pod starts, loads persisted sessions from PostgreSQL, and accepts traffic. Clients with existing `Mcp-Session-Id` headers continue without interruption.

## Multi-Replica Deployment

With `store: database`, multiple replicas share the same session store. Any replica can serve any client because sessions are validated against PostgreSQL on every request.

```
           LB
          / | \
    Pod-1  Pod-2  Pod-3
      \      |     /
       PostgreSQL
       (sessions table)
```

## Session Hijack Prevention

Each session records a hash of the authentication token used during creation. Subsequent requests are validated against this hash. If a different token is presented for an existing session, the platform returns HTTP 403.

Anonymous sessions (no authentication token) skip this check.

## Enrichment Dedup Continuity

The platform's semantic enrichment middleware tracks which metadata has been sent to each session (avoiding redundant context). With database sessions:

- **Shutdown**: dedup state is serialized to the session's `state` JSONB column
- **Startup**: dedup state is restored from persisted sessions into the in-memory cache

This means clients do not receive duplicate metadata blocks after a server restart.

## Server-Pushed Notifications

In stateless streamable HTTP mode (the production shape when `sessions.store: database`), the SDK refuses GET requests for SSE streaming and closes each session at end-of-request. Without intervention, downstream agents (Claude.ai, Claude Desktop) never receive `notifications/tools/list_changed` — when a gateway upstream re-authenticates, agents still show the old tool list until they disconnect and reconnect.

The platform handles this with a session broadcaster:

- **In-memory** for single-replica or no-DB deployments — direct fan-out across local SSE subscribers.
- **Postgres LISTEN/NOTIFY** (channel `mcp_notifications`) when a database is configured — every replica `LISTEN`s once at startup and re-publishes received events to its local SSE subscribers, so a notification fired on any replica reaches every connected client across the cluster.

The session-aware HTTP handler intercepts `GET /` requests with `Accept: text/event-stream` and a valid `Mcp-Session-Id`. It opens a long-lived response, subscribes to the broadcaster bound to the session, and streams every event as a JSON-RPC 2.0 notification:

```
data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}\n\n
```

A 25-second comment-frame heartbeat (`: keepalive\n\n`) keeps the stream alive through proxy idle timeouts.

The gateway toolkit publishes `tools/list_changed` (debounced 50 ms — a longer window than the MCP SDK's own 10 ms internal debounce, chosen to absorb the postgres LISTEN/NOTIFY round-trip cost across replicas) after every aggregate tool-inventory change that registers or removes at least one tool: a connection coming up after re-auth, a connection being removed, the SetTokenStore retry promoting a placeholder. Connections that resolve to zero tools (placeholder upstreams that never finished discovery, removals on connections with no live tools) are silently skipped to keep the notification budget proportional to operator-visible inventory state. Wiring is automatic via `Platform.WireGatewayBroadcaster`, mirroring `WireGatewayTokenStore`.

The same broadcaster carries `prompts/list_changed` and `resources/list_changed`, so runtime-mutable prompts and managed resources reach connected clients without a reconnect:

- **`notifications/prompts/list_changed`** fires on every prompt store write (create, update, delete, approve, and scope promotion) across all write paths (the `manage_prompt` tool, the admin and portal REST handlers, and the knowledge `add_prompt` change type). Emission is a property of the store itself (a notifying wrapper over the shared prompt store), so no write path can change the prompt set without notifying. The advertised `prompts.listChanged` capability is therefore honest.
- **`notifications/resources/list_changed`** fires when a managed resource is created or deleted through the admin REST API. Startup registration of existing resources does not notify (no clients are connected yet).

Both are debounced 50 ms like the gateway signal, so a bulk import or multi-resource upload collapses to a single notification, and both fan out cross-replica through the same `mcp_notifications` channel.

**`resources/subscribe` (per-resource `notifications/resources/updated`) is intentionally not implemented.** Per-resource subscriptions only pay off when a client watches a specific document for content changes; no current client (Claude.ai, Claude Desktop) subscribes to individual managed resources, and the coarse `resources/list_changed` signal already prompts a re-list that surfaces additions and removals. The capability flag is left unset so the advertised contract matches what the server emits. If a future client needs per-URI update notifications, the SDK supports the flag and the resource store's update path can key notifications by URI.

### A session resumed on another build

A client keeps the tool list it read and validates results against it. After a deploy it reconnects to the session it had, on a process running a new build whose tools may differ, and nothing above tells it so: the SDK sends `notifications/tools/list_changed` only when a running server adds or removes a tool. So each session records, in its stored state (`tools_build`), the build whose tool list it was last told about; a new session records the build it was created on. When a request, or the session's reopened event stream, arrives on a process whose build differs, the platform sends `notifications/tools/list_changed` to that session alone, once, and records the running build (#1946). A session that holds no build (one created before a release recorded builds, or one revived after its row expired) is told once too, since it may hold a list from any build. An announcement made from a request, with the event stream perhaps not yet reconnected, is sent again when the session's stream next opens.

An event addressed to one session carries its id (`session_id` in the `mcp_notifications` payload); every replica receives it, and only the stream of that session writes it. A client on protocol revision 2025-11-25 or earlier initializes and keeps its `Mcp-Session-Id`, and is told on its `GET` stream; a client on 2026-07-28 is told on its listen stream, as described below. During a rolling update a session moving between replicas on different builds is told on each move; each told client re-lists once.

### Clients on protocol revision 2026-07-28

A client on protocol revision 2026-07-28 (SEP-2575) opens no `GET` stream. When it registers a list-changed handler it sends one `subscriptions/listen` request, and the response to that request is the stream its `tools/list_changed`, `prompts/list_changed` and `resources/list_changed` notifications arrive on, each stamped with the listen's subscription id. That stream is held by the replica that received the request (#1967).

Each replica records the listen streams it holds when the SDK acknowledges them (`notifications/subscriptions/acknowledged`, which names the notification types the server agreed to), and writes every event it receives on the `mcp_notifications` channel to the streams that agreed to that type, so a prompt saved or a managed resource created on any replica reaches every listening client. The record is dropped when the listen request ends.

A gateway connection's tools reach a listening client differently. Every replica applies a gateway connection change to its own MCP server (the replica that handled the write directly, the others through the reload bus), and the SDK tells that replica's listen streams when its server adds or removes a tool. The `tools/list_changed` the gateway toolkit also publishes on `mcp_notifications` is therefore not written to listen streams, where it would repeat what the SDK already sent once per replica that applied the change. A `tools/list_changed` addressed to one session (the notice below) is written.

A listening client that sends a `Mcp-Session-Id` the platform issued (the go-sdk client resends the one it was given on `server/discover`) is told once when its listen opens, and the session's recorded build is not the running one, the same notice a session's reopened `GET` stream carries. A client that connects again gets a new session on the running build, so it has nothing to be told.

A listen stream ends when the replica holding it stops. The go-sdk client (v1.8.0) does not open another: a 2026-07-28 listen carries no event ids to resume from, and the client ends its handling of a request stream that closes without one. Such a client is told nothing further until it connects again; the platform has no stream to write to in the meantime.

If the postgres broadcaster fails to start (e.g. the database role lacks `LISTEN` privilege), the platform falls back to in-memory and continues: `tools/list_changed`, `prompts/list_changed`, and `resources/list_changed` propagation degrades to single-replica scope rather than blocking platform startup.

When multiple deployments share a single postgres instance, set `sessions.broadcast_channel` to a deployment-unique value so each deployment's `LISTEN/NOTIFY` traffic stays isolated. The default channel name is `mcp_notifications`.
