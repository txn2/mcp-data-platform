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

Steps 2 and 3 act on the `source` you send with the call, and on the saved
version when you send none. That is what makes them a loop: a save is
immediately the version `run_script` executes and a schedule fires, so sending
the edit is how you try a change without making it live.
4. Once saved, `run_script` executes the latest saved version — on demand or
   on a schedule — as the script's own principal, presenting the roles you held
   at the save.

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
- **A destination has to be one this deployment declares.** `platform.export`
  writes to `portal` unless it names a bucket destination the operator
  configured under `scripts.destinations`. `validate` reports a name the
  deployment cannot serve, which is worth checking after an upgrade: the
  declared set changes without the script changing.

## A saved script runs

Saving a version makes it the version that runs, immediately, under the access
you hold at the save. There is no approval step: every call a run makes is
authorized against your captured roles by the same persona filtering an
interactive session gets, so the script can reach exactly what you can reach
and nothing more. A disabled, deprecated, or superseded script is the only
thing the run gate refuses.

A script is yours: you are the only person who sees it, edits it, runs it, and
schedules it, and administrators do all four on every script. An administrator
can also move a script to another owner, which hands over all of that at once.

## The dialect contract

The following is the same text `manage_script command=help` returns, shipped
with this release:

```text
{{DIALECT_CONTRACT}}
```
