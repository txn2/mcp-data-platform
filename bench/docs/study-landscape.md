# External study landscape

The standing survey behind stage 0 of the study lifecycle
([`findings-register.md`](findings-register.md)): what the field has already
measured in the areas this benchmark program works in, so a candidate question
is checked against the outside world before anything is built. A literature gap
found here is motivation for a probe, never evidence of an effect.

Survey conducted 2026-08-08. Every entry's primary source URL was fetched
during the survey; a claim that could not be confirmed on a fetched page is
marked UNVERIFIED and must not be cited as established. Maintenance: a new
candidate gets a targeted pass over the relevant section plus a search for
anything newer, appended here with citations; a section is refreshed in full
only when its area has visibly moved.

## 1. MCP benchmarks and evaluations

| Benchmark | Source | What it measures | Relevant findings |
| --- | --- | --- | --- |
| MCPBench (Alibaba/ModelScope, 2025-04) | [arXiv:2504.11094](https://arxiv.org/abs/2504.11094) | Compares MCP servers (web search, DB query, GAIA) on accuracy, time, tokens under a fixed agent | Best server only 64% accuracy; a declarative interface substantially improves accuracy (input-side) |
| MCP-Universe (Salesforce, 2025-08) | [arXiv:2508.14704](https://arxiv.org/abs/2508.14704) | LLMs against 11 real servers, 6 domains, format/static/dynamic evaluators | GPT-5 43.72%, Claude-4.0-Sonnet 29.44%; names an "unknown-tools challenge"; documents long-context degradation as steps accumulate |
| LiveMCPBench (ICIP-CAS, 2025-08) | [arXiv:2508.01780](https://arxiv.org/abs/2508.01780) | 95 tasks over 70 servers / 527 tools; large-scale tool retrieval is the evaluated variable | Retrieval errors account for nearly half of all failures; but see the validity audit below — 18.9pp spread across 23 identical reruns |
| MCPEval (Salesforce, 2025-07) | [arXiv:2507.12806](https://arxiv.org/abs/2507.12806) | Methodology framework: automated task generation + trajectory verification + dual scoring | Qualitative; no discovery/memory/metadata coverage |
| MCP-Bench (Accenture, 2025-08) | [arXiv:2508.20453](https://arxiv.org/abs/2508.20453) | 20 LLMs, 28 live servers / 250 tools; fuzzy instructions without explicit tool names | Fuzzy-instruction tool retrieval and multi-hop trajectories are first-class; no knowledge layer |
| MCPToolBench++ (Ant Group, 2025-08) | [arXiv:2508.07575](https://arxiv.org/abs/2508.07575) | 1.5K QA pairs over a 4k+ server marketplace | Raw tool-use performance only (metric details beyond abstract UNVERIFIED) |
| MCP-AgentBench (2025-09) | [arXiv:2509.09734](https://arxiv.org/abs/2509.09734) | 33 servers, 188 tools, 600 queries, outcome-oriented scoring | No headline numbers in abstract |
| MCPVerse (2025-08) | [arXiv:2508.16260](https://arxiv.org/abs/2508.16260) | 550+ real tools, action space over 140k tokens; Oracle/Standard/Max-Scale modes isolate tool-count effects | Most models degrade as the tool set grows; Claude-4-Sonnet improved with the expanded action space — cleanest published tool-count-scaling manipulation |
| MCPMark (2025-09) | [arXiv:2509.24002](https://arxiv.org/abs/2509.24002) | 127 CRUD-heavy tasks, programmatic state verification, pass@1/pass^4 | gpt-5-medium 52.56% pass@1 falls to 33.86% pass^4 — repetition exposes reliability; write-path coverage rare elsewhere |
| LiveMCP-101 (2025-08) | [arXiv:2508.15760](https://arxiv.org/abs/2508.15760) | 101 queries needing coordinated multi-tool use; parallel reference agent handles live-tool drift | Frontier LLMs below 60%; seven-failure-mode taxonomy |
| MCPAgentBench (2025-12, distinct from the 2025-09 one) | [arXiv:2512.24565](https://arxiv.org/abs/2512.24565) | 178 tasks distilled from real MCP tool definitions, sandbox with distractor tools | Distractor-tool discrimination as a direct tool-selection manipulation |
| RAG-MCP (2025-05) | [arXiv:2505.03275](https://arxiv.org/abs/2505.03275) | Semantic retrieval of tool descriptions before selection, stress test over growing tool counts | Tool selection 43.13% vs 13.62% enumeration baseline — the canonical discovery-vs-enumeration result |
| MCP-Zero (2025-06) | [arXiv:2506.01056](https://arxiv.org/abs/2506.01056) | Agent-initiated tool requests + hierarchical routing over 308 servers / 2,797 tools | 98% token reduction at maintained accuracy; closest published analogue to a server-side search-first design |
| "How Many Tools Should an LLM Agent See?" (2026-05) | [arXiv:2605.24660](https://arxiv.org/abs/2605.24660) | Optimal shortlist depth, chance-corrected; BFCL + ToolBench | Adaptive ~7-tool lists match fixed 50-tool coverage; the enumeration penalty is quantified and query-dependent |
| "From Tool Orchestration to Code Execution" (2026-02) | [arXiv:2602.15945](https://arxiv.org/abs/2602.15945) | Context-coupled vs code-execution MCP designs; 16 attack classes | Code execution cuts tokens/latency but enlarges attack surface |
| DynamicMCPBench (2026-07) | [arXiv:2607.20531](https://arxiv.org/abs/2607.20531) | Path-agnostic effect checkpoints on live servers; 24 models, 121 servers, 750 tasks, pass³ | Best agents ~50%; accuracy falls 39% → 13% from shortest to longest tool chains — strongest published multi-hop degradation number |
| MCPEvol-Bench (2026-07) | [arXiv:2607.14642](https://arxiv.org/abs/2607.14642) | Robustness to server evolution via 11 mutation operators | Frontier models drop ~14% on evolved servers |
| "Benchmarking the Benchmarks" validity audit (2026-06) | [arXiv:2607.02577](https://arxiv.org/abs/2607.02577) | Audit of BFCL v4, τ²-Bench, LiveMCPBench, MCP-Atlas; 496 expert-reviewed tasks | 18.5% evaluator-human misalignment; LiveMCPBench scored 57.9%–76.8% across 23 identical reruns; failure taxonomies for deterministic and LLM-judge scoring |
| "MCP at First Glance" (2025-06) | [arXiv:2506.13538](https://arxiv.org/abs/2506.13538) | Mining 1,899 open-source MCP servers | 7.2% with vulnerabilities, 5.5% tool poisoning; ecosystem-quality baseline |
| AgentSM (2026-01) | [arXiv:2601.15709](https://arxiv.org/abs/2601.15709) | Text-to-SQL agent capturing execution traces as reusable "semantic memory"; Spider 2.0 / BIRD | 44.8% execution accuracy on Spider 2.0 Lite; closest published work to a knowledge layer improving a data agent — but framework-internal, not a platform service |

Surfaced but not fetched (UNVERIFIED): MCP-SafetyBench
([arXiv:2512.15163](https://arxiv.org/abs/2512.15163)), MCPSecBench
([arXiv:2508.13220](https://arxiv.org/pdf/2508.13220)), remote-server
authentication measurement ([arXiv:2605.22333](https://arxiv.org/abs/2605.22333)),
MCPZoo ([arXiv:2607.11086](https://arxiv.org/abs/2607.11086)), MCP-Atlas
(known only via the validity audit), PlanBench-XL
([arXiv:2606.22388](https://arxiv.org/html/2606.22388v1)).

## 2. Agent memory: benchmarks and systems

| Work | Source | What it measures | Relevant findings |
| --- | --- | --- | --- |
| LoCoMo (2024-02) | [arXiv:2402.17753](https://arxiv.org/abs/2402.17753) | QA + summarization over very-long multi-session conversations | Pure read-side recall; long-range temporal/causal reasoning lags humans |
| LongMemEval (ICLR 2025) | [arXiv:2410.10813](https://arxiv.org/abs/2410.10813) | 500 questions in scalable chat histories: extraction, multi-session and temporal reasoning, knowledge updates, abstention | ~30% accuracy drop under sustained interaction; knowledge-updates and abstention are the closest classic categories to staleness |
| MemGPT (2023-10) | [arXiv:2310.08560](https://arxiv.org/abs/2310.08560) | OS-style virtual context with memory tiers | Landmark for agent-initiated capture (self-directed paging); evaluates downstream success, not the write decisions |
| A-MEM (NeurIPS 2025) | [arXiv:2502.12110](https://arxiv.org/abs/2502.12110) | Zettelkasten-style linked notes; new writes trigger updates to existing memories | Direct hit for write-time consolidation — but system-side automatic, and consolidation quality itself is unscored; link-following depth at read time unmeasured |
| Mem0 (2025-04) | [arXiv:2504.19413](https://arxiv.org/abs/2504.19413) | Extract-consolidate-retrieve pipeline (+graph variant) on LoCoMo | 26% relative judge improvement over OpenAI memory; extraction is system-side; methodology disputed by Letta (below) |
| HippoRAG (NeurIPS 2024) | [arXiv:2405.14831](https://arxiv.org/abs/2405.14831) | KG + Personalized PageRank single-step retrieval | Up to +20% multi-hop QA, 10-30x cheaper than iterative retrieval — graph structure substitutes for deep traversal |
| MemoryBank (2023-05) | [arXiv:2305.10250](https://arxiv.org/abs/2305.10250) | Memory store with Ebbinghaus-style forgetting | Decay designed in but never benchmarked as an outcome |
| Zep (2025-01) | [arXiv:2501.13956](https://arxiv.org/abs/2501.13956) | Temporal KG agent memory | Up to +18.5% on LongMemEval; vendor-run; temporal edge validity is the staleness-relevant mechanism |
| MemBench (ACL 2025 Findings) | [arXiv:2506.21605](https://arxiv.org/abs/2506.21605) | Factual and reflective memory levels across scenarios | Reflective level is consolidation-adjacent; no capture-decision or tier analysis |
| MemoryAgentBench (2025-07) | [arXiv:2507.05257](https://arxiv.org/abs/2507.05257) | Four competencies incl. selective forgetting over incremental multi-turn feeding | No current method masters all four; selective forgetting is the closest measurement of write/expiry policy quality |
| LongMemEval-V2 (2026-05) | [arXiv:2605.12493](https://arxiv.org/abs/2605.12493) | Memory over agentic work trajectories (115M tokens): state recall, workflow knowledge, premise awareness | Trajectory-store + coding-agent evidence gathering 72.5% vs RAG 48.5%; first benchmark of memory over work rather than chat; still read-side |
| STALE (2026-05) | [arXiv:2605.06527](https://arxiv.org/abs/2605.06527) | Can agents know their memories are no longer valid — 400 implicit-conflict scenarios | Best model 55.2%; recognizing an outdated memory does not imply applying the update; cascading invalidation hardest |
| Supersede (2026-06) | [arXiv:2606.27472](https://arxiv.org/abs/2606.27472) | Write-side memory-update gap on LongMemEval's update subset | Self-maintained memory drops frontier accuracy 92% → 77%; longer conversations 68% → 28% with no recovery from bigger budgets; single-author, small n |
| MemEvoBench (2026-04) | [arXiv:2604.15774](https://arxiv.org/abs/2604.15774) | Behavioral drift from repeated misleading exposure via the agent's own memory-evolution loop | Substantial safety degradation under biased updates; static prompt defenses insufficient |
| AgentMemBench (2026-06) | [arXiv:2608.00009](https://arxiv.org/html/2608.00009) | Head-to-head of five storage-architecture classes on shared corpora | External key-value dominates every quality axis; architecture is always the experimenter's condition, never the agent's choice |
| RefMem-Bench (2026-05) | [arXiv:2606.01223](https://arxiv.org/abs/2606.01223) | 26,000 QA instances requiring synthesis of fragmented cues | Measures synthesis at read time, not consolidation at write time |
| Memory-R1 (2025-08) | [arXiv:2508.19828](https://arxiv.org/abs/2508.19828) | RL-trained memory manager with ADD/UPDATE/DELETE/NOOP operations, 3B-14B scales | The write decision is the trained behavior — but by a dedicated manager, not the working agent |
| Letta leaderboard (ongoing, vendor) | [letta.com](https://www.letta.com/blog/letta-leaderboard/) | Fixed framework, varied model, memory read/write/update tasks with penalties for unnecessary operations | Only evaluation found scoring agent-initiated write/update behavior per se, with tier deltas: Claude Haiku 3.5 over-uses memory operations |
| Letta filesystem post (vendor) | [letta.com](https://www.letta.com/blog/benchmarking-ai-agent-memory/) | Plain filesystem tools over conversation files on LoCoMo | 74.0% with GPT-4o-mini beats Mem0's reported 68.5% graph variant; argues isolated-retrieval memory benchmarks are weak evidence |

Surfaced but not fetched (all UNVERIFIED): GateMem
([arXiv:2606.18829](https://arxiv.org/abs/2606.18829), multi-agent shared-memory
write contention), MemoryGraft (2512.16962), EvoMemBench (2605.18421), PERMA
(2603.23231), MemSyco-Bench (2607.01071), MemDelta (2606.29914), "Don't Ask the
LLM to Track Freshness" (2606.01435).

## 3. Linked knowledge and traversal

| Work | Source | What it measures | Relevant findings |
| --- | --- | --- | --- |
| GraphRAG (Microsoft, 2024-04) | [arXiv:2404.16130](https://arxiv.org/abs/2404.16130) | Entity KG + community summaries vs vanilla RAG for global sensemaking | Wins on global questions; no depth/connectivity numbers in abstract |
| GraphRAG-Bench (Tencent+PolyU, 2025-06) | [arXiv:2506.02404](https://arxiv.org/abs/2506.02404) | 9 GraphRAG methods on multi-hop college-level questions | Pipeline-wide scoring incl. reasoning coherence |
| "When to use Graphs in RAG" (ICLR 2026) | [arXiv:2506.05690](https://arxiv.org/abs/2506.05690) | RAG vs GraphRAG across four task types | GraphRAG wins complex reasoning, vanilla RAG wins simple facts; token overhead up to ~376x; denser constructed graphs (HippoRAG2 node degree 8.75–13.31 vs 1.48–5.50) correlate with better performance — the only quantified links-per-node result found, and density is a byproduct, not a controlled variable |
| RAG vs GraphRAG systematic evaluation (2025-02) | [arXiv:2502.11371](https://arxiv.org/abs/2502.11371) | Unified-framework comparison | Distinct strengths per task; hybrids beat both |
| RAGSearch (2026-04) | [arXiv:2604.09666](https://arxiv.org/abs/2604.09666) | Dense RAG vs GraphRAG inside agentic search loops at fixed budget | Agentic iteration substantially closes the gap — iterative traversal partially substitutes for explicit graph structure |
| WildGraphBench (2026-02) | [arXiv:2602.02053](https://arxiv.org/abs/2602.02053) | GraphRAG over citation-linked wild corpora at three question levels | Helps multi-fact aggregation; hurts summarization detail |
| HippoRAG 2 (ICML 2025) | [arXiv:2502.14802](https://arxiv.org/abs/2502.14802) | Factual, sense-making, associative memory tasks | +7% associative over SOTA embedding retrieval at low cost |
| RAPTOR (ICLR 2024) | [arXiv:2401.18059](https://arxiv.org/abs/2401.18059) | Recursive summary tree, retrieval across abstraction levels | +20% absolute on QuALITY with GPT-4; per-level traversal usage unmeasured |
| MemTree (2024-10) | [arXiv:2410.14052](https://arxiv.org/abs/2410.14052) | Online dynamic tree-structured memory | Helps where structure matters; traversal usage unmeasured |
| Think-on-Graph (ICLR 2024) | [arXiv:2307.07697](https://arxiv.org/abs/2307.07697) | LLM beam search over Freebase/Wikidata | Most direct depth ablation found: depth/width swept 1–4, gains diminish beyond depth 3 (few questions need deeper chains); GPT-4 consistently above ChatGPT, and the scaffold lets weaker LLMs partially close the gap |
| Graph-CoT / GRBench (ACL 2024 Findings) | [arXiv:2404.07103](https://arxiv.org/abs/2404.07103) | 1,740 questions over 10 domain graphs, difficulty stratified by hop count | Iterative agent loop beats RAG/CoT across three backbones (per-difficulty numbers UNVERIFIED); connectivity never varied |
| Graph Counselor (ACL 2025) | [arXiv:2506.03939](https://arxiv.org/abs/2506.03939) | Multi-agent adaptive graph exploration with self-reflection | Explicitly targets dynamic depth adjustment vs preset schemes |
| SLM exploration modules for KGQA (2025-09) | [arXiv:2509.07399](https://arxiv.org/abs/2509.07399) | Whether small-model KGQA failure is a traversal failure | Clearest tier-vs-traversal result: traversal breaks down specifically for small models; offloading exploration to simple modules recovers much of the gap |
| Structure-content trade-off (2025-06) | [arXiv:2506.13380](https://arxiv.org/abs/2506.13380) | Question decomposition vs retrieved-subgraph connectedness | Precision-vs-connectivity trade-off in the retrieved subgraph |
| CatRAG (2026-02) | [arXiv:2602.01965](https://arxiv.org/abs/2602.01965) | Query-aware dynamic edge weighting for graph walks | Names hub-node diversion as a failure mode — node degree distribution shapes traversal outcomes |
| LLM-WikiRace (2026-02) | [arXiv:2602.16902](https://arxiv.org/html/2602.16902v4) | Navigate Wikipedia links source-to-target within 30 steps, 549K-page snapshot | Closest existing controlled link-traversal benchmark: success collapses 96% → 29% from 3–4 to 7–8 required hops; looping in 66% of medium/hard trajectories predicts failure; hub-seeking is a deliberate frontier strategy; reasoning-trained models beat instruction-tuned peers by up to 20 points at matched world knowledge; models under 8B near zero on hard |
| WebArena (2023-07) | [arXiv:2307.13854](https://arxiv.org/abs/2307.13854) | 812 long-horizon tasks on self-hosted sites | Best GPT-4 agent 14.41% vs human 78.24% at the time; no per-depth breakdown |
| BrowseComp (OpenAI, 2025-04) | [arXiv:2504.12516](https://arxiv.org/abs/2504.12516) | 1,266 hard-to-find questions requiring persistent browsing | Accuracy scales smoothly with browsing effort; Deep Research 51.5% vs humans 29.2%; 91% calibration error — browsing inflates confidence in wrong answers |
| DeepWideSearch (2025-10) | [arXiv:2510.20168](https://arxiv.org/abs/2510.20168) | Tasks requiring depth and width simultaneously | SOTA agents average 2.39%; "insufficient retrieval (stopping too shallow)" named as a failure mode but stopping depth not quantified |
| WebDetective (2025-10) | [arXiv:2510.05137](https://arxiv.org/abs/2510.05137) | Hint-free multi-hop in a controlled Wikipedia sandbox, 25 models; factorizes search sufficiency vs knowledge utilization vs refusal | Models fail at utilization even with sufficient evidence; near-zero appropriate refusal; agents "excel at following prescribed paths but fail when required to discover them" |
| Chroma chunking evaluation (2024-07, vendor) | [trychroma.com](https://www.trychroma.com/research/evaluating-chunking) | Chunking strategies at token-level recall/precision | Up to 9-point recall spread between strategies; all results are embedding-retrieval, not agent reading |
| AI21 query-dependent chunking (2026-01, vendor) | [ai21.com](https://www.ai21.com/blog/query-dependent-chunking/) | Recall across chunk sizes 50–2000 tokens | No single size dominates; per-query oracle selection has 20–40% headroom over any fixed size |
| Mix-of-Granularity (COLING 2025) | [arXiv:2406.00456](https://arxiv.org/abs/2406.00456) | Trained per-query granularity router | Optimal granularity is query-dependent |

UNVERIFIED chunk-size items: a Springer chapter (10.1007/978-3-032-00712-4_21)
and a "Vecta Feb 2026 benchmark", both known only from search summaries.

## 4. Tool-use benchmark methodology

| Work | Source | Methodology lesson |
| --- | --- | --- |
| τ-bench (Sierra, 2024-06) | [arXiv:2406.12045](https://arxiv.org/abs/2406.12045) | State-based grading (database diff) over text matching; pass^k exposes reliability pass@1 hides (pass^8 below 25% in retail) |
| τ²-bench (Sierra, 2025-06) | [arXiv:2506.07982](https://arxiv.org/abs/2506.07982) | Dual-control (user holds tools too) re-opened headroom after retail began saturating; constraining the user simulator to environment state reduces a confound |
| BFCL (ICML 2025) | [PMLR 267](https://proceedings.mlr.press/v267/patil25a.html) | Single-turn AST scoring saturated at the top; multi-turn state-based evaluation plus abstention cases restored spread |
| ToolLLM/ToolBench (2023-07) | [arXiv:2307.16789](https://arxiv.org/abs/2307.16789) | Scale of real APIs made selection hard; LLM-judge scoring plus live unstable APIs made scores noisy |
| API-Bank (EMNLP 2023) | [arXiv:2304.08244](https://arxiv.org/abs/2304.08244) | Runnable APIs with annotated ground truth over judge-scored text |
| ToolSandbox (Apple, 2024-08) | [arXiv:2408.04682](https://arxiv.org/abs/2408.04682) | Implicit state dependencies between tools are what discriminate once static single-call scoring stops doing so |
| AgentBench (ICLR 2024) | [arXiv:2308.03688](https://arxiv.org/abs/2308.03688) | Environment diversity exposes that models fail in different environments for different reasons |
| SWE-bench (ICLR 2024) | [arXiv:2310.06770](https://arxiv.org/abs/2310.06770) | Execution-based grading on real repos gave enormous initial headroom (Claude 2: 1.96%) |
| GAIA (2023-11) | [arXiv:2311.12983](https://arxiv.org/abs/2311.12983) | Exact-match short answers avoid judge noise; "human-easy, AI-hard" targeting kept it discriminating for years |
| SWE-Bench Illusion (2025-06) | [arXiv:2506.12286](https://arxiv.org/abs/2506.12286) | Contamination probe: 76% buggy-file identification from issue text alone on Verified vs 53% off-benchmark — leaderboard scores can reflect memorization |
| SWE-Bench+ (2024-10) | [arXiv:2410.06992](https://arxiv.org/abs/2410.06992) | 32.67% of passing patches had solution leakage, 31.08% passed on weak tests; filtering drops SWE-Agent+GPT-4 from 12.47% to 3.97% |
| LiveCodeBench (2024-03) | [arXiv:2403.07974](https://arxiv.org/abs/2403.07974) | Release-date-windowed rolling evaluation is the canonical contamination handle |
| Log-analysis critique of τ-bench (2026-05) | [arXiv:2605.08545](https://arxiv.org/abs/2605.08545) | 25 of 50 τ-bench Airline tasks flawed; outcome-only reporting masks whether the agent, simulator, or golden answer failed — log audits required |
| Lost in Simulation (2026-01) | [arXiv:2601.17087](https://arxiv.org/html/2601.17087) | Agent success varies up to 9 points by which LLM simulates the user; the simulator is itself a model under test |

Recurring discrimination levers: state- or execution-based grading over text
similarity; statefulness with implicit dependencies; repeated-trial reliability
metrics (pass^k); rolling post-cutoff data. Recurring saturation causes:
solution leakage, memorization/contamination, weak oracles, flawed golden
tasks, and unmodeled simulator variance. This program's own instance of the
saturation failure is recorded in the register's #1027 row.

## 5. Knowledge conflicts, misinformation, and poisoned stores

| Work | Source | What it measures | Relevant findings |
| --- | --- | --- | --- |
| PoisonedRAG (USENIX Sec 2025) | [arXiv:2402.07867](https://arxiv.org/abs/2402.07867) | Optimization-based corruption of a RAG corpus | 90% attack success with 5 malicious texts per question in million-document corpora; generators do not verify retrieved content |
| AgentPoison (2024-07) | [arXiv:2407.12784](https://arxiv.org/abs/2407.12784) | Backdoor triggers retrieving poisoned demonstrations from agent memory/KB | Over 80% success at under 0.1% poison rate, under 1% benign degradation |
| MINJA (2025-03) | [arXiv:2503.03704](https://arxiv.org/abs/2503.03704) | Corrupting shared agent memory purely through normal queries | Cross-session, cross-user propagation with no verification at retrieval time (headline percentages UNVERIFIED beyond abstract) |
| Memory poisoning replication (2026-01) | [arXiv:2601.05504](https://arxiv.org/abs/2601.05504) | MINJA under realistic conditions | Pre-existing legitimate memories substantially reduce attack effectiveness — store priors modulate adoption |
| Flooding spread in multi-agent communities (2024-07) | [arXiv:2407.07791](https://arxiv.org/abs/2407.07791) | Manipulated knowledge spreading through benign agent communication | Spreads during normal communication and persists via chat-history memory, re-infecting after the attacker leaves |
| Faulty-agent resilience (2024-08) | [arXiv:2408.00989](https://arxiv.org/abs/2408.00989) | One corrupted agent across six collaboration structures | Agents do not spontaneously challenge faulty peers; imposed review roles recover up to 96.4% of errors — verification is architectural, not behavioral |
| ClashEval (2024-04) | [arXiv:2404.10198](https://arxiv.org/abs/2404.10198) | Prior vs perturbed retrieved evidence | Models adopt incorrect retrieved content over 60% of the time, overriding correct priors; adoption falls as perturbations get less plausible |
| Adaptive Chameleon or Stubborn Sloth (ICLR 2024) | [arXiv:2305.13300](https://arxiv.org/abs/2305.13300) | Receptivity to coherent counter-memory | Adoption is driven by coherence, not truth; confirmation bias when both supporting and conflicting evidence present |
| Sycophancy (Anthropic, 2023-10) | [arXiv:2310.13548](https://arxiv.org/abs/2310.13548) | Sycophancy across five SOTA assistants | Uniform across frontier assistants — capability did not confer skepticism; preference optimization implicated |
| EchoMist (EMNLP 2025) | [arXiv:2503.09598](https://arxiv.org/abs/2503.09598) | Detecting false premises embedded as assumptions | Explicit tier effect: 8B → 70B scaling improves detection; targeted training (Tulu-3) adds over 12 points; verification collapses for post-cutoff claims (over 70% reinforcement) |
| MisBench (ACL 2025) | [aclanthology.org](https://aclanthology.org/2025.acl-long.674/) | 10.3M misinformation instances across conflict types and styles | Susceptibility persists under stylistic reframing of the same false claim (tier numbers UNVERIFIED) |

Convergent picture: models at every tested tier adopt plausible wrong context
by default; skepticism improves with scale and targeted training but does not
emerge for free; working defenses are architectural, not behavioral. This
program's knowledge-pollution report adds a piece not measured elsewhere:
adoption of a wrong claim that arrived through a governed curation gate, with
tier- and class-graded separation.

## 6. Gaps adjacent to this program

Each gap below was stated by a surveying pass after fetching the primary
sources above, and is tied to the candidate or study it informs. A gap is a
reason to run a probe, not evidence the effect exists.

1. **Cross-session payoff of agent knowledge capture.** Every fetched MCP
   benchmark treats tasks as memoryless; memory benchmarks evaluate recall,
   not tool-use outcomes. The published knowledge-layer series sits alone
   here.
2. **Discretionary capture during real work.** Nothing scores whether a
   task-performing agent spontaneously decides to write knowledge, or the
   precision/recall of those decisions against capture-worthiness ground
   truth. Letta's write tasks are instructed; Memory-R1's writer is a
   dedicated manager. Informs the capture candidate (gated on #1136).
3. **Sink selection.** No benchmark presents multiple legitimate storage
   destinations and scores whether the write landed in the right one;
   storage architecture is always the experimenter's condition. Informs the
   capture-and-sink candidate.
4. **Write-time consolidation quality.** A-MEM consolidates but never scores
   the merge; STALE and Supersede measure failure to update, not the quality
   of a performed synthesis. Informs any future synthesis candidate — which
   still needs its platform mechanism named before it enters the ledger.
5. **Response-side semantic enrichment.** MCP work is input-side (tool
   descriptions, declarative interfaces); no benchmark varies catalog context
   in tool responses and measures downstream accuracy.
6. **Catalog-grounded data discovery.** Tool discovery is well studied at the
   tool-description level; discovering data (tables, datasets) through a
   catalog and acting on it has no benchmark coverage.
7. **Link density as a controlled variable.** The only connectivity data is
   observational (constructed-graph density correlates with GraphRAG scores;
   hub-node diversion named as a failure mode). Nobody builds the same
   content at low/medium/high link density and compares. Informs the
   graph-traversal candidate.
8. **Voluntary stopping depth.** Depth-conditioned success exists
   (LLM-WikiRace: 96% → 29% as required hops grow); how deep an agent
   *chooses* to traverse before giving up is nowhere the dependent variable.
   Informs the graph-traversal candidate.
9. **Page size for reading agents.** All chunk-size results are
   embedding-retrieval recall; none measure how page size affects an agent's
   navigation decisions or end-task accuracy when it reads pages and follows
   references. Informs the graph-traversal candidate.
10. **Tier × depth × structure on wiki-style notes.** Tier-stratified
    traversal exists for Wikipedia and for formal KGs (where traversal is
    precisely where small models break down); the space between typed
    triples and rendered web pages — interlinked authored notes — is empty.
    Informs the graph-traversal candidate and matches this program's
    tier-inversion findings.

    Gaps 7 to 10 are now partially measured by the graph-completion study
    (`docs/reference/benchmark-report-graph-completion.md`, 2026-08-10;
    register row in [`findings-register.md`](findings-register.md)), after a
    first lookup-shaped probe could not speak to them (its instrument-defect
    row remains in the register). What is now measured: gap 8's quantity
    exists — voluntary traversal depth with the agent choosing when to stop,
    read as grounded closure coverage: full-depth walks at 0.96/0.42 across
    two reading budgets on 42 pages and 1.00 at 5000 pages with search
    removed, so voluntary traversal is scale-invariant around a closure. Gap
    10's empty space — interlinked authored notes, between typed triples and
    rendered web pages — now has controlled readings: the same content with
    edges present versus rendered to prose, at three corpus scales, with the
    result that edges buy search cost (flat versus roughly doubling across
    two orders of magnitude) and search-absent robustness, not coverage; the
    study deliberately replaced tier framing with reading-budget-to-corpus
    framing, so gap 10's tier axis is reframed rather than filled. Gap 7
    (density as a varied axis) remains unmeasured: the study fixed
    EdgeDensity at 3 and varied presence versus absence, the axis's
    endpoints, and its result — coverage at ceiling in every cell — says a
    density sweep at these scales would re-measure the ceiling. Gap 9 (page
    size for reading agents) remains unmeasured, held constant by design.
    The study also recorded a boundary the gap list did not anticipate: an
    "unreachable by search" certification sampling task-derived phrasings is
    defeated by read-derived queries, so any future design posing gap-style
    reachability questions must certify against what the agent reads, not
    only what the task says.
11. **Reproducibility discipline.** The flagship MCP discovery benchmark
    shows an 18.9-point spread across identical reruns; any new measurement
    needs repeated trials (pass^k-style) and pre-stated decision rules —
    already this program's practice, now with external corroboration.
