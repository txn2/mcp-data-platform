# Installation

## Prerequisites

- Go 1.24+ (for building from source)
- An MCP-compatible client (Claude Desktop, Claude Code, or custom)
- Access to Trino, DataHub, and/or S3 services you want to connect

Optional:
- PostgreSQL (for audit logging and OAuth state)

## Installation Methods

### Go Install

The recommended method for most users:

```bash
go install github.com/txn2/mcp-data-platform/cmd/mcp-data-platform@latest
```

This installs the binary to your `$GOPATH/bin` directory.

### Homebrew (macOS)

```bash
brew install txn2/tap/mcp-data-platform
```

### From Source

Clone and build:

```bash
git clone https://github.com/txn2/mcp-data-platform.git
cd mcp-data-platform
go build -o mcp-data-platform ./cmd/mcp-data-platform
```

### Docker

Pull the official image:

```bash
docker pull ghcr.io/txn2/mcp-data-platform:latest
```

Run with a configuration file:

```bash
docker run \
  -v /path/to/platform.yaml:/etc/mcp/platform.yaml \
  -e DATAHUB_TOKEN=your-token \
  ghcr.io/txn2/mcp-data-platform:latest \
  --config /etc/mcp/platform.yaml
```

## Verify Installation

Check the version:

```bash
mcp-data-platform --version
```

Run with help to see available options:

```bash
mcp-data-platform --help
```

## Client Setup

### Claude Code

Add mcp-data-platform as an MCP server:

```bash
claude mcp add mcp-data-platform -- mcp-data-platform
```

With a configuration file:

```bash
claude mcp add mcp-data-platform -- mcp-data-platform --config /path/to/platform.yaml
```

Verify the server is registered:

```bash
claude mcp list
```

### Claude Desktop

Locate your configuration file:

- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add the server configuration:

```json
{
  "mcpServers": {
    "mcp-data-platform": {
      "command": "mcp-data-platform",
      "args": ["--config", "/path/to/platform.yaml"],
      "env": {
        "DATAHUB_TOKEN": "your-token",
        "TRINO_PASSWORD": "your-password"
      }
    }
  }
}
```

Restart Claude Desktop to load the new server.

## Command Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--config` | Path to YAML configuration file | None |
| `--transport` | Transport protocol: `stdio` or `http` | `stdio` |
| `--address` | Listen address for HTTP transports | `:8080` |
| `--version` | Print version and exit | - |
| `--help` | Print help message | - |

## Running with HTTP Transport

For remote access or web-based clients. The HTTP server serves both SSE (`/sse`, `/message`) and Streamable HTTP (`/`) transports:

```bash
mcp-data-platform --config platform.yaml --transport http --address :8080
```

With TLS:

```yaml
server:
  transport: http
  address: ":8443"
  tls:
    enabled: true
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem
```

## Next Steps

- [Configuration](configuration.md) - Configure toolkits, auth, and personas
- [Tools](tools.md) - Explore available tools
