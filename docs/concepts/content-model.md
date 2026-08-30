---
description: The four content layers - resources, assets, knowledge pages, and memory - what each holds, who writes it, how an agent reaches it, and the operator rule that makes an approved template mandatory.
---

# Content Model: Resources, Assets, Knowledge, Memory

Four places content lands in this platform, and the difference between them is not the file format. It is who authored it and what the agent is supposed to do with it.

!!! quote "The split"
    Resources are human-uploaded inputs an agent uses as-is: report templates, brand files, data dictionaries, sample payloads, and reference documents. Assets are AI-generated outputs. Knowledge pages are curated facts to search and synthesize. Memory is per-user recall. If it existed before the conversation and the agent should use it verbatim, it is a resource.

That statement is a single string in the source (`instructions.ResourcePositioning`). The agent reads it in `platform_info`, the person uploading a file reads it on the portal resources page and in the upload dialog, and a test fails the build if any of those copies drifts from the others.

## The four layers

| Layer | Holds | Authored by | Agent reaches it via | Portal |
|-------|-------|-------------|----------------------|--------|
| **Resources** | Files used as-is: templates, brand assets, data dictionaries, sample payloads, runbooks | A person, before the conversation | `search` then `fetch`, the `resources/read` protocol method, a prompt attachment, or an [asset reference](../server/asset-references.md) | Resources |
| **Assets** | Dashboards, reports, visualizations, documents produced during a session | The agent, during the conversation | `save_asset` / `manage_asset`, and `search` | Assets |
| **Knowledge pages** | Curated business and domain facts, cross-linked and citable | A person or a promoted insight, reviewed | `search` then `fetch`, `apply_knowledge` | Knowledge |
| **Memory** | Per-user recall: preferences, corrections, working context | The agent on the user's behalf | `memory_capture` / `memory_manage`, and `search` | Knowledge |

The consequences of getting the split wrong are concrete. A report template saved as an asset is an output the agent believes it produced, so the next session regenerates rather than reuses it. A reference PDF pasted into a knowledge page loses its formatting and its bytes, which is exactly what a brand file exists to preserve. A durable fact filed as a personal memory reaches one user instead of the team.

### Choosing

- Did it exist before the conversation, and should the agent reproduce it rather than rewrite it? **Resource.**
- Did the agent make it in a session, and does a person want to keep or share it? **Asset.**
- Is it a fact you want found, cited, and synthesized into new answers? **Knowledge page.**
- Is it about how one person works? **Memory.**

A resource is filed under a folder path inside its library, and the suggested first-level folders carry the same distinction one level down: `data` are records to read as fact rather than as an example, `templates` are layouts a deliverable must be produced in, `playbooks` are procedures to follow rather than summarize, `samples` are examples to pattern-match against, and `references` are documents to consult. The line between `data` and `samples` is what the agent does with the rows: read them, or copy their shape. `visual` is the one suggestion named for what the file is rather than for what the agent does with it -- logos, photographs, diagrams, and design elements meant to be displayed. They are suggestions and not a closed set: a path nests as deep as a library needs. An asset that needs one of those names it by URI rather than writing its bytes into the markup; see [Asset References](../server/asset-references.md).

Some knowledge pages arrive with the platform itself: the [built-in pages](../portal/knowledge.md#built-in-pages) documenting the features whose purpose is not readable from tool schemas (managed-script authoring, export identity, the dashboard refresh pattern, asset references and the refresh loop, provenance and the capture loop). Each states its mechanism as a mermaid diagram as well as in prose. They are reconciled from the binary at startup, badged **Built-in**, read-only where people edit, and a deployment can hide one (and later restore it) and write its own page on the topic. Search ranking is not their only way in: the instruction baseline's scripts bullet and `manage_script help` name them by slug, and `fetch` takes a page's slug in place of its id, so an agent reads the page that bears on the decision in front of it rather than hoping a query surfaces it.

## What the agent is told

When a deployment has managed resources, `platform_info` appends a resources section to the agent instructions. It carries the positioning statement above plus the operating rule:

- Before formatting a deliverable, `search` for an applicable template or reference resource and follow it rather than inventing a layout.
- When the user names a company file ("our template", "the checklist", "the brand header"), resolve it with `search` and read it in full with `fetch` rather than asking them to paste it.
- Material attached to a prompt is authoritative: use it as given.

The section names a tool only when the caller's persona can reach it. A persona denied `search` is pointed at the `resources/list` protocol method instead, which is persona-filtered but is not a tool and is therefore always available. A deployment with no managed resources gets no section at all.

This is steering, not enforcement: it changes what the agent knows to look for, and the [Knowledge-Layer Benchmark](../reference/benchmarks.md) is where the behavior it produces is measured.

## Making an approved template mandatory

The operator surface for a stricter rule is `server.agent_instructions`, which composes beneath the platform baseline in `platform_info`. It is set in YAML or edited at runtime from the admin portal's **Agent Instructions** page, which writes the `server.agent_instructions` config key to the database, so a rule can be tightened without a redeploy.

```yaml
server:
  name: mcp-data-platform
  agent_instructions: |
    Reporting standard: every report, summary, or briefing you produce must use the
    approved template for that deliverable. Before writing one, search for a resource
    under the `templates` folder matching the deliverable's name. If one exists, fetch
    it and produce your output in its structure, section order, and headings. Do not
    restructure it, and do not substitute your own layout. If no approved template
    exists, say so in one line before the output so the gap is visible.
```

Scoping the rule to one audience instead of the whole deployment is a persona suffix, appended to the same layer:

```yaml
personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst"]
    context:
      agent_instructions_suffix: |
        Client-facing deliverables additionally require the brand header resource. Fetch
        it and place it before the first section.
```

Both layers land in the same `agent_instructions` string the agent receives, beneath the platform baseline and above the resources section. To see what the baseline already covers before writing your own, read it from `GET /api/v1/admin/config/agent-instructions-baseline`, which renders the baseline for this deployment's registered tools.

## Where each layer is documented

- [User Portal](../portal/index.md) covers the resources, assets, and knowledge surfaces a person uses
- [Knowledge Capture](../knowledge/overview.md) covers the insight-to-page lifecycle
- [Memory Layer](../memory/overview.md) covers per-user recall
- [Configuration](../server/configuration.md#managed-resources) covers the `resources.managed` block, and [Configuration](../server/configuration.md) covers `server.agent_instructions`
