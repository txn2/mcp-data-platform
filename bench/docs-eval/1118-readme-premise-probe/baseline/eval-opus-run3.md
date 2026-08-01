## What it's actually selling

Strip the marketing and there are three separable products bundled into one binary:

1. **A metadata enrichment proxy** — intercepts Trino/S3/DataHub tool responses and splices catalog context into them. This is the novel, defensible bit.
2. **A generic MCP/REST gateway** — auth, persona-based ACLs, audit logging, OAuth 2.1. Useful, but a crowded space.
3. **A knowledge/memory/portal product** — pgvector memory, agent-authored knowledge with human review, an asset-sharing web app with collections, prompt libraries, feedback threads, and branded email digests.

That third bucket is where my skepticism concentrates.

## What's genuinely good

The engineering hygiene is well above the open-source median, and in specific, non-cosmetic ways. Distinguishing `govulncheck` (reachability-aware) from `osv-scanner` (whole-graph, Scorecard-equivalent) and keeping *documented, expiring* suppressions is a level of care most projects never reach. Complexity budgets enforced on both the Go and TypeScript sides with a ratchet baseline, race-detected tests, cosign-signed artifacts, and load/bench harnesses as separate modules all point to someone who has run production software before.

The benchmark framing is also unusually honest for a vendor: reporting that the platform is *statistically tied* with bare tools on questions where context doesn't help is the kind of negative result marketing departments delete. Committed raw data, a no-API-key recompute notebook, and a threats-to-validity section are real accountability.

Apache 2.0 across the whole thing, portal included, with no open-core teaser tier.

## Where I'd push back

**The headline number is close to circular.** "Knowledge-trap questions" are defined as questions an agent gets wrong without business context — that is, questions whose answers live in the metadata the platform injects. Going 42.7% → 98.7% on those is closer to a plumbing correctness check than an effectiveness finding. The near-ceiling result and the ±11-point CI suggest a small n on a task set authored by the same party that built the system. The arm that would actually be persuasive isn't clearly present: *platform vs. a cheap alternative* — dumping the glossary into the system prompt, or just letting the agent call DataHub's own MCP server. Beating "no context at all" is a low bar.

**Scope is the main risk, not the tech.** This project ships its own OAuth 2.1 authorization server with PKCE and Dynamic Client Registration, an SMTP notification system with a durable retry queue, a React portal with public share links, and inline UI panels — all maintained alongside a data-enrichment proxy. That's four or five products' worth of attack surface and maintenance load behind what looks like a one-maintainer, consultancy-sponsored project. Writing your own authorization server is a decision most teams should not make.

**Security posture deserves harder questions than the README invites.** The platform is by design a confused-deputy magnet: it holds credentials to your warehouse, object storage, and third-party SaaS, and personas are the only boundary. Then it adds (a) metadata from a catalog flowing into model context, (b) agents *writing back* to that catalog, and (c) "self-configuration," where admins change personas and connections *by asking the agent*. If prompt injection lands anywhere in that loop, the escalation path is short. "Metadata is sanitized against prompt injection" is an acknowledgment, not a solution — sanitization has a poor track record against this class of attack. Human-in-the-loop review helps until review volume makes rubber-stamping the default.

**Two of the control mechanisms are non-deterministic by construction.** "Workflow gating" steers agents to discovery before SQL with "escalating warnings," and "explicit session handles" require the agent to thread a `session_id` through every call to make orientation "unskippable." Both are prompt engineering dressed as middleware. They work until a model decides not to comply, and they'll behave differently across model versions.

**The hard problem is assumed away.** Every example — deprecation warnings, PII tags, an 87% quality score, column business definitions — presupposes a DataHub instance somebody actually curates. Organizations that have that discipline are rare, and they're also the ones least blocked by the problem this solves. Organizations without it get an elaborate pipe carrying nothing. The knowledge-capture loop is an honest attempt at bootstrapping this, but the README never names curation as the actual bottleneck.

**The fallback shape undercuts the pitch.** "No warehouse or catalog? Runs on PostgreSQL alone" is a reasonable on-ramp, but in that configuration you've discarded cross-enrichment — the differentiator — and you're now a generic MCP gateway competing with a dozen others, carrying a portal and an auth server you may not want.

## What the README doesn't tell you

- **Latency and token cost.** Enrichment adds a DataHub round trip to every tool call. "Session dedup to save tokens" implies the overhead was significant enough to need mitigating; no numbers are given. There's a load-test harness — publish its output.
- **Maturity and adoption.** No production users, no case studies, no statement of what's stable versus experimental. A codecov badge with no visible percentage.
- **When *not* to use it.** No comparison to DataHub's own MCP server, dbt's MCP server, Cube, or warehouse-native offerings from Snowflake/Databricks. A README that can't articulate its non-fit is doing positioning, not documentation.
- **Governance.** "Contributions for bug fixes, tests, and documentation are welcome" — features conspicuously absent. Read that as: this is one person's roadmap, and you're a consumer. Bus factor looks like one; sponsorship comes from the author's own consultancy.

## Bottom line

Credible engineering, real and honest measurement, and a genuinely good core idea — enrichment-at-the-protocol-layer is the right place to solve this — wrapped in a README that oversells via badge density, self-minted DOIs, and a strawman before/after comparison.

Worth a serious look if you already run DataHub *and* keep it curated, and you're willing to treat everything except the enrichment proxy as optional. Approach with more caution if you'd be adopting it for the gateway, the portal, or the built-in OAuth server: that's a lot of security-critical surface from a thin maintainer base, in a protocol ecosystem still changing underneath it. And treat the +56-point figure as a plumbing validation, not evidence of a 56-point improvement in your analysts' work.
