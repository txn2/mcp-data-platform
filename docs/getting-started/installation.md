# Installation

## Prerequisites

- Go 1.24 or later (for building from source)
- An MCP-compatible client (Claude Desktop, Claude Code, etc.)
- PostgreSQL (optional, for audit logging)

## Installation Methods

### Go Install (Recommended)

```bash
go install github.com/txn2/mcp-data-platform/cmd/mcp-data-platform@latest
```

### Homebrew (macOS)

```bash
brew install txn2/tap/mcp-data-platform
```

### From Source

```bash
git clone https://github.com/txn2/mcp-data-platform.git
cd mcp-data-platform
go build -o mcp-data-platform ./cmd/mcp-data-platform
```

### Docker

```bash
docker pull ghcr.io/txn2/mcp-data-platform:latest
```

## Verify Installation

```bash
mcp-data-platform --version
```

## Next Steps

- [Configuration](configuration.md)
- [Tools Reference](../reference/tools.md)
