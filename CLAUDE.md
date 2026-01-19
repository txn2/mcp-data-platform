# CLAUDE.md

This file provides guidance to Claude Code when working with this project.

## Project Overview

**{{project-name}}** is a Model Context Protocol (MCP) server for Go. It enables AI assistants to interact with external systems via the MCP protocol.

**Key Design Goals:**
- Composable: Can be used standalone OR imported as a library
- Generic: No domain-specific logic; suitable for various deployments
- Secure: Read-only by default with configurable limits

## Code Standards

1. **Idiomatic Go**: All code must follow idiomatic Go patterns and conventions. Use `gofmt`, follow Effective Go guidelines, and adhere to Go Code Review Comments.

2. **Test Coverage**: Project must maintain >80% unit test coverage. Build mocks where necessary to achieve this. Use table-driven tests where appropriate.

3. **Testing Definition**: When asked to "test" or "testing" the code, this means running the full CI test suite:
   - Unit tests with race detection (`make test` or `go test -race ./...`)
   - Linting (`make lint` / golangci-lint)
   - Security scanning (`gosec ./...`)
   - All checks that run in CI must pass locally before considering code "tested"

4. **Human Review Required**: A human must review and approve every line of code before it is committed. Therefore, commits are always performed by a human, not by Claude.

5. **Go Report Card**: The project MUST always maintain 100% across all categories on [Go Report Card](https://goreportcard.com/). This includes:
   - **gofmt**: All code must be formatted with `gofmt`
   - **go vet**: No issues from `go vet`
   - **gocyclo**: All functions must have cyclomatic complexity ≤10
   - **golint**: No lint issues (deprecated but still checked)
   - **ineffassign**: No ineffectual assignments
   - **license**: Valid license file present
   - **misspell**: No spelling errors in comments/strings

6. **Diagrams**: Use Mermaid for all diagrams. Never use ASCII art.

## Project Structure

```
{{project-name}}/
├── cmd/{{project-name}}/main.go   # Standalone server entrypoint
├── pkg/                            # PUBLIC API (importable by other projects)
│   ├── client/                     # Client wrapper
│   │   └── client.go               # Connection and operations
│   └── tools/                      # MCP tool definitions
│       └── toolkit.go              # NewToolkit() and RegisterAll()
├── internal/server/                # Default server setup (private)
│   └── server.go                   # Server factory with Version var
├── docs/                           # Documentation (MkDocs)
├── mcpb/                           # MCPB bundle build scripts
├── go.mod
├── LICENSE                         # Apache 2.0
└── README.md
```

## Key Dependencies

- `github.com/mark3labs/mcp-go` - MCP SDK for Go

## Building and Running

```bash
# Build
go build -o {{project-name}} ./cmd/{{project-name}}

# Run
./{{project-name}}
```

## Composition Pattern

This package is designed to be imported by other MCP servers:

```go
import (
    "github.com/{{github-org}}/{{project-name}}/pkg/client"
    "github.com/{{github-org}}/{{project-name}}/pkg/tools"
)

// Create client
myClient, _ := client.New(ctx, client.FromEnv())

// Create toolkit and register on your server
toolkit := tools.NewToolkit(myClient)
toolkit.RegisterAll(yourMCPServer)
```

## Configuration Reference

Environment variables:
- `EXAMPLE_VAR` - Example variable (default: value)

## MCP Tools

| Tool | Description |
|------|-------------|
| `example_tool` | Example tool |
