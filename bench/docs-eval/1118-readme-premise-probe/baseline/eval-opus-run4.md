I can only assess the README, not the code — so treat this as an evaluation of the *claims and the shape of the project*, not of delivery quality. A README can't be wrong about ambition, only about execution.

## What's genuinely good

**The problem is real.** Agents with SQL access but no catalog context is a legitimate failure mode, and "the model confidently queried the deprecated table" is a thing that actually happens. The framing is honest about it.

**The engineering hygiene signals are unusually strong.** OpenSSF Best Practices + Scorecard, CodeQL, cosign-signed artifacts, `govulncheck` *plus* `osv-scanner` with documented, expiring suppressions, complexity budgets enforced on both the Go and TypeScript sides with a ratchet baseline. Most projects in the MCP space have a `main.go` and vibes. This is the profile of someone who has shipped production software before.

**A benchmark exists at all.** With committed raw data, a recompute notebook that needs no API key, an ablation design, and an explicit "threats to validity" section. That's rarer than it should be, and the negative result — no gain on plain lookups — is reported rather than buried. That's a credibility signal.

**Security posture is stated in the right direction:** fail-closed, default-deny personas, enforced read-only, and — importantly — sanitizing catalog metadata against prompt injection. Catalog descriptions are attacker-writable in a lot of orgs; most people don't think about that.

## Where I'd push back

**Scope sprawl is the headline risk.** The README itself pivots from "a bridge" to "a platform," and the feature list is roughly six products: an MCP enrichment server, an MCP gateway, a REST/OpenAPI gateway with semantic endpoint ranking, a vector memory + knowledge governance system, a full React web portal with collections/sharing/public links/feedback threads/prompt library, and — this one stands out — a **built-in OAuth 2.1 authorization server with PKCE and Dynamic Client Registration**. Plus an SMTP notification system with digests, retries, and delivery history. Writing your own authorization server is a security-critical, high-maintenance undertaking that most teams would rather delegate to Keycloak; the email digest subsystem is the kind of thing that appears when a tool is drifting toward being a SaaS app. Each of these is permanent surface area for what appears to be a very small team.

**Interrogate the benchmark before you believe the number.** 42.7% → 98.7% is a big-sounding delta, but:
- The question category ("knowledge-trap questions") was defined by the authors. Questions constructed to be unanswerable without business context will, near-tautologically, be answered better when you inject business context. That's closer to a plumbing validity check than evidence of business value.
- 98.7% is at ceiling, which means the benchmark can't discriminate between this implementation and a mediocre one.
- Self-designed, self-run, self-published. A Zenodo DOI is archival, not peer review; the badge lends an academic sheen the artifact hasn't earned.
- The arm I'd want is: *agent wired to plain `mcp-datahub` + `mcp-trino` separately, allowed to make four calls.* If that arm also scores high, then cross-enrichment is a latency/token optimization, not an accuracy mechanism — a fine thing to be, but a different claim. The README says "four-arm ablation," so this may be answered; go check specifically for it.

**The "4 round trips" motivating story is dated.** Modern agents chain tool calls autonomously; the user doesn't experience four turns. The real cost is latency and tokens — and enriching *every* response has its own token cost. The parenthetical "with session dedup to save tokens" is a quiet admission that automatic enrichment bloats context. Injecting owners, tags, and quality scores into responses the model didn't need them for is not free.

**The value proposition is conditional on a well-curated DataHub, and most catalogs are stale.** Nothing in the README addresses metadata freshness or trust. If ownership is three reorgs out of date and quality scores are from a broken job, the platform's contribution is to present stale metadata to the agent with more authority than before. That's arguably worse than no metadata. The "run on PostgreSQL alone, no catalog" fallback is a sensible hedge, but it drops the differentiating feature and leaves you with a gateway + memory + portal in a much more crowded field.

**Enrichment-by-interception is architecturally brittle.** "Intercepts tool responses at the protocol level" means parsing the output shape of upstream tools. That works cleanly for `mcp-trino`, `mcp-datahub`, and `mcp-s3` because the same author controls all three. It explains why the supported set is exactly those three, and why there's no Snowflake, BigQuery, Unity Catalog, dbt, OpenMetadata, Atlan, or Collibra story. If you're not a Trino + DataHub shop, most of the unique value doesn't apply to you.

**"Self-configuration" is a design tension worth scrutinizing.** Admins manage personas and connections "by asking the agent." The same agent is exposed to prompt injection from catalog metadata and from arbitrary proxied third-party MCP servers. That's a path from injected content to modification of the authorization config. There's presumably a guard; the README doesn't say what it is.

**Related:** the public share viewer renders AI-generated HTML/JSX/SVG, and the test that verifies CSP actually blocks things runs *on demand, not per commit*. Understandable (needs a live stack and network egress) but it's a security-relevant test outside CI.

**Bus factor.** "Open source by Craig Johnston, sponsored by Deasil Works and Plexara" reads as single-vendor, likely largely single-author. The contribution line is telling: bug fixes, tests, and documentation are welcome — features are conspicuously not listed. Apache 2.0 protects you legally; it doesn't give you a maintainer.

**Spec churn.** Building deep protocol-level behavior while explicitly chasing the "sessionless MCP 2026-07-28 protocol" means a large surface tracking a fast-moving spec.

## Verdict

Credible, unusually well-engineered, and honestly benchmarked *by the standards of the MCP ecosystem* — which is a low bar it clears comfortably. The main risks are scope, single-vendor dependency, and a value proposition that quietly assumes a discipline (a maintained catalog) that most organizations don't have.

**Good fit if:** you already run DataHub + Trino, your catalog is actually curated, and you have a team willing to own a substantial piece of infrastructure.
**Poor fit if:** your stack is Snowflake/Databricks, your catalog is decorative, or you want a small composable component rather than a platform.

**Before adopting, check:** the four-arm ablation for a "plain catalog tools, no enrichment" arm; distinct contributor count and issue response times in the repo; whether the built-in OAuth server can be disabled in favor of your existing IdP; what happens to a `trino_query` when DataHub is unreachable (degrade or fail?); whether there's any metadata TTL/staleness handling; and the actual token overhead of enrichment on a wide table.
