## Up front

Assessing from a README alone means assessing *claims and presentation*, not code. What I can judge: the coherence of the value proposition, the credibility of the evidence offered, the shape of the risk surface, and what the document chooses not to say. On those axes this is a well-above-average project with one structural problem and one sharp security question.

## What's genuinely strong

**The core insight is real.** "Your AI assistant can run SQL but doesn't know `cust_id` is PII" is a correct diagnosis. Agents don't fail at querying; they fail at knowing what to distrust. Attaching deprecation, ownership, and quality context at the protocol layer — so the model can't skip the step — is a better answer than "put it in the system prompt and hope."

**The engineering discipline signals are unusually credible.** Most projects wave at security with a badge wall. This one distinguishes `govulncheck` (reachability-based) from `osv-scanner` (whole-graph, Scorecard-style), keeps the noisier one out of the blocking suite, and documents suppressions *with expiry dates*. It enforces cyclomatic and cognitive complexity budgets on both the Go and TypeScript sides with a ratchet baseline. It has a dedicated e2e test that renders the public share viewer and fails on anything CSP blocks. Load testing and effectiveness benchmarking are separate Go modules. This is someone who has been burned before and built accordingly.

**The benchmark reports a null result.** Claiming a +56-point gain on knowledge-gated questions *and* explicitly saying the platform ties bare tools on lookups and arithmetic is the behavior of someone measuring rather than marketing. Same for publishing the four-arm ablation, a threats-to-validity section, and recomputation from committed raw data.

## The structural problem: scope

Count the products in here. Semantic enrichment middleware. A vector-backed memory store. A knowledge-governance workflow with changesets and rollback. An MCP proxy. A REST API gateway with OpenAPI catalogs. An OAuth 2.1 **authorization server** with PKCE and Dynamic Client Registration. A web portal with dashboards, collections, public share links, a prompt library, and feedback threads. An email subsystem with per-user preferences, daily digests, a durable retry queue, and delivery history. Inline UI panels via MCP Apps.

That is five or six products. The email digest feature in particular reads as a long way from "AI assistant can run SQL." And writing your own OAuth authorization server is a decision most teams should be talked out of — it's a large, adversarially-probed surface where the failure mode is silent.

This matters because of the second signal: the footer says "Open source by Craig Johnston, sponsored by Deasil Works and Plexara," and contributions are invited for "bug fixes, tests, and documentation" — pointedly not features. Read together, that's a vendor-controlled roadmap with a small core team maintaining an enormous surface. Apache 2.0 protects you legally; it doesn't staff the project. The bus factor question is the single most important thing I'd want answered before depending on this.

## The sharp security question

Two design decisions interact badly, and the README doesn't acknowledge the interaction:

1. The platform's job is to **inject third-party-authored text into model context** — catalog descriptions, glossary terms, knowledge pages, and, via the MCP gateway, the tool descriptions of proxied servers. Tool-description poisoning is a known, live attack class.
2. **Self-configuration**: "Admins manage personas, connections, and prompts by asking the agent." Personas *are* the authorization model. So the agent that consumes untrusted text can also, in an admin session, rewrite the access-control policy.

The README's answer is one clause: "metadata is sanitized against prompt injection." Sanitizing free-text against injection is not a solved problem and shouldn't be stated as a settled property. The honest framing is defense-in-depth with residual risk. I'd want to know whether self-configuration is off by default, whether it requires a separate confirmation channel outside the model loop, and whether proxied tool descriptions are treated as untrusted input.

Relatedly: for a project this security-forward, I see no mention of a vulnerability disclosure process. That's a conspicuous gap.

## Where the benchmark is weaker than it reads

The number is 42.7% → 98.7%, and both endpoints deserve scrutiny.

The 98.7% is near-ceiling, which usually means the questions were answerable by retrieving a specific fact the system had been seeded with — closer to a retrieval test than a reasoning test. And "knowledge-trap questions" were presumably authored by the people who built the knowledge layer, which is a construct-validity problem: the benchmark defines the category of question the product is good at. A 95% CI spanning +44 to +67 implies a small *n*. A Zenodo DOI confers citability, not peer review.

None of this makes the result false — the mechanism is plausible and the direction is almost certainly right. But the load-bearing question is whether the baseline arm is fair: could an agent with plain `mcp-datahub` + `mcp-trino` and decent prompting close most of that gap? The four-arm ablation may answer this; the README doesn't, and the headline number will be quoted without its caveats.

The "4 round trips vs 1 call" diagram is also somewhat a straw man — modern agents parallelize tool calls. The real argument is that the agent *doesn't know to ask*, which is stronger, and which the README does make one line later.

## The adoption paradox

The differentiating feature — cross-enrichment — requires a mature, well-populated DataHub with real ownership, tags, glossary terms, and quality scores. Most organizations' catalogs are sparse or stale. Enrichment doesn't fix that; it launders stale metadata into confident-sounding agent output.

The project clearly knows this, which is why the knowledge write-back loop exists. But that loop's governance workflow depends on human review — the exact bottleneck that left the catalog empty in the first place. Whether AI-drafted-plus-human-approved metadata actually clears that bottleneck, or just relocates it, is the project's central unresolved bet.

Meanwhile the low-friction entry path ("runs on PostgreSQL alone") gives you the gateways, memory, and portal — but *not* cross-enrichment. The easiest way in is the least differentiated version of the product.

## What's missing from the README

No comparison to alternatives (DataHub's own MCP efforts, dbt's MCP server, warehouse-native agent tooling). No maturity or stability statement for the server itself — there's a release badge and a library API-stability policy, but nothing telling me whether the server's config format will break under me. Coverage badge with no number quoted. Nothing on audit-log retention or PII handling, which matters when you're logging every tool call. And the architectural rationale link goes to the author's own blog — self-referential authority.

## Bottom line

If you run DataHub and Trino, have an IdP, and want agents to stop confidently reading deprecated tables, this is the most serious attempt at that problem I've seen described, and the engineering rigor is real. Pilot it.

If you're evaluating it as long-term infrastructure, the risks are concentration (one sponsor, huge surface, features closed to outside contribution) and a security model where the agent can reconfigure its own permissions while consuming untrusted text. Neither is disqualifying; both should be answered before you build on it.

If you're the maintainer: the email digest queue is not why anyone will adopt this, and every feature like it is surface you have to defend forever.
