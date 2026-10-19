# What the platform documents about itself

These pages ship with the platform and are updated by each release on its next
start. They hold what a tool's schema cannot: the rules behind a feature, the
patterns that work, and the mistakes worth avoiding. Each page below is named
with a one-line summary; `fetch` one by its reference, or open it here.

How the topics lead into one another:

```mermaid
flowchart LR
  W["writing a script,<br/>its dialect and a<br/>whole worked example"] --> O["where its output lands<br/>and what identity it keeps"]
  O --> D["a dashboard whose data<br/>a schedule refreshes"]
  O --> R["files an asset's<br/>content names"]
  D --> R
  R --> V["documents that fit any screen,<br/>presentations and maps"]
  V --> T["the media type<br/>a stored file declares"]
  C["naming sources and<br/>capturing what was learned"] --> O
```

{{PAGE_INDEX}}
