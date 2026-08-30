# Configuration Reference

The canonical, actively-maintained configuration reference is
[Configuration](../server/configuration.md) - it covers every `platform.yaml`
block (server, auth, database, personas, toolkits, cross-enrichment,
tuning, workflow gating, portal, admin API, audit, sessions, knowledge,
memory, API gateway, MCP Apps, resources, elicitation, icons) with field
tables, defaults, and a complete example.

This page previously duplicated that content and had drifted out of sync
with it on several defaults; it's now a redirect to avoid a second copy
going stale. A few topics have their own deeper reference:

- [OAuth 2.1 Server](../auth/oauth-server.md) - the built-in inbound
  authorization server (`oauth:` block)
- [Trino to DataHub](../cross-enrichment/trino-datahub.md#urn-mapping-for-mismatched-names) -
  URN mapping when Trino and DataHub name the same data differently
- [Lineage Inheritance](../cross-enrichment/lineage.md) - lineage-aware
  column metadata inheritance (`semantic.lineage`)
- [The Portal](../portal/index.md) - enabling it, branding, and the public
  viewer, with the guided tour of every screen it serves
- [Knowledge Capture](../knowledge/overview.md) and
  [Memory Layer](../memory/configuration.md) - knowledge/memory subsystem
  configuration
- [Tuning and Scaling](tuning-and-scaling.md) - resource sizing and Go
  runtime tuning, as opposed to the `tuning:` operational-rules block
