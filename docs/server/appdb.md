---
description: Application databases (kind appdb). An application's own PostgreSQL database, read and written by the scripts and endpoints an application is built from, with native parameter binding, typed results, and values kept out of audit, logs and spans.
---

# Application Databases

An application database (`kind: appdb`) is an application's own PostgreSQL database: the system of record for an application built in the platform, such as an order system or a CRM. The scripts and endpoints the application is made of read and write it. Trino is the platform's analytical engine and holds no transaction, row lock or enforced constraint; an application database holds the rows an application commits.

An application database is not a data source. It registers no agent tool and never appears in `search` or the catalog. Agents reach remote software through [API connections](api-gateway.md) and connected data sources through Trino. An agent builds an application by writing [managed scripts](../scripts/running.md) and endpoints, and those reach the application database through `platform.query` and `platform.execute`.

To report on application data, an operator adds a Trino catalog (the `postgresql` connector) over the same database. It is then a connected data source like any other.

## Registering a connection

Register the database as a connection of kind `appdb`, in the portal under **Admin > Connections** (shown as "Application database") or through `PUT /api/v1/admin/connection-instances/appdb/{name}`. In YAML:

```yaml
toolkits:
  appdb:
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
| `user` | (required) | The database user every statement on this connection runs as |
| `password` | | The user's password, or `{{secret:<name>}}` naming a [stored secret](api-gateway.md#a-secret-in-the-request) whose `allow_connections` lists the connection. Encrypted at rest and never returned by a read |
| `sslmode` | `verify-full` | `disable`, `require` (encrypts, checks nothing), `verify-ca` (checks the chain, not the name) or `verify-full` (checks the chain and that the certificate names `host`) |
| `tls_ca_bundle_pem` | | Certificates trusted beside the system roots |
| `mtls_client_cert_pem`, `mtls_client_key_pem` | | A client certificate and its key. The key is encrypted at rest and never returned by a read |
| `max_conns` | `5` | Pool size on each replica, at most 100 |
| `statement_timeout` | `30s` | Longest one statement may run, at most `10m`. A statement's `timeout_seconds` may only lower it |
| `max_rows` | `1000` | Most rows one result returns, at most 100000 |
| `read_only` | `false` | Refuse `platform.execute` on this connection for every script |

An application database cannot take a name a Trino connection holds, or the reverse: `platform.query` names its connection alone, and the save is refused with 409.

A script uses a connection only when the persona its run presents allows the connection in its `connections:` list. The check runs once per run, at the run's first statement on that connection. The driver is `jackc/pgx/v5`; the platform's own database stays on `lib/pq`.

**Admin > Connections > Test** opens a connection and reports the server version, the database and the user it reached. Each connection's state (`healthy`, `unreachable`, `auth_failed`) is the one its statements last found.

## From a script

`platform.query` and `platform.execute` take an application database's name as `connection`. Placeholders are `:name`, as for every connection; on an application database each is sent as a bound parameter and never written into the SQL text:

```python
def main():
    rows = platform.query(
        "SELECT id, total FROM orders WHERE customer_id = :customer AND status = ANY(:statuses)",
        connection="orders",
        params={"customer": 42, "statuses": ["open", "paid"]},
    )["rows"]
    platform.execute(
        "UPDATE orders SET status = 'shipped' WHERE id = :id",
        connection="orders",
        params={"id": rows[0]["id"]},
    )
