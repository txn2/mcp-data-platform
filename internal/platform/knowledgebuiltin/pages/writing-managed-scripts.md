# Writing a managed script

A managed script is a small Starlark program the platform stores, versions, and
runs unattended: a KPI report, a recurring export, a dashboard refresh. Write
one when the logic is settled and the work will repeat; keep using the query
tools directly while you are still exploring. The tool is `manage_script`, and
`manage_script command=help` returns the full dialect contract plus worked
examples you can copy.

## The loop

1. `manage_script command=create` (or `update`) stores the source. It is parsed
   on save, so code that cannot run is refused at the keyboard.
2. `command=validate` parses the source and reports the capabilities,
   connections, and destinations it would reach. It executes nothing.
3. `command=run_draft` executes the draft for real under your own identity and
   persona, with tighter limits, persisting nothing: `platform.export` reports
   the shape and size of each output instead of writing it.
4. Once approved, `run_script` executes the approved version — on demand or on
   a schedule — under a capability grant bound at approval.

## The dialect is deliberately smaller than Python

Starlark looks like Python but has no `import`, no `try/except`, no `while`, no
recursion, no f-strings, no classes, no clock, no randomness, no filesystem,
and no network. Each absence is deliberate: errors fail the run so the failure
is recorded, unbounded control flow is off so a script's cost is readable from
its source, and a script with no clock reproduces exactly. The full contract,
as `manage_script command=help` states it, is appended below.

## The mistakes that cost a round trip

- **A SQL DECIMAL column arrives in the rows as a string, not a number.** Pass
  it through `float()` before arithmetic:
  `sum([float(r["total"]) for r in rows])`. Comparing or summing the raw value
  fails or, worse, concatenates.
- **Bind parameters, never concatenate SQL.** `platform.query` takes `:name`
  placeholders and a `params` dict; the platform renders each value as a typed
  SQL literal. A date binds as a quoted string, so compare it against a DATE
  column as `DATE :day`.
- **A truncated query result fails the run** rather than returning a partial
  answer. Aggregate in SQL, or narrow the query.
- **A write statement is refused.** Scripts read with `platform.query` and
  write only through `platform.export`; INSERT, UPDATE, DELETE, CREATE, and
  DROP never execute.
- **f-strings do not exist.** Use `"{}".format(x)` or `"%s" % x`.

## When your script runs without a reviewer

A script at `personal` scope, written by the person who owns it, is approved on
save and runs immediately under the access its author already holds. Four
things send the version to a reviewer instead, and the save says which:

- the source does not parse;
- a call **computes its connection or destination instead of naming one** — a
  `connection=` value that is not a literal string, including a value taken
  from `run.params`, makes the reach unreadable from the code, so the version
  waits for a human. If you want a personal script to run immediately, name
  the connection as a literal;
- the author holds no roles;
- the script writes to a bucket destination no approval has pinned an address
  for, or reaches a connection the author's own persona cannot.

A `global` or `persona`-scoped script, and any script somebody other than its
owner edited, always waits for an administrator.

## The dialect contract

The following is the same text `manage_script command=help` returns, shipped
with this release:

```text
{{DIALECT_CONTRACT}}
```
