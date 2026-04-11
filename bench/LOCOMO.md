# LOCOMO Benchmark

Run the [LOCOMO](https://snap-research.github.io/locomo/) (Long-term Conversational Memory) benchmark against the mcp-data-platform memory layer using the `memory_manage` and `memory_recall` MCP tools.

LOCOMO is the standard benchmark for evaluating long-term conversational memory in agent systems. Published at ACL 2024, it provides 10 multi-session conversations with 7,512 QA pairs across five categories: single-hop, multi-hop, temporal reasoning, open-domain, and adversarial.

## Prerequisites

- A running mcp-data-platform instance with memory enabled (PostgreSQL + pgvector + Ollama)
- An MCP client that can call `memory_manage` and `memory_recall` tools
- The LOCOMO dataset
- An LLM for answer generation and judging (any model accessible to your MCP client)

## Dataset

Download `locomo10.json` from the official repository:

```bash
curl -L -o locomo10.json \
  https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json
```

The file contains 10 conversations. Each has:
- Multiple sessions with timestamps (`session_N`, `session_N_date_time`)
- Dialog turns with speaker, `dia_id`, and text
- QA pairs with question, answer, category (1-5), and evidence dialog IDs

### QA Categories

| Category | Name | Count | What it tests |
|----------|------|-------|---------------|
| 1 | Single-hop | 2,705 | Direct recall from one session |
| 2 | Multi-hop | 1,104 | Synthesizing across sessions |
| 3 | Temporal | 1,547 | Time-based reasoning |
| 4 | Open-domain | 285 | Conversation + world knowledge |
| 5 | Adversarial | 1,871 | Correctly rejecting unanswerable questions |

## Procedure

### 1. Ingest conversations

For each conversation in `locomo10.json`, iterate over sessions and turns. Insert each turn as a memory record using `memory_manage`:

```json
{
  "command": "remember",
  "content": "[Speaker, Session date] Turn text here...",
  "dimension": "<knowledge|event|preference|relationship>",
  "category": "general",
  "confidence": "high",
  "source": "automation",
  "metadata": {
    "dia_id": "D1:3",
    "conversation_id": "0",
    "session_id": "1",
    "speaker": "Caroline",
    "locomo_benchmark": true
  }
}
```

**Dimension classification** (simple keyword heuristics):
- Text mentions dates, "yesterday", "last week", "ago", "schedule" -> `event`
- Text contains "prefer", "like", "love", "hate", "favorite", "want to" -> `preference`
- Text mentions "married", "brother", "sister", "friend", "colleague", "partner" -> `relationship`
- Default -> `knowledge`

**Content formatting**: Prefix each turn with `[Speaker, Session date]` to provide temporal context. This also ensures the content meets the 10-byte minimum.

**Tracking**: Record the mapping of `dia_id` to memory record ID for retrieval recall scoring.

### 2. Evaluate QA pairs

For each QA pair, query the memory layer and generate an answer:

1. **Retrieve**: Call `memory_recall` with the question as a semantic query:
   ```json
   {
     "query": "Where does Bob prefer to travel?",
     "strategy": "semantic",
     "limit": 10
   }
   ```

2. **Generate**: Pass the retrieved memories as context to the LLM along with the question. System prompt:
   > You are answering questions about conversations between two people. Use ONLY the provided context to answer. If the context does not contain enough information, say "I cannot determine the answer from the available context." Be concise and factual.

3. **Score**: Compare the generated answer against the ground truth using:
   - **Token F1**: Lowercase both strings, split on whitespace/punctuation, compute precision/recall/F1
   - **LLM judge**: Ask the LLM to compare the generated answer against ground truth and reply "CORRECT" or "WRONG" with reasoning
   - **Retrieval recall@k**: What fraction of the evidence `dia_id`s appear in the retrieved memory records

### 3. Report

Aggregate results by category:
- Mean Token F1 per category and overall
- LLM judge accuracy per category and overall
- Mean retrieval recall@k

### 4. Cleanup

After the run, archive all benchmark records:

```json
{
  "command": "list",
  "filter_category": "general",
  "limit": 100
}
```

Filter for records with `locomo_benchmark: true` in metadata and call `forget` on each.

## Scoring reference

Published LOCOMO scores (2026) for comparison:

| System | LOCOMO LLM-Judge | Notes |
|--------|-----------------|-------|
| Mem0 | 67.1% | ECAI 2025 paper |
| Zep (corrected) | 75.1% | Zep rebuttal to Mem0 benchmark |
| Letta | ~83.2% | |
| Full context (GPT-3.5-16K) | 37.8% | Catastrophic on adversarial (2.1%) |
| RAG + Observations (top-5) | 41.4% | Strong on adversarial (44.7%) |
| Human baseline | 87.9% | |

## Alternative benchmarks

For more comprehensive evaluation, consider also running:

- **[LongMemEval](https://github.com/xiaowu0162/longmemeval)** (ICLR 2025) — 500 questions at up to 1.5M token scale, tests knowledge updates and abstention
- **[LoCoMo-Plus](https://arxiv.org/abs/2602.10715)** (Feb 2026) — extends LOCOMO with cognitive memory (causal, state, goal, value constraints)

Both can be run against the same `memory_manage`/`memory_recall` tools using the same procedure adapted for their dataset formats.

## Automating the benchmark

The benchmark can be automated with any scripting language that can call MCP tools. The procedure is:

1. Parse `locomo10.json`
2. Insert turns via `memory_manage` (remember)
3. For each QA pair: `memory_recall` (semantic) -> format context -> LLM generate -> score
4. Aggregate and report
5. Cleanup via `memory_manage` (forget)

No code needs to live in this repository. A Python script, a Claude Code session, or any MCP client can execute the full benchmark against a running instance.
