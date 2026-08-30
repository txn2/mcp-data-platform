---
description: "The admin Tools page: every registered tool, its schema, a live runner, activity, enrichment rules, and visibility."
---

# Tools

The Tools page is a master-detail view. The list on the left groups every registered tool by connection (Trino, DataHub, S3, platform, and gateway-proxied MCP) with search filtering; selecting a tool opens its detail across five tabs.

## Overview

![Tools Overview](../images/screenshots/light/admin-admin-tools-overview-light.webp#only-light)![Tools Overview](../images/screenshots/dark/admin-admin-tools-overview-dark.webp#only-dark)

The Overview tab shows the selected tool's description (with an inline override editor), toolkit kind, connection, title, the JSON input schema, and per-persona access — which personas can call the tool and the rule that decided it.

## Try It

![Tools Try It](../images/screenshots/light/admin-admin-tools-tryit-light.webp#only-light)![Tools Try It](../images/screenshots/dark/admin-admin-tools-tryit-dark.webp#only-dark)

An interactive execution environment for the selected tool:

- **Dynamic parameter form** — Auto-generated from the tool's JSON schema with type-appropriate inputs (text areas for SQL, number fields for limits, dropdowns for enums)
- **Result display** — Rendered markdown tables for structured data, with a Raw toggle for JSON output
- **Execution history** — Timestamped log of tool calls with duration, status, and replay capability

## Activity

![Tools Activity](../images/screenshots/light/admin-admin-tools-activity-light.webp#only-light)![Tools Activity](../images/screenshots/dark/admin-admin-tools-activity-dark.webp#only-dark)

Aggregated call volume, success rate, and average duration for the selected tool over the recent window, with a deep link to the audit log filtered to this tool.

## Enrichment

![Tools Enrichment](../images/screenshots/light/admin-admin-tools-enrichment-light.webp#only-light)![Tools Enrichment](../images/screenshots/dark/admin-admin-tools-enrichment-dark.webp#only-dark)

Shown for gateway-proxied (MCP) tools with a connection. Lists the enrichment rules attached to the tool — each rule's predicate, action source and operation, merge strategy, and enabled state. This is where the platform's bidirectional cross-enrichment is configured per tool.

## Visibility

![Tools Visibility](../images/screenshots/light/admin-admin-tools-visibility-light.webp#only-light)![Tools Visibility](../images/screenshots/dark/admin-admin-tools-visibility-dark.webp#only-dark)

Toggle the tool's membership in the platform-wide deny list, and preview whether a given persona can access it before committing the change.

