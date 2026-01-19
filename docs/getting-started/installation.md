# Installation

## Prerequisites

- Go 1.24 or later (for building from source)
- An MCP-compatible client (Claude Desktop, Claude Code, etc.)

## Installation Methods

### Go Install (Recommended)

```bash
go install github.com/{{github-org}}/{{project-name}}/cmd/{{project-name}}@latest
```

### Homebrew (macOS)

```bash
brew install {{github-org}}/tap/{{project-name}}
```

### From Source

```bash
git clone https://github.com/{{github-org}}/{{project-name}}.git
cd {{project-name}}
make build
```

### Docker

```bash
docker pull ghcr.io/{{github-org}}/{{project-name}}:latest
```

## Verify Installation

```bash
{{project-name}} --version
```

## Next Steps

- [Configuration](configuration.md)
- [Tools Reference](../reference/tools.md)
