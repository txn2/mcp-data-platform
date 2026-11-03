---
description: Application databases (kind appdb). An application's own PostgreSQL database, read and written by the scripts and endpoints an application is built from, with transactions, batches, a statement cache, versioned schema migrations, native parameter binding, typed results, and values kept out of audit, logs and spans.
---

# Application Databases

An application database (`kind: appdb`) is an application's own PostgreSQL database: the system of record for an application built in the platform, such as an order system or a CRM. The scripts and endpoints the application is made of read and write it. Trino is the platform's analytical engine and holds no transaction, row lock or enforced constraint; an application database holds the rows an application commits.

An application database is not a data source. Its tables never appear in `search` or the catalog, and its data is reached by no agent tool. Agents reach remote software through [API connections](api-gateway.md) and connected data sources through Trino. An agent builds an application by writing its schema as [migrations](#schema-migrations) with `manage_schema`, the one tool this kind registers, and by writing [managed scripts](../scripts/running.md) and endpoints, which reach the application database through `platform.query`, `platform.execute`, `platform.transaction` and `platform.batch`.

To report on application data, an operator adds a Trino catalog (the `postgresql` connector) over the same database. It is then a connected data source like any other.

## Registering a connection

The server must be PostgreSQL 17 or later. A connection to an older server is refused when it is saved, when it is tested and on every physical connection its pool opens, with a message naming the version found and the floor, so a connection configured in YAML is held to it as one saved through the admin API is. A connection's `test_connection` is an application database too and is held to the same floor.

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
        statement_cache: true
        pool_wait_timeout: "5s"
        transaction_idle_timeout: "10s"
        schema_apply_personas: []
        schema_ledger: "mcp_schema"
        test_connection: ""          # this connection; its user needs CREATEDB
        persona_roles:               # a persona's statements log in as its own role
          sales: "orders_sales"
        persona_role_passwords:
          sales: "{{secret:orders-sales-password}}"
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
| `statement_cache` | `true` | Keep each statement prepared on the physical connection that first ran it, so it is described once. Set `false` behind PgBouncer in transaction mode |
| `pool_wait_timeout` | `5s` | Longest a statement waits for a free pooled connection, at most `1m`. Past it the statement fails naming the pool and its `max_conns` |
| `transaction_idle_timeout` | `10s` | Longest a script's transaction may go without a statement, at most `10m`. Past it the transaction is rolled back |
| `schema_apply_personas` | `[]` | Personas that may apply and revert this connection's migrations. Empty means administrators only |
| `schema_ledger` | `mcp_schema` | Schema in this database that holds the ledger of applied migrations |
| `test_connection` | this connection | Application database connection whose server the throwaway databases of this connection's migration checks and script tests are created on. Its user needs `CREATEDB` |
| `persona_roles` | `{}` | Maps a persona to the database login role its statements run as. A persona not named runs as `user`. See [Persona roles](#persona-roles) |
| `persona_role_passwords` | `{}` | Maps a persona named in `persona_roles` to its role's password, or `{{secret:<name>}}`. Encrypted at rest and never returned by a read, the config export, an audit row or a log |

An application database cannot take a name a Trino connection holds, or the reverse: `platform.query` names its connection alone, and the save is refused with 409.

A script uses a connection only when the persona its run presents allows the connection in its `connections:` list. The check runs once per run, at the run's first statement on that connection. The driver is `jackc/pgx/v5`; the platform's own database stays on `lib/pq`.

**Admin > Connections > Test** opens a connection and reports the server version, the database and the user it reached, logs in as every persona role and names each (warning of one that is a superuser or has `BYPASSRLS`, which row-level security does not apply to), and reports whether a notification sent through the pool reaches a listening connection, which a [webhook source with input postgres](webhooks.md#an-application-database-as-a-source) needs. Through PgBouncer in transaction mode it does not. Each connection's state (`healthy`, `unreachable`, `auth_failed`) is the one its statements last found; a persona role's failed login is reported under the connection's name.

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

## Transactions

`platform.transaction(connection, fn, isolation="read_committed", read_only=False, retry=False, on_error="raise")` opens one transaction, calls `fn(tx)`, and commits when `fn` returns. It returns what `fn` returned. When `fn` fails, the transaction is rolled back and the failure is the call's. Inside `fn`, `tx.query`, `tx.execute` and `tx.batch` bind values as `platform.query` does and run in the transaction:

```python
def main():
    order = {"id": 1001, "total": "59.70"}
    items = [{"sku": "A-1", "qty": 2}, {"sku": "B-7", "qty": 1}]

    def place(tx):
        tx.execute("INSERT INTO orders (id, total) VALUES (:id, :total)", params=order)
        tx.batch([
            {"sql": "INSERT INTO items (order_id, sku, qty) VALUES (:o, :s, :q)",
             "params": {"o": order["id"], "s": i["sku"], "q": i["qty"]}}
            for i in items
        ])
        return tx.query("SELECT count(*) AS n FROM items WHERE order_id = :o", params={"o": order["id"]})["rows"][0]["n"]

    platform.transaction("orders", place)
```

An order and its line items commit together or not at all: when a line item violates a constraint, nothing is left, and the run's error names the constraint.

- `isolation` is `read_committed`, `repeatable_read` or `serializable`. `read_only=True` opens a `READ ONLY` transaction, the only kind a `read_only` connection opens.
- The first statement that fails ends the transaction, as PostgreSQL does. Every later call on `tx` fails saying so.
- `retry=True` runs `fn` again when the transaction fails with a serialization failure (SQLSTATE `40001`) or a deadlock (`40P01`), and on nothing else: three tries in all, with a short jittered pause between them. Because `fn` may run more than once, a retried `fn` reads and writes only through `tx`: `platform.call`, `platform.query`, `platform.execute`, `platform.batch`, `platform.export`, `platform.publish_data`, `platform.notify`, `platform.publish`, `platform.save_state` and `platform.mint_token` are refused inside it.
- Transactions do not nest. While one is open, `platform.query` and `platform.execute` on its connection are refused, since they would run on another pooled connection and wait on the transaction's own locks.
- A `tx` is used only inside `fn`.

The transaction holds one pooled connection for its life, and is never held across MCP calls or by an agent. It is rolled back when the run ends however it ends (finished, failed, timed out, shed by admission, drained at shutdown), when it goes `transaction_idle_timeout` without a statement, and when the replica closes its pools; the next call on it names the reason. An endpoint handler may open one, and it does not outlive the request.

## Batches

`platform.batch(connection, statements, on_error="raise")` sends statements pipelined, in one round trip, in a transaction of their own that commits only when every one succeeds, and returns their results in order. `tx.batch(statements)` does the same inside a transaction. A statement is a SQL string or `{"sql": ..., "params": {...}}`, and none reads another's result. A batch is a write in a draft. A failure names the statement: `statement 2 of the batch: ...`.

## Statement cache and pool

With `statement_cache` on, each statement is prepared on the physical connection that first runs it and run as a prepared statement after that, which saves the round trip that describes it. Each physical connection keeps at most 256 statements, least recently used dropped first. When the platform applies a migration, every connection drops what it holds at its next use; a change made outside the platform makes the first call fail with SQLSTATE `0A000` and the next one prepare the statement again. Behind PgBouncer in transaction mode a session's statements land on different server connections, so set `statement_cache: false` there.

A statement that finds no free pooled connection within `pool_wait_timeout` fails naming the pool and its `max_conns`, rather than hanging the request.

## Grants are the boundary

What a script can do is what the connection's database user may do. `platform.query`'s `READ ONLY` transaction keeps a read from writing by mistake, but the boundary is the user's grants: give a connection meant for reading a user that can only read.

Session state does not outlive a call: each connection is reset as it returns to the pool, so a `SET ROLE`, a `SET search_path`, a `LISTEN` or a temporary table one statement leaves is gone before the next uses the connection. With `statement_cache: false` the reset is `DISCARD ALL`. With the cache on it is the sequence PostgreSQL documents `DISCARD ALL` as, less its `DEALLOCATE ALL`: `CLOSE ALL`, `SET SESSION AUTHORIZATION DEFAULT`, `RESET ALL`, `UNLISTEN *`, `pg_advisory_unlock_all()`, `DISCARD PLANS`, `DISCARD TEMP` and `DISCARD SEQUENCES`. The prepared statements it keeps are the platform's own.

## Persona roles

The platform authorizes every run's use of a connection by persona first. A connection can also make the database a second boundary: `persona_roles` maps a persona to a login role, and every statement a run of that persona makes logs in as that role, through a pool of its own on each replica, sized `max_conns` and opened on first use. The database's own grants and row-level security then apply to it. A persona the map does not name runs as the connection's `user`.

A run presents the persona of the script version's author, as it does for every call, so a script saved by someone in `sales` runs as `orders_sales`. An endpoint request runs its script the same way: as the author's persona, never the caller's.

The roles log in directly; the platform never switches to one with `SET ROLE`. A session can `RESET ROLE` or `SET ROLE` to any role the role it logged in as is a member of, so a switched role is no boundary against the person writing the SQL. A role that logged in has nothing to reset to. Do not make a persona role a member of a role with more privileges, a superuser, or a role with `BYPASSRLS`; the connection test warns of the last two.

A script test's throwaway database keeps the persona roles when the test server is the connection's own server, where the roles exist, so a test runs each statement as the role it would run as. On another test server every statement runs as the test server's user.

## Caller claims

Every transaction sets who it runs for as transaction-local settings, from bound values, before its first statement:

| Setting | Value |
|---------|-------|
| `mcp.user_id` | An authenticated endpoint's caller: the user id the platform authenticated them as. Outside an endpoint, the script version's author's, which is empty until the platform has seen the author sign in |
| `mcp.email` | The same person's address |
| `mcp.persona` | The persona the statement runs as (the author's) |
| `mcp.source` | `endpoint` for an endpoint request, `script` for a run |

On a public or bot-check endpoint `mcp.user_id` and `mcp.email` are empty. Every setting is set in every transaction, empty when absent, so a statement never reads a value an earlier caller's transaction on the same pooled connection set. A row-level security policy reads them with `current_setting('mcp.user_id', true)`, and treats the empty string as no one:

```sql
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_orders ON orders TO orders_sales
  USING (customer_id = nullif(current_setting('mcp.user_id', true), ''));
```

What a claim is worth depends on who wrote the SQL. The author of a script can set any setting in their own statement, so a claim protects against the person who did not write the SQL: an endpoint's caller, a form's submitter. Against the person writing the SQL the boundary is their persona's login role.

## Statements the platform refuses

`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT` and `PREPARE TRANSACTION` are refused: the platform holds the transaction, and `platform.transaction` is how statements commit together. `DEALLOCATE` and `DISCARD` are refused too. A string holding two statements is refused by the server, since the extended protocol carries one.

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

## Schema migrations

An application's tables are created and changed through versioned migrations the platform keeps, applies and reports, never by ad hoc DDL. A migration belongs to a connection and a schema (namespace, `public` by default): a version, a name, the SQL that applies it, optional SQL that reverts it, its author and a checksum. The platform keeps each migration and its history in its own database. The target database holds the ledger of what was applied in `mcp_schema.migrations` (the schema is `schema_ledger`): version, checksum, when, by whom and how long it took.

`manage_schema` takes a `command`, a `connection` (an application database) and an optional `schema`:

| Command | What it does |
|---------|--------------|
| `status` | What the ledger records as applied, what is pending, and any drift between the ledger and the platform's migrations |
| `add` | Adds a migration (`version`, `name`, `up`, optional `down`, `transactional`), or replaces one still pending. Its version must be above every applied version |
| `plan` | Runs the pending migrations (up to `to_version`) in a transaction, reports the relations and columns they change and the locks they take from `pg_locks`, and rolls back |
| `apply` | Applies the pending migrations (up to `to_version`) |
| `down` | Reverts the latest applied migration, or every one above `to_version`, with its down SQL |
| `history` | What happened to each migration: added, edited, applied, reverted, failed |
| `describe` | What the server is and what the applied migrations produced, read from the target's `pg_catalog`. This is how an agent reads its application's schema while writing scripts. See [Describe](#describe) |

```json
{"command": "add", "connection": "orders", "version": 1, "name": "orders",
 "up": "CREATE TABLE orders (id bigint PRIMARY KEY, total numeric(10,2) NOT NULL)",
 "down": "DROP TABLE orders"}
```

- An applied migration is immutable. Adding an applied version again is refused, and its checksum is compared with the ledger on every apply and status: a mismatch stops the apply and names the version. A fix is a new migration.
- `apply` holds an advisory lock in the target database for the whole apply, so two replicas or two callers never apply at once: the second waits up to 30 seconds and then finds nothing pending, or reports the lock. Each migration runs in a transaction of its own with `lock_timeout` (10 seconds) and `statement_timeout` (the connection's, or `timeout_seconds`) set and `search_path` set to its schema, and writes its ledger row in the same transaction. A migration that fails partway leaves no change and no ledger row, and stops the apply.
- A statement that cannot run in a transaction (`CREATE INDEX CONCURRENTLY`) is a migration of its own with `transactional: false`. It runs alone and is not planned.
- Adding and planning need the connection in the caller's persona and a connection that is not `read_only`. Applying and reverting need the admin persona or a persona the connection's `schema_apply_personas` names.
- The portal's connection page (**Admin > Connections**, an application database) lists the migrations, which are applied, the plan of the pending ones, and the history.

A migration's SQL is kept in the platform's database and in the target's ledger checksum. Do not write a secret into a migration.

### Describe

`describe` returns, for the migration's schema:

- `server`: the server's `major` (17, 18) and full `version`.
- `extensions`: the extensions installed in the database, with their versions and schemas, and `available_extensions`: the ones the server offers that the database has not installed, with their default versions.
- `tables`: each table, partitioned table, view and materialized view, with its columns (type, nullability, default, comment), constraints (primary key, unique, foreign key, check, exclusion), indexes, `row_security` and `force_row_security`, its `policies` (command, roles, permissive or restrictive, `using`, `with_check`), its `triggers` (timing, events, row or statement level, `when`, the function, enabled state, definition), a partitioned table's `partition_key` and `partitions` with their bounds, and a view's `view` (definition, `security_invoker`, `security_barrier`, check option). Nullability is reported per column on every major: PostgreSQL 18 also records `NOT NULL` as a constraint, and `describe` leaves those out of `constraints` so its answer is the same on 17 and 18.
- `functions`: each function and procedure, with its arguments, return type, language, volatility, `security_definer`, the `search_path` it pins, and its body.
- `enums` with their values in order, and `domains` with their base type, nullability, default and checks.

An object an extension created is left out, since no migration wrote it. An empty kind is `[]`. Function bodies, view definitions and policy expressions are returned to the caller and are not logged, traced, audited or indexed; they are the database's text, so a literal a migration wrote into one is returned as written.

### Review before a migration saves

`add` reviews a migration before it saves it (#2405), the way a script's source passes its save gate:

| Rule | Statement | Severity | Safe form |
|------|-----------|----------|-----------|
| `create_index_locks` | `CREATE INDEX` without `CONCURRENTLY` on a table an earlier migration created | warning | `CREATE INDEX CONCURRENTLY` in a migration of its own with `transactional: false` |
| `not_null_without_default` | `ADD COLUMN ... NOT NULL` with no default | warning | A constant `DEFAULT`, or nullable, backfilled, then `SET NOT NULL` |
| `volatile_default_rewrites` | `ADD COLUMN ... DEFAULT` a value computed per row (`random()`, `clock_timestamp()`, `gen_random_uuid()`, `nextval()`) | warning | No default or a constant one, then a batched backfill |
| `type_change_rewrites` | `ALTER COLUMN ... TYPE` | warning | A new column, backfilled, reads moved to it |
| `constraint_validates` | `ADD CONSTRAINT ... FOREIGN KEY` or `CHECK` without `NOT VALID` | warning | `NOT VALID`, then `VALIDATE CONSTRAINT` in a later migration |
| `lock_timeout_raised` | `SET lock_timeout` above the platform's 10 seconds, or 0 | warning | Leave it, and retry an apply that times out |
| `rename_referenced` | `RENAME` of a table or column a saved script names in a string (its SQL, or a row key) beside the connection's name | warning | The new name beside the old, the scripts moved, the old removed later |
| `drop_table`, `drop_column`, `truncate`, `unbounded_delete`, `unbounded_update` | `DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, a `DELETE` or `UPDATE` with no `WHERE` | destructive | |

The review also reads each migration for statements that leave the database open to a role that should not reach it, and for features the server's major does not have (#2449). The major, the schema's tables with row-level security and its virtual generated columns are read from the connection at `add`; when they cannot be read the migration is reviewed without the rules that need them and `add` returns a `target_unread` warning saying so.

| Rule | Statement | Severity | Safe form |
|------|-----------|----------|-----------|
| `security_definer_search_path` | A `SECURITY DEFINER` function or procedure without `SET search_path` ending in `pg_temp` (`FROM CURRENT` is not taken as pinned) | warning | `SET search_path = <its schemas>, pg_temp` on the function |
| `security_definer_public_execute` | A `SECURITY DEFINER` function with no `REVOKE ... FROM PUBLIC` covering it in the same migration (on the function, on `ALL FUNCTIONS IN SCHEMA`, or `ALTER DEFAULT PRIVILEGES` before it) | warning | `REVOKE EXECUTE ON FUNCTION <name> FROM PUBLIC`, then `GRANT` it to the roles that call it |
| `row_security_not_forced` | `ENABLE ROW LEVEL SECURITY` without `FORCE ROW LEVEL SECURITY` on the same table in the same migration: the owner, which migrations and connections without a persona role run as, passes every policy | warning | `FORCE ROW LEVEL SECURITY` in the same migration |
| `view_bypasses_row_security` | A view reading a table with row-level security (enabled on the server or in the migration) without `security_invoker = true`: the view reads as its owner | warning | `CREATE VIEW ... WITH (security_invoker = true)`, or `ALTER VIEW ... SET (security_invoker = true)` in the same migration |
| `index_on_virtual_column` | On PostgreSQL 18, an index on a generated column declared without `STORED`, which 18 makes virtual and cannot index | warning | `GENERATED ALWAYS AS (...) STORED` |
| `feature_needs_newer_server` | On PostgreSQL 17, a feature 18 introduced: `uuidv7()` or `uuidv4()`, `RETURNING old`/`new`, a virtual generated column, `WITHOUT OVERLAPS`, `NOT ENFORCED` | warning | The finding names the form 17 has |
| `copy_program` | `COPY ... FROM PROGRAM` or `TO PROGRAM` | refused | |
| `untrusted_code` | `CREATE LANGUAGE`, `CREATE EXTENSION` of an untrusted language, or a function, procedure or `DO` block in one (`plpython3u`, `plperlu`, `pltclu`, `C`, `internal`) | refused | |
| `event_trigger` | `CREATE EVENT TRIGGER` | refused | |
| `alter_system` | `ALTER SYSTEM` | refused | |

A refused statement stops the save and the refusal names each one: they need privileges an application's role must not hold, or reach outside the database. On a 17 server a statement using an 18 feature, and on 18 an index on a virtual generated column, also fail the apply from zero below; that refusal names what the review found.

A table the same migration creates is not a live table, and no rule reads a statement on it. Every finding names the statement, the object, what it does to a live table and the safe form, and is returned in `add`'s `findings` and recorded in the migration's history. A warning does not stop the save. A destructive finding does: the migration saves only with `change_summary` (what it removes, in plain words) and `user_agreed: true` once the person the application is built for agreed, and the summary and who agreed are in its history.

Then every migration of the schema, the new one included, is applied from zero on a throwaway database on the connection's test server. A set that does not apply is refused naming the migration and the error. A test server whose user lacks `CREATEDB` cannot run this check: the migration saves with a `from_zero_unchecked` warning naming the privilege. The database built is kept as the schema's template, so the next check or script test that needs the same migrations starts from it.

### The outbox template

`manage_schema add` with `template: "outbox"` writes a migration the platform provides in place of `up` and `down`: an outbox table in the migration's schema (`outbox_table`, default `outbox`), a trigger function that writes each inserted, updated or deleted row of a captured table to it as JSON and notifies the channel named `<schema>.<table>`, and a trigger on each table in `capture`. With `notify: false` the trigger function writes the row and sends no notification: a transaction that notifies takes a lock at commit that every other notifying commit waits on, which serializes a table many sessions write at once, and the source's poll every `poll_seconds` then delivers the rows on its own. It is reviewed, applied and reverted like any other migration. A [webhook source with input postgres](webhooks.md#an-application-database-as-a-source) in mode `outbox` then files each row as an event.

```json
{"command": "add", "connection": "orders", "version": 4, "template": "outbox", "capture": ["orders", "order_items"]}
```

| Column | Holds |
|--------|-------|
| `id` | The row's place, `bigint` identity |
| `source_table` | The captured table, as `schema.table` |
| `op` | `INSERT`, `UPDATE` or `DELETE` |
| `payload` | The new row, or the old one for a `DELETE`, as `jsonb` |
| `created_at` | When the transaction that made the change began |

### Script tests against a throwaway database

A script's test can run against a real database built from the connection's migrations (#2407) rather than answering its statements from a recording, so constraints, triggers, row-level security and concurrency are exercised:

```python
def test_a_duplicate_email_is_refused():
    testing.database("orders", schemas = ["app"])
    platform.execute("INSERT INTO app.users (email) VALUES (:e)", connection = "orders", params = {"e": "a@example.com"})
    msg = assert.fails(lambda: platform.execute("INSERT INTO app.users (email) VALUES (:e)",
                                                connection = "orders", params = {"e": "a@example.com"}))
    assert.contains(msg, "23505")
```

- `testing.database(connection, schemas=None)` builds the database from the connection's migrations in the named schemas, or every schema, pending migrations included. For each set of migrations the platform builds a template once, under an advisory lock on the test server, and clones each test's database from it with `CREATE DATABASE ... TEMPLATE`, so a test starts in well under a second.
- After the call, every statement, transaction and batch the test makes on that connection runs on the clone; a statement on another application database the test did not open is refused. Fixtures are written with `platform.execute`. Nothing reaches the connection's own database.
- The clone is dropped when the test ends; an altered-rows rerun starts from a clone of its own. Coverage and the altered-rows check work as for a recorded test.
- The databases are created on the server of the connection's `test_connection`, the connection itself by default, with that connection's credentials and this connection's limits. A test server whose user lacks `CREATEDB` fails the test naming the privilege and the user. A test runs only when its run is allowed both the connection and its `test_connection`.
- The `test_connection`'s user runs the migrations `add` checks and the statements tests make for every connection that names it. Give it `CREATEDB` on a server that holds nothing else, and no other privilege.
- They are named `mcpt_<hash>_<head>` (a template, kept while it is the newest for its migrations), `mcpb_<unix seconds>_<random>` (one being built) and `mcpc_<unix seconds>_<random>` (a test's clone). Each replica runs a sweep, one at a time under an advisory lock on the platform's database, that drops builds and clones its test server's user owns older than `MCP_PLATFORM_APPDB_TEST_MAX_AGE` (default `1h`) every `MCP_PLATFORM_APPDB_TEST_SWEEP_INTERVAL` (default `10m`), and once at start: the ones a run that died never dropped. No other database is ever dropped. `DROP DATABASE ... WITH (FORCE)` needs PostgreSQL 13 or later.

## What is recorded

| Sink | What it keeps |
|------|---------------|
| Audit | The run's own `script_run` event; no row per statement |
| Spans | `postgres.<operation>` per statement with the operation, a table summary, the database and the connection; the SQL text only under `OTEL_TRACES_INCLUDE_DB_STATEMENT=true`; a failure's SQLSTATE, never its message |
| Metrics | `postgres_statements_total{connection,operation,status}`, `postgres_statement_duration_seconds{connection,operation}`, `postgres_slow_statements_total{connection,operation}`; `status` is `ok`, `canceled`, `error` or the SQLSTATE class (`class_23`). `appdb_statement_cache_total{connection,result}` (hit, miss), `appdb_transactions_total{connection,outcome}` (committed, rolled_back, retried, idle_timeout), `appdb_pool_wait_timeouts_total{connection}`, and per replica `appdb_pool_connections{connection,state}` (acquired, idle, max), `appdb_pool_acquires_total{connection,wait}` and `appdb_pool_wait_seconds_total{connection}` |
| Recordings | Each statement in or out of a transaction, its bound values and its answer, and whether a transaction committed, which replay and script tests need. With an encryption key configured, every recording is sealed whole, bound to its run, before it is stored; without one it is stored gzipped |
| Migrations | `manage_schema` calls are audited like any tool call. A migration's SQL and history are kept in the platform's database |

A bound value reaches no audit row, log line, metric label or span. A value a script decrypts and binds reaches the run's recording, which is sealed when the deployment holds an encryption key.
