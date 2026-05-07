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

## Configuration

```yaml
sessions:
  store: memory               # "memory" (default) or "database"
  ttl: 30m                    # session lifetime (defaults to streamable.session_timeout)
  idle_timeout: 30m           # idle session eviction (defaults to streamable.session_timeout)
  cleanup_interval: 1m        # cleanup routine frequency
```

See [Configuration Reference](../reference/configuration.md#session-externalization) for full parameter details.

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

If the postgres broadcaster fails to start (e.g. the database role lacks `LISTEN` privilege), the platform falls back to in-memory and continues — `tools/list_changed` propagation degrades to single-replica scope rather than blocking platform startup.

When multiple deployments share a single postgres instance, set `sessions.broadcast_channel` to a deployment-unique value so each deployment's `LISTEN/NOTIFY` traffic stays isolated. The default channel name is `mcp_notifications`.
