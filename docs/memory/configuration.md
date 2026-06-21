---
description: Configuration reference for the memory layer including embedding provider, staleness watcher, and persona tool access.
---

# Memory Configuration

## Config Reference

```yaml
memory:
  enabled: true
  embedding:
    provider: ollama          # "ollama" or "noop"
    ollama:
      url: "http://localhost:11434"
      model: "nomic-embed-text"
      timeout: 30s
      max_input_bytes: 6000     # cap per-text input before embedding (0 = default 6000)
  staleness:
    enabled: true
    interval: 15m             # How often to check for stale memories
    batch_size: 50            # Records per staleness check cycle
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `true` (when database available) | Enable the memory layer. Set `false` to explicitly disable. |
| `embedding.provider` | string | `noop` | Embedding provider: `ollama` for real embeddings. Anything else (including unset) selects the noop placeholder; memory writes persist with `Embedding: nil` and the apigateway embed-job queue refuses to start. Semantic features stay off until a real provider is wired. |
| `embedding.ollama.url` | string | `http://localhost:11434` | Ollama API base URL |
| `embedding.ollama.model` | string | `nomic-embed-text` | Ollama model name (768-dim) |
| `embedding.ollama.timeout` | duration | `30s` | HTTP timeout for embedding API calls |
| `embedding.ollama.max_input_bytes` | int | `6000` | Cap on the byte length of each text sent to Ollama. The platform truncates input itself (on a UTF-8 boundary) because Ollama's `truncate` flag is unreliable: content exceeding the model's context can return `400 the input length exceeds the context length` even with `truncate:true`. The default sits below `nomic-embed-text`'s ~2048-token boundary with margin. Raise it only for a larger-context model. Only the embedded text is trimmed; stored content is unaffected. |
| `staleness.enabled` | bool | `false` | Enable background staleness watcher |
| `staleness.interval` | duration | `15m` | Interval between staleness check cycles |
| `staleness.batch_size` | int | `50` | Number of records to check per cycle |

!!! note
    Memory requires `database.dsn` to be configured. Without a database, memory tools will not be registered.

## Persona Configuration

Memory tools (`memory_capture`, `memory_manage`) are opt-in. Add `memory_*` to a persona's `tools.allow` list (reading memory back is served by `search`):

```yaml
personas:
  definitions:
    analyst:
      tools:
        allow: ["trino_*", "datahub_*", "memory_*"]
    admin:
      tools:
        allow: ["*"]
```

## Embedding Provider

The memory layer generates 768-dimensional embeddings for semantic search using [Ollama](https://ollama.ai/) with the `nomic-embed-text` model.

When Ollama is unavailable, memory records are stored without embeddings and a warning is logged. Semantic recall (via `search`) requires embeddings to function; entity lookup and graph traversal work without embeddings.

### Unconfigured State

When `memory.embedding.provider` is unset or unrecognized, the platform substitutes a noop placeholder that returns zero vectors. This is the documented degraded state, not an error: the platform still boots so Trino, S3, DataHub, audit, OAuth, and every other non-embedding feature remains available.

In this state:

- Startup logs one `WARN` line naming `memory.embedding.provider` as the key to set.
- `GET /api/v1/admin/embedding/status` returns `{ "kind": "noop", "status": "unconfigured", ... }`.
- The portal renders an amber banner on the API Catalogs and Memory pages.
- Memory writes persist `Embedding: nil` (symmetric with the recall-side guard that refuses to vector-search zero vectors).
- The apigateway embed-job queue does not start, so spec saves do not produce zero-vector rows in `api_catalog_operation_embeddings`. Per-spec badges render "not indexed" honestly; `api_list_endpoints` falls back to lexical scoring.

To set up Ollama:

```bash
# Install Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# Pull the embedding model
ollama pull nomic-embed-text
```

## Migration

Migration `000031_memory_records` creates the `memory_records` table with pgvector support. It automatically migrates existing data from the legacy `knowledge_insights` table and drops it.

Migration `000054_memory_hybrid_search` adds hybrid recall: the `embedding_model` and `embedding_text_hash` breadcrumb columns (used by the index-jobs backfill consumer to dedup re-embeds and detect model-swap gaps), an `hnsw` ANN index on `embedding` (replaces the O(n) sequential cosine scan, requires pgvector >= 0.5.0), and a GIN index on `to_tsvector('english', content)` backing the lexical retrieval arm. No new configuration keys are introduced; the hybrid blend weight is fixed at 0.6 semantic / 0.4 lexical.

The migration requires the pgvector PostgreSQL extension. For managed PostgreSQL services this is typically pre-installed. For self-hosted PostgreSQL:

```bash
# Ubuntu/Debian
sudo apt install postgresql-16-pgvector

# Or build from source
cd /tmp && git clone https://github.com/pgvector/pgvector.git
cd pgvector && make && sudo make install
```