```

- `platform.query` runs one statement in a `READ ONLY` transaction that is always rolled back. A statement that writes fails there.
- `platform.execute` runs one statement and commits it in a transaction of its own.
- A list is bound as one array parameter: write `= ANY(:ids)`, not `IN :ids`.
- Each value is read by the type PostgreSQL gives its parameter: a number or a string is parsed as that type (pass a `numeric` as a string to keep every digit), `json` and `jsonb` take any value, an array type takes a list, `bytea` takes base64, and `None` is NULL.
- A dollar-quoted string (`$$ ... $$`), a string literal, a quoted identifier and a comment are left as written.

The statements do not cross the MCP server: the script host calls the connection's pool directly, so a request an endpoint answers pays no tool call, middleware pass or audit row per statement. In a draft run, `platform.execute` on an application database is a write and is refused unless the draft allows writes, like any other write. A recording keeps each statement and its answer, and a script test answers from the recording with the database stopped.

## Grants are the boundary

What a script can do is what the connection's database user may do. `platform.query`'s `READ ONLY` transaction keeps a read from writing by mistake, but the boundary is the user's grants: give a connection meant for reading a user that can only read.

Session state does not outlive a statement: each connection is reset with `DISCARD ALL` as it returns to the pool, so a `SET ROLE`, a `SET search_path`, a `LISTEN` or a temporary table one statement leaves is gone before the next uses the connection.

`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT` and `PREPARE TRANSACTION` are refused: the platform holds the transaction. A string holding two statements is refused by the server, since the extended protocol carries one.

## The platform's own database is never a target

A connection that reaches the platform's own database is refused. A script with `platform.execute` there could rewrite personas, grants and the audit log.

Comparing host, port and database name would be defeated by a DNS alias, an address in place of a name, or a pooler in front of the same server, so the check asks the database. The platform takes a random advisory lock on its own database, asks the new connection whether `pg_locks` shows that lock in the connection's own database, and releases it. The check runs when a connection is saved, if its database answers, and again on every physical connection a pool opens, so a connection whose host name is later pointed at the platform's database is refused at its first statement. A streaming replica does not show the primary's advisory locks; do not connect to a replica of the platform's database.

## Results

`res["columns"]` lists each column's name and the type PostgreSQL gave it (`integer`, `numeric`, `timestamp with time zone`, `text[]`), and `res["rows"]` holds one dict per row in column order. Values map by type:

| PostgreSQL | Script value |
|------------|--------------|
| integers, `real`, `double precision` | number; an integer beyond 2^53 - 1 either way is a decimal string; `NaN` and infinities are their PostgreSQL spelling |
| `numeric` | decimal string, every digit kept |
| `timestamptz` | RFC 3339 in UTC |
| `timestamp` | `YYYY-MM-DDTHH:MM:SS[.ffffff]` |
| `date` | `YYYY-MM-DD` |
| `json`, `jsonb` | the decoded document |
| `uuid` | canonical string |
| arrays | lists, each element mapped by the element type |
| `bytea` | base64 |
| anything else (`interval`, `inet`, an enum, a range) | the text PostgreSQL writes for it |

A query returns at most the run's row cap and the connection's `max_rows`; a result cut at either fails the run, as for every query a script makes, since a partial result would silently change what it computes.

## Errors

A statement PostgreSQL refuses fails with its SQLSTATE, condition name, and the constraint, table, column and type it named. That sentence is what the run's error, its audit record and the logs carry. The server's message, `DETAIL` and `HINT` can repeat a value (`Key (email)=(ada@example.com) already exists`), so they go to the script alone: with `on_error="return"` the failure is returned to the script, and its `postgres` field holds them.

## What is recorded

| Sink | What it keeps |
|------|---------------|
| Audit | The run's own `script_run` event; no row per statement |
| Spans | `postgres.<operation>` per statement with the operation, a table summary, the database and the connection; the SQL text only under `OTEL_TRACES_INCLUDE_DB_STATEMENT=true`; a failure's SQLSTATE, never its message |
| Metrics | `postgres_statements_total{connection,operation,status}`, `postgres_statement_duration_seconds{connection,operation}`, `postgres_slow_statements_total{connection,operation}`; `status` is `ok`, `canceled`, `error` or the SQLSTATE class (`class_23`) |
| Recordings | Each statement, its bound values and its answer, which replay and script tests need |

A bound value reaches no audit row, log line, metric label or span.
