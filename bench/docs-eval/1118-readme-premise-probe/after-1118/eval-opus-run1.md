## What it claims to be

A single MCP server that sits between AI assistants and a data stack (Trino/DataHub/S3), splicing catalog metadata into every tool response, plus a fairly large surrounding platform: agent memory, a knowledge-writeback workflow, an MCP/REST gateway, an OAuth 2.1 broker, RBAC, audit, and a web portal.

Caveat up front: I'm judging the README, not the code. Everything below is an assessment of the *claims and the shape of the project*, not a verification of them.

## What's genuinely strong

**The core insight is correct and underserved.** Agents querying warehouses do work blind. Column names and types are not enough to answer a business question safely, and the failure mode — confident, plausible, wrong — is exactly the one that's hardest to catch. Pushing governance metadata into the same response as the schema is the right structural fix.

**The security section is unusually specific.** It names files, cites RFC 8252 §7.3 for loopback redirect handling, distinguishes bcrypt (client secrets) from SHA-256 (issued tokens) from AES-GCM (upstream refresh tokens), and rate-limits `/token` *before* the bcrypt work. Those are the details of someone who has actually implemented OAuth rather than imported it. The "broker, not an IdP" framing is honest and accurate about why MCP forces this shape.

**Prompt injection via catalog metadata is a real attack surface** and almost nobody talks about it. Any system that pipes free-text owner notes and tag descriptions into a model's context has just created an injection channel. Naming it is a point in their favor.

**The engineering posture is mechanically enforced, not asserted.** `make posture-check` failing CI when the stated test ratios drift is a genuinely good idea — README claims decay, and this one can't silently.

**Publishing raw benchmark data with a no-API-key recomputation notebook** is more than most vendors do, and the scoping ("this measures the knowledge layer, not the whole platform"; "statistically tied on non-knowledge questions") shows discipline.

## Where I'd push back

**The headline benchmark is close to tautological.** 42.7% → 98.7% is an enormous effect, and the reason is visible in the construction: "knowledge-trap questions" are questions whose answers live in the semantic layer, evaluated against a system whose job is to inject the semantic layer. If you define the test set as *questions requiring context X*, the arm with context X wins by definition. The 98.7% ceiling suggests the tasks reduce to retrieval-and-report once the context is present. This demonstrates the plumbing works — worth knowing! — but it is not evidence about realistic mixed workloads, and the +56-point number will be read as the latter. The Zenodo DOIs add an academic veneer that self-archiving doesn't actually confer.

**"Statistically tied" on non-knowledge questions is presented as neutral. It isn't.** If enrichment fires on lookups and arithmetic where it provides no accuracy benefit, then on that segment you're paying tokens and latency for nothing. The README leads with accuracy and mentions "session dedup to save tokens" without quantifying either. For agent systems, context-window pressure and per-call latency are first-order costs, not footnotes.

**The most important operational question is unanswered: what happens when DataHub is slow or down?** Every Trino call now fans out to a second service. Does the query fail, hang, or degrade to unenriched? What's the p99 penalty? "Fail-closed" is stated for auth; enrichment presumably needs to fail *open*, but that tension is never addressed. I'd want this on the front page.

**The value is entirely hostage to catalog quality, which the README never concedes.** Most real DataHub deployments are sparsely populated, with stale ownership and aspirational quality scores. Injecting a wrong owner or an out-of-date deprecation notice doesn't just fail to help — it manufactures a new class of confident error, now with an authoritative-looking provenance. "Garbage in the catalog" is the binding constraint on this entire product, and it goes unmentioned.

**The scope is alarming for what appears to be a one-maintainer project.** Cross-enrichment engine, vector memory, governance workflow, MCP gateway, REST/OpenAPI gateway, OAuth authorization server, persona RBAC, audit, admin portal, user portal, asset collections, public sharing, prompt library, feedback threads, MCP Apps UI panels, and a branded email notification system with daily digests, per-share toggles, and delivery history. That last one is a product unto itself and has nothing to do with the thesis. This reads as accretion, and each subsystem is a permanent security and maintenance liability — an OAuth server and a public-link sharing feature especially. Bus factor looks like one, with two commercial sponsors.

**"Contributions for bug fixes, tests, and documentation are welcome"** — note the omission. This is single-vendor OSS with a controlled roadmap. Apache 2.0 protects you legally; it doesn't give you influence. Combine that with the fact that your memory, knowledge, assets, personas, audit trail, and OAuth client registrations all live in its schema, and the exit cost is high. Nothing in the README addresses export.

**"Sanitized against prompt injection" is overstated.** There is no reliable sanitizer for natural-language injection. Listing it in a table beside exact-match `redirect_uri` validation and bcrypt hashing — controls that are actually verifiable — implies a parity that doesn't exist. It mitigates; it cannot prevent. Operators reading that table could reasonably conclude they're covered.

**The 4-round-trips diagram argues the weaker case.** A competent agent with a DataHub MCP server attached wouldn't need four user turns. The real argument is the one made in the prose right after — the agent *doesn't know to ask* — and the diagram undercuts it by reframing a knowledge problem as a latency problem.

**"Workflow gating… escalating warnings" and "orientation unskippable"** is middleware nagging the model into compliance. It works until the host's system prompt or the next model release disagrees with it. Needing escalating warnings to get agents to run discovery before SQL is a sign the tool design isn't carrying its own weight.

**The DataHub disclaimer's prominence is telling.** A long, insistent "you don't actually need DataHub" section placed *before* the "Why" suggests repeated pushback. It's a fair clarification, but note where it lands you: strip DataHub and what remains is a generic MCP/API gateway with a portal and a memory store, competing in a crowded and rapidly commoditizing space where the differentiation is thinnest.

## Missing from the README

Maturity/stability statement for the platform (not just the Go library). Any named production adopters. Latency or throughput numbers, despite a load harness existing. What embedding model powers pgvector recall, where it runs, and whether it egresses data. DB migration/upgrade story. A "known limitations" section. A comparison to alternatives.

## Bottom line

The thesis is right, the security work looks more careful than typical, and the measurement culture is above average for this ecosystem — but the headline benchmark proves less than it appears to, the scope is far wider than one maintainer can plausibly sustain, and the two facts that most determine whether this works for you (your catalog's actual quality, and the latency/token tax of enrichment) are the two the README doesn't discuss.

Worth a serious evaluation if you already run a well-curated DataHub and Trino and are deploying agents against them. Approach the portal, OAuth server, and sharing features as a separate adoption decision with their own risk profile — and pin your version.
