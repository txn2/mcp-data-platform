# {{project-name}}

{{project-description}}

## Quick Start

### Installation

```bash
go install github.com/{{github-org}}/{{project-name}}/cmd/{{project-name}}@latest
```

### Claude Code CLI

```bash
claude mcp add {{project-name}} -- {{project-name}}
```

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "{{project-name}}": {
      "command": "{{project-name}}",
      "env": {}
    }
  }
}
```

## Features

- Feature 1
- Feature 2
- Feature 3

## Next Steps

- [Installation Guide](getting-started/installation.md)
- [Configuration](getting-started/configuration.md)
- [Tools Reference](reference/tools.md)
