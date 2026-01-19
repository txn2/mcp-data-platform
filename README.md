# {{github-org}}/{{project-name}}

[![GitHub license](https://img.shields.io/github/license/{{github-org}}/{{project-name}}.svg)](https://github.com/{{github-org}}/{{project-name}}/blob/main/LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/{{github-org}}/{{project-name}}.svg)](https://pkg.go.dev/github.com/{{github-org}}/{{project-name}})
[![Go Report Card](https://goreportcard.com/badge/github.com/{{github-org}}/{{project-name}})](https://goreportcard.com/report/github.com/{{github-org}}/{{project-name}})
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/{{github-org}}/{{project-name}}/badge)](https://scorecard.dev/viewer/?uri=github.com/{{github-org}}/{{project-name}})
[![codecov](https://codecov.io/gh/{{github-org}}/{{project-name}}/branch/main/graph/badge.svg)](https://codecov.io/gh/{{github-org}}/{{project-name}})
[![SLSA 3](https://slsa.dev/images/gh-badge-level3.svg)](https://slsa.dev)

**Full documentation at [{{docs-url}}](https://{{docs-url}})**

{{project-description}}

## Installation

### Go Install

```bash
go install github.com/{{github-org}}/{{project-name}}/cmd/{{project-name}}@latest
```

### From Source

```bash
git clone https://github.com/{{github-org}}/{{project-name}}.git
cd {{project-name}}
make build
```

### Docker

```bash
docker run ghcr.io/{{github-org}}/{{project-name}}
```

## Quick Start

### Claude Code CLI

```bash
claude mcp add {{project-name}} -- {{project-name}}
```

### Claude Desktop

Add to your `claude_desktop_config.json` (find via Claude Desktop → Settings → Developer):

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

## MCP Tools

| Tool | Description |
|------|-------------|
| `example_tool` | Example tool description |

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `EXAMPLE_VAR` | Example variable | `default` |

## Development

```bash
# Clone the repository
git clone https://github.com/{{github-org}}/{{project-name}}.git
cd {{project-name}}

# Build
make build

# Run tests
make test

# Run linter
make lint

# Run all checks
make verify

# Serve documentation locally
make docs-serve
```

## Contributing

We welcome contributions for bug fixes, tests, and documentation. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[Apache License 2.0](LICENSE)

---

Open source by [Craig Johnston](https://twitter.com/cjimti), sponsored by [Deasil Works, Inc.](https://deasil.works/)
