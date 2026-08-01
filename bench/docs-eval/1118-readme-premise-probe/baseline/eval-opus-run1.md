## Bottom line

This is an unusually well-engineered and unusually well-marketed project, and those two facts should be weighed separately. The engineering hygiene signals are real and rare. The product scope is alarming. And the headline benchmark claim is weaker than it looks, not because it's dishonest but because of what it structurally can and can't show.

Caveat up front: I'm assessing a README, not a codebase. A great README is evidence of care, but it's also the artifact most optimized for persuasion.

---

## What's genuinely strong

**The problem is real and correctly diagnosed.** "The agent gets column names, not the fact that the table was deprecated last month" is the actual failure mode of text-to-SQL agents in enterprises. Most projects in this space stop at "we wired an LLM to your warehouse." This one identifies that the missing ingredient is metadata the agent doesn't know to go ask for. The strongest line in the whole README is buried: *"Easy to miss warnings."* That's the value, not the round-trip savings.

**The engineering discipline is above the 95th percentile for open source.** Cosign-signed artifacts, CodeQL, OpenSSF Scorecard and Best Practices, `govulncheck` plus `osv-scanner` with *documented, expiring suppressions*, race-detected tests, cyclomatic/cognitive complexity gates enforced in CI on both the Go and React sides with a ratchet baseline, separate load and effectiveness harnesses as their own modules. Most projects claiming "enterprise-ready" have none of this. Whoever built this has shipped production software before.

**The benchmark honesty is better than the norm.** Reporting that the platform is *statistically tied* on tasks where it shouldn't help is the single most credible thing in the document — that's a null result its authors chose to publish. Same for the pinned model, arm-vs-arm design, committed raw data, no-API-key reproduction notebook, and an explicit threats-to-validity section.

**The PostgreSQL-only deployment shape is smart de-risking.** It gives the project a life outside the narrow DataHub+Trino population.

---

## Where I'd push back hard

**1. Scope. This is five or six products in one repo.**

Count what's being maintained: an MCP enrichment proxy, a pgvector memory layer, a knowledge-governance workflow with approvals and rollback, an MCP-to-MCP gateway, a REST/OpenAPI gateway with semantic endpoint ranking, **a from-scratch OAuth 2.1 authorization server with PKCE and Dynamic Client Registration**, an outbound OAuth token manager, a React portal with collections/public sharing/prompt library/feedback threads, inline MCP UI apps, a Go library with a stability policy, and — I am not making this up — **a branded email notification system with per-user digest preferences, a durable retry queue, and delivery history**.

Writing your own authorization server is a decision most teams should be talked out of. Writing your own email digest scheduler *inside a data catalog MCP server* is scope discipline failing in real time. Every one of these is a maintenance and security surface, and the footer suggests this is essentially one author plus two sponsoring companies he's affiliated with. Bus factor is a live concern; so is the odds that some of this surface is thinner than the polished feature table implies.

**2. The benchmark is closer to a functional test than an effectiveness measurement.**

The +56-point gain is on "knowledge-trap questions": questions defined as those that can't be answered without business context. The same team designed the questions and the system that supplies the context. So the finding reduces to *"an agent given the information needed to answer a question answers it more often."* That's worth verifying, but it's near-tautological — it measures that the retrieval plumbing works, not that the platform beats alternatives.

The interesting comparison is absent from the README's summary: not "platform vs. bare SQL tool," but "platform vs. an agent handed the plain DataHub MCP server," or "platform vs. dumping the catalog docs into the system prompt." The four-arm ablation may cover this; the headline doesn't say. Also, 98.7% is essentially ceiling, which usually means the item set is easier than the framing suggests, and the ±11.5-point CI implies a fairly small n. Two Zenodo DOIs lend the visual grammar of peer review to what are self-published vendor reports.

**3. The design is a large indirect prompt-injection surface, and the writeback loop makes it worse.**

The core mechanism is: automatically inject third-party-authored free text — dataset descriptions, glossary terms, uploaded "playbooks," knowledge pages — into a model's context on every tool response. "Metadata is sanitized against prompt injection" is doing enormous work in that sentence; there is no robust sanitizer for natural language. Then the knowledge-capture loop lets agents *write* into the same store that later agents read, creating a self-poisoning path. Human review mitigates it exactly as long as reviewers don't rubber-stamp, which at volume they will.

Now stack **self-configuration** on top: "admins manage personas, connections, and prompts by asking the agent." That's an LLM with a mutation path into the authorization config of a security gateway, sharing a context window with attacker-influenceable text. That combination deserves a threat model in the README, not a feature-table row.

**4. The governance pitch is advisory, not enforcing — and the copy blurs this.**

The opening line is about the agent not knowing `cust_id` is PII. The platform tells the agent it's PII. It does not stop the agent from selecting it. Personas gate *tools and connections*, not rows and columns; actual enforcement is delegated to Trino and S3. That's a defensible architecture, but a compliance-minded reader will come away with a stronger impression than the product delivers.

**5. Enrichment has costs the README doesn't quantify.**

Every enriched call is an extra catalog round trip — latency, load amplification on DataHub, and a new failure coupling (what happens to `describe_table` when DataHub is down? "Fail open with degraded context" and "fail closed" are very different products). And every enriched response spends context tokens. "Session dedup to save tokens" acknowledges this; the benchmark's "statistically tied on non-knowledge tasks" is the reassuring result, but no token or latency overhead figures are given anywhere. For long agent sessions, context dilution is the obvious risk and it's unaddressed.

**6. "Semantic layer" is used in a non-standard way.** In data engineering that term normally means a metrics/dimension layer — dbt Semantic Layer, Cube, AtScale, LookML. Here it means catalog metadata. Anyone shopping for the former will be confused.

**7. No competitive positioning, no roadmap, no adoption evidence.** Nothing on OpenMetadata, Unity Catalog, dbt's MCP server, or the native MCP offerings from Databricks and Snowflake — all of which overlap. No named production users, no contributor count. And "contributions for bug fixes, tests, and documentation are welcome" pointedly omits features, which tells you it's a single-vendor project with a closed roadmap. Legitimate, but plan accordingly.

**8. Operational weight is undersold.** Full experience: PostgreSQL+pgvector, DataHub, Trino, S3, an IdP, SMTP, Prometheus/OTel. DataHub alone is a platform team's project. That's fine for a company that already has this stack; it's a wall for everyone else.

---

## If I were evaluating it for adoption

Questions I'd want answered before committing:

- What does the four-arm ablation compare, exactly, and what's the n? Does any arm test "plain DataHub MCP server"?
- What's the p50/p99 latency and token overhead of enrichment, and what's the behavior when the catalog is unavailable?
- What's the threat model for indirect prompt injection through catalog metadata and agent-written knowledge, especially with self-configuration enabled?
- How much of the feature table is load-bearing vs. demo-grade? Test coverage per subsystem would tell you fast.
- Is the OAuth 2.1 server optional? Can I put my existing IdP/gateway in front instead?

**Fit:** strong if you already run DataHub + Trino and have a platform team. Reasonable as a PostgreSQL-only MCP/API gateway with memory. Poor if you're on Snowflake/Databricks/BigQuery without Trino, or if you lack the ops capacity — in which case the honest alternative is a much smaller enrichment shim you write yourself, since the core idea (splice catalog context into tool responses) is maybe a few hundred lines.

**The one-sentence version:** an excellent idea, executed with real rigor, wrapped in a scope that's roughly three times what one team can sustain, and sold with a benchmark headline that's more circular than it sounds.
