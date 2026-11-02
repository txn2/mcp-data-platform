---
description: The postgres connection kind. Read and write a PostgreSQL database through postgres_query and postgres_execute, with native parameter binding, transactions, typed results, and values kept out of audit, logs and spans.
---

# PostgreSQL Toolkit

The PostgreSQL toolkit (`kind: postgres`) connects the platform to a PostgreSQL database an operator names and lets agents read and write it. Trino is the platform's analytical engine and holds no transaction, row lock or enforced constraint; a postgres connection is an operational store, a system of record an agent or a managed script can build on.

Two tools serve every connection:

| Tool | What it does |
|------|--------------|
| `postgres_query` | Runs one statement in a `READ ONLY` transaction and returns its columns, with their PostgreSQL types, and its rows |
| `postgres_execute` | Runs one statement, or a `statements` list in one transaction that commits only when every statement succeeds, and returns each statement's command, rows affected and any `RETURNING` rows |

## Registering a connection

Register the database as a connection of kind `postgres`, in the portal under **Admin > Connections** or through `PUT /api/v1/admin/connection-instances/postgres/{name}`. In YAML:

```yaml
toolkits:
  postgres:
    enabled: true
    instances:
      orders:
        host: "db.internal.example.com"
        port: 5432
        database: "orders"
        user: "orders_app"
        password: "{{secret:orders-db-password}}"
        sslmode: "verify-full"
        tls_ca_bundle_pem: |
          -----BEGIN CERTIFICATE-----
          ...
        max_conns: 5
        statement_timeout: "30s"
        max_rows: 1000
        read_only: false
        description: "The order system"
    default: orders
```

| Key | Default | Meaning |
|-----|---------|---------|
| `host` | (required) | Server host name or address |
| `port` | `5432` | Server port |
| `database` | (required) | Database to connect to. The platform's own database is refused (below) |
| `user` | (required) | The database user every call on this connection runs as |
| `password` | | The user's password, or `{{secret:<name>}}` naming a [stored secret](api-gateway.md#a-secret-in-the-request) whose `allow_connections` lists the connection. Encrypted at rest and never returned by a read |
| `sslmode` | `verify-full` | `disable`, `require` (encrypts, checks nothing), `verify-ca` (checks the chain, not the name) or `verify-full` (checks the chain and that the certificate names `host`) |
| `tls_ca_bundle_pem` | | Certificates trusted beside the system roots |
| `mtls_client_cert_pem`, `mtls_client_key_pem` | | A client certificate and its key. The key is encrypted at rest and never returned by a read |
| `max_conns` | `5` | Pool size on each replica, at most 100 |
| `statement_timeout` | `30s` | Longest one statement may run, at most `10m`. A call's `timeout_seconds` may only lower it |
| `max_rows` | `1000` | Most rows one result returns, at most 100000 |
| `read_only` | `false` | Refuse `postgres_execute` on this connection for every persona |

A connection is reachable only by a persona whose `connections:` allow list names it, like every other connection kind. The driver is `jackc/pgx/v5`; the platform's own database stays on `lib/pq`.

**Admin > Connections > Test** opens a connection and reports the server version, the database and the user it reached. Each connection's state (`healthy`, `unreachable`, `auth_failed`) is the one its calls last found, and `list_connections` reports `read_only` beside it.

## Grants are the boundary

What a tool can do is what the connection's database user may do. `postgres_query` runs in a `READ ONLY` transaction that is always rolled back, which keeps an agent's read from writing by mistake, but the boundary is the user's grants: give a connection meant for reading a user that can only read.

A statement may set any session setting, so a statement author cannot be held to a setting by the platform. Session state does not outlive a call: each connection is reset with `DISCARD ALL` as it returns to the pool, so a `SET ROLE`, a `SET search_path`, a `LISTEN` or a temporary table one call leaves is gone before the next call uses the connection.

`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT` and `PREPARE TRANSACTION` are refused: the tools hold the transaction, and such a statement would commit part of a `statements` list. A string holding two statements is refused by the server, since the extended protocol carries one.

## The platform's own database is never a target

A connection that reaches the platform's own database is refused. An agent with `postgres_execute` there could rewrite personas, grants and the audit log.

