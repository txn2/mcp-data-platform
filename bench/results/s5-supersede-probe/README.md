# S5 supersede probe (pre-study premise probe)

Exploratory. The premise probe for a proposed supersede-reliability report: rerun of the #964 isolated supersede sub-benchmark at k=3 (30 protocol-runs, claude-cli sonnet, a3 arm against a dedicated database) to test whether report 1's wide supersede confidence intervals (duplicate rate CI [14, 86] on seven runs) concealed a mid-range defect.

They did not. Conditional on capture: superseded 8/8, duplicated 0/8, update correctness 8/8. The supersede mechanism is at ceiling and the report candidate died by the pre-stated decision rule.

The probe's real finding is one stage upstream. Strict capture ran 10/30, bimodal by protocol, and decomposes completely:

- ~11/30 captured but mis-filed: the fact was recorded and linked to the semantically nearest dataset (`daily_region_revenue`) instead of the canonical one (`orders`, `customers`). Deterministic per protocol — every replicate of the same protocol mis-filed the same way.
- ~5/30 never captured: the agent did the teach work and never called capture, despite the explicit instruction.
- 10/30 captured and filed as expected.

Mis-filing matters because promotion targets, URN-scoped delivery, and supersede matching all key on the linked entity; a mis-filed fact fails silently downstream, and it is a candidate mechanism for report 1's 46.7% cross-identity transfer.

Environment notes: fresh `mcp_bench_s5` database; DataHub quickstart restarted from its stopped containers (bench seed intact); Trino re-seeded; config variant of the a3 profile with only the DSN changed. Model and client in the manifest.
