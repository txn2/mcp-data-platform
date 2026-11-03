# The managed-script dialect contract

The contract a managed script is written against: every module, member and
argument, and every rule a save holds a script to. It is the text
`manage_script command=help` returns in this release. The authoring loop is on
mcp:knowledge_page:platform-writing-managed-scripts.

```mermaid
flowchart LR
  P["platform.*<br/>query, transaction, call, export ..."] --> S["your script's main()"]
  R["run<br/>params, state, caller"] --> S
  M["json, date, re, hash, xml"] --> S
  S --> T["test_* functions<br/>testing, assert"]
  T --> V["a save runs them<br/>and holds the rules below"]
```

```text
{{DIALECT_CONTRACT}}
```
