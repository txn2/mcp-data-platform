# Configuration

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `EXAMPLE_VAR` | Example variable | `default` |

## Claude Code CLI

Add {{project-name}} as an MCP server:

```bash
claude mcp add {{project-name}} -- {{project-name}}
```

With environment variables:

```bash
claude mcp add {{project-name}} \
  -e EXAMPLE_VAR=value \
  -- {{project-name}}
```

## Claude Desktop

Add to your `claude_desktop_config.json` (find via Claude Desktop → Settings → Developer):

```json
{
  "mcpServers": {
    "{{project-name}}": {
      "command": "{{project-name}}",
      "env": {
        "EXAMPLE_VAR": "value"
      }
    }
  }
}
```

## Docker

```bash
docker run \
  -e EXAMPLE_VAR=value \
  ghcr.io/{{github-org}}/{{project-name}}:latest
```
