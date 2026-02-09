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