Comparing host, port and database name would be defeated by a DNS alias, an address in place of a name, or a pooler in front of the same server, so the check asks the database. The platform takes a random advisory lock on its own database, asks the new connection whether `pg_locks` shows that lock in the connection's own database, and releases it. The check runs when a connection is saved, if its database answers, and again on every physical connection a pool opens, so a connection whose host name is later pointed at the platform's database is refused at its first statement. A streaming replica does not show the primary's advisory locks; do not connect to a replica of the platform's database.

## Parameters

Values are bound as parameters and never written into the SQL text. `params` is a list bound to `$1`, `$2`, ... in order, or an object bound to `@name` placeholders, in which a placeholder the object does not name, or a name no placeholder uses, is refused rather than bound as NULL:

```json
{"connection": "orders", "sql": "SELECT * FROM orders WHERE customer_id = $1 AND placed_at >= $2", "params": [42, "2026-01-01T00:00:00Z"]}
{"connection": "orders", "sql": "SELECT * FROM orders WHERE customer_id = @customer", "params": {"customer": 42}}
```

The platform describes the statement first, which gives each parameter's type, and sends each value as text for the server to read by that type: a number or a string is parsed as the parameter's type (send a `numeric` as a string to keep every digit), `json` and `jsonb` take any value, an array type takes a list (or a string already written as an array literal), `bytea` takes base64, and `null` is NULL.

A `statements` list is the atomic write:

```json
{"connection": "orders", "statements": [
  {"sql": "INSERT INTO customers (email, name) VALUES ($1, $2) RETURNING id", "params": ["ada@example.com", "Ada"]},
  {"sql": "INSERT INTO orders (customer_id, total, placed_at) VALUES (currval('customers_id_seq'), @total, now())", "params": {"total": "19.90"}}
]}
```

A failure in any statement rolls back the others, and the result names the statement that failed by its index.

## Results

Each result carries its columns, each with its name and the type PostgreSQL gave it (`integer`, `numeric`, `timestamp with time zone`, `text[]`). Values map by that type:

| PostgreSQL | JSON |
|------------|------|
| integers, `real`, `double precision` | number; an integer beyond 2^53 - 1 either way is a decimal string, since a JSON reader that decodes numbers as doubles would change it; `NaN` and infinities are their PostgreSQL spelling |
| `numeric` | decimal string, every digit kept |
| `timestamptz` | RFC 3339 in UTC |
| `timestamp` | `YYYY-MM-DDTHH:MM:SS[.ffffff]` |
| `date` | `YYYY-MM-DD` |
| `json`, `jsonb` | the decoded document |
| `uuid` | canonical string |
| arrays | lists, each element mapped by the element type |
| `bytea` | base64 |
| anything else (`interval`, `inet`, an enum, a range) | the text PostgreSQL writes for it |

`postgres_query` returns at most `limit` rows (the connection's `max_rows` by default) and says `truncated` when more were there; it stops the statement at the server once it has one row past the cap. `postgres_execute` returns at most `max_rows` `RETURNING` rows per statement.

## Errors

A statement PostgreSQL refuses is answered with its SQLSTATE, condition name, and the constraint, table, column and type it named, plus the server's message, `DETAIL` and `HINT`, in the result's `postgres` object and a second text block.

The message and `DETAIL` can repeat a value (`Key (email)=(ada@example.com) already exists`, `invalid input syntax for type integer: "abc"`), so they go to the caller alone. The result's first text block, which the audit row's error text, the logs and the reflexive capture read, carries only the SQLSTATE and the names of the objects involved.

## What is recorded

| Sink | What it keeps |
|------|---------------|
| Audit row (`audit_logs.parameters`) | The SQL text, and each parameter's name (`$1`, `email`) and JSON type, never its value |
| Call catalog | The SQL text (a `statements` list as its statements in order) |
| Spans | `postgres.<operation>` per statement with the operation, a table summary, the database and the connection; the SQL text only under `OTEL_TRACES_INCLUDE_DB_STATEMENT=true`; a failure's SQLSTATE, never its message |
| Metrics | `postgres_statements_total{connection,operation,status}`, `postgres_statement_duration_seconds{connection,operation}`, `postgres_slow_statements_total{connection,operation}`; `status` is `ok`, `canceled`, `error` or the SQLSTATE class (`class_23`) |

A value bound from a decrypted form field (`platform.decrypt`) is the case the audit rule protects: the statement is recorded, the value never is.

Both tools are gated by the [`purpose` argument](tools.md#the-purpose-argument). The search-first gate and the schema gate apply to them once the catalog scans PostgreSQL connections (#2401).
