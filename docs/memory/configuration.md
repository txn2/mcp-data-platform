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
  staleness:
    enabled: true
    interval: 15m             # How often to check for stale memories
    batch_size: 50            # Records per staleness check cycle
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `true` (when database available) | Enable the memory layer. Set `false` to explicitly disable. |
| `embedding.provider` | string | `noop` | Embedding provider: `ollama` for real embeddings, `noop` for zero vectors |
| `embedding.ollama.url` | string | `http://localhost:11434` | Ollama API base URL |
| `embedding.ollama.model` | string | `nomic-embed-text` | Ollama model name (768-dim) |
| `embedding.ollama.timeout` | duration | `30s` | HTTP timeout for embedding API calls |
| `staleness.enabled` | bool | `false` | Enable background staleness watcher |
| `staleness.interval` | duration | `15m` | Interval between staleness check cycles |
| `staleness.batch_size` | int | `50` | Number of records to check per cycle |

!!! note
    Memory requires `database.dsn` to be configured. Without a database, memory tools will not be registered.

## Persona Configuration

Memory tools (`memory_manage`, `memory_recall`) are opt-in. Add `memory_*` to a persona's `tools.allow` list:

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

When Ollama is unavailable, memory records are stored without embeddings and a warning is logged. Semantic recall (`memory_recall` with `semantic` or `auto` strategy) requires embeddings to function. Entity and graph recall strategies work without embeddings.

To set up Ollama:

```bash
# Install Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# Pull the embedding model
ollama pull nomic-embed-text
```

## Migration

Migration `000031_memory_records` creates the `memory_records` table with pgvector support. It automatically migrates existing data from the legacy `knowledge_insights` table and drops it.

The migration requires the pgvector PostgreSQL extension. For managed PostgreSQL services this is typically pre-installed. For self-hosted PostgreSQL:

```bash
# Ubuntu/Debian
sudo apt install postgresql-16-pgvector

# Or build from source
cd /tmp && git clone https://github.com/pgvector/pgvector.git
cd pgvector && make && sudo make install
```
