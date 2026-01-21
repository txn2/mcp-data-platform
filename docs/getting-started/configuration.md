# Configuration

## Configuration File

mcp-data-platform uses YAML configuration with environment variable expansion (`${VAR_NAME}`).

Create a `platform.yaml` file:

```yaml
server:
  name: mcp-data-platform
  transport: stdio

auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
  api_keys:
    enabled: true
    keys:
      - key: "${API_KEY_ADMIN}"
        name: "admin"
        roles: ["admin"]

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_*", "datahub_*"]
        deny: ["*_delete_*"]
    admin:
      display_name: "Administrator"
      roles: ["admin"]
      tools:
        allow: ["*"]
  default_persona: analyst

semantic:
  provider: datahub
  cache:
    enabled: true
    ttl: 5m

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

database:
  dsn: "${DATABASE_URL}"
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string for audit logs | - |
| `API_KEY_ADMIN` | Admin API key (if using API key auth) | - |

## Claude Code CLI

Add mcp-data-platform as an MCP server:

```bash
claude mcp add mcp-data-platform -- mcp-data-platform
```

With a configuration file:

```bash
claude mcp add mcp-data-platform \
  -- mcp-data-platform --config /path/to/platform.yaml
```

## Claude Desktop

Add to your `claude_desktop_config.json` (find via Claude Desktop → Settings → Developer):

```json
{
  "mcpServers": {
    "mcp-data-platform": {
      "command": "mcp-data-platform",
      "args": ["--config", "/path/to/platform.yaml"],
      "env": {
        "DATABASE_URL": "postgres://user:pass@localhost/audit",
        "API_KEY_ADMIN": "your-api-key"
      }
    }
  }
}
```

## Docker

```bash
docker run \
  -e DATABASE_URL=postgres://user:pass@host/audit \
  -v /path/to/platform.yaml:/etc/mcp/platform.yaml \
  ghcr.io/txn2/mcp-data-platform:latest \
  --config /etc/mcp/platform.yaml
```

## Command Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--config` | Path to configuration file | - |
| `--transport` | Transport type (stdio, sse) | stdio |
| `--address` | Server address for SSE transport | :8080 |
| `--version` | Show version and exit | - |
