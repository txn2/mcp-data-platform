# Contributing to mcp-data-platform

Thank you for your interest in contributing to mcp-data-platform! This document provides guidelines and instructions for contributing.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## Getting Started

### Prerequisites

- Go 1.24 or later
- golangci-lint (for linting)
- gosec (for security scanning)

### Setting Up Development Environment

1. Fork and clone the repository:
   ```bash
   git clone https://github.com/YOUR_USERNAME/mcp-data-platform.git
   cd mcp-data-platform
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Install development tools:
   ```bash
   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
   go install github.com/securego/gosec/v2/cmd/gosec@latest
   go install golang.org/x/vuln/cmd/govulncheck@latest
   ```

4. Verify your setup:
   ```bash
   go test -race ./...
   golangci-lint run ./...
   gosec ./...
   ```

## Development Workflow

### Making Changes

1. Create a new branch:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. Make your changes following the [coding standards](#coding-standards).

3. Run tests and linting:
   ```bash
   go test -race ./...
   golangci-lint run ./...
   gosec ./...
   ```

4. Commit your changes:
   ```bash
   git commit -m "feat: add your feature description"
   ```

### Commit Messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` New features
- `fix:` Bug fixes
- `docs:` Documentation changes
- `test:` Adding or updating tests
- `refactor:` Code refactoring
- `ci:` CI/CD changes
- `chore:` Maintenance tasks

Examples:
```
feat: add support for custom semantic providers
fix: handle nil pointer in middleware chain
docs: update configuration examples
test: add tests for persona filtering
```

### Pull Requests

1. Update documentation if needed.
2. Add tests for new functionality.
3. Ensure all tests pass: `go test -race ./...`
4. Ensure linting passes: `golangci-lint run ./...`
5. Ensure security checks pass: `gosec ./...`
6. Submit your pull request.

#### PR Requirements

- [ ] Tests added/updated
- [ ] Documentation updated
- [ ] Linting passes
- [ ] Security scan passes
- [ ] Commit messages follow conventions
- [ ] Branch is up to date with main

## Coding Standards

### Go Code Style

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` and `goimports` for formatting
- All exported functions, types, and packages must have documentation
- Use meaningful variable and function names
- Keep functions focused and reasonably sized
- Cyclomatic complexity must not exceed 10

### Error Handling

- Always handle errors explicitly
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Return errors rather than logging and continuing
- Use error types for distinguishable error conditions

### Testing

- Write table-driven tests where appropriate
- Aim for >80% code coverage
- Test both success and failure paths
- Use descriptive test names: `TestFunctionName_Scenario_ExpectedResult`

Example:
```go
func TestPersonaFilter_AllowDeny_WildcardPatterns(t *testing.T) {
    filter := persona.NewFilter(persona.ToolRules{
        Allow: []string{"trino_*"},
        Deny:  []string{"*_delete_*"},
    })

    if !filter.IsAllowed("trino_query") {
        t.Error("expected trino_query to be allowed")
    }
    if filter.IsAllowed("trino_delete_table") {
        t.Error("expected trino_delete_table to be denied")
    }
}
```

### Documentation

- Package-level documentation explaining purpose
- Function documentation for exported functions
- Inline comments for complex logic only
- Keep README.md and CLAUDE.md up to date

## Project Structure

```
mcp-data-platform/
├── cmd/mcp-data-platform/   # Main application entry point
├── internal/server/         # Internal server implementation
├── pkg/                     # Public API packages
│   ├── platform/            # Core platform facade
│   ├── auth/                # Authentication (OIDC, API keys)
│   ├── oauth/               # OAuth 2.1 server
│   ├── persona/             # Role-based personas
│   ├── semantic/            # Semantic metadata provider
│   ├── query/               # Query execution provider
│   ├── middleware/          # Request/response middleware
│   ├── registry/            # Toolkit registry
│   ├── audit/               # Audit logging
│   ├── tuning/              # Prompts, hints, rules
│   └── tools/               # Base toolkit
├── configs/                 # Example configurations
└── migrations/              # SQL migrations
```

### Where to Make Changes

- **New semantic providers**: Add to `pkg/semantic/`
- **New query providers**: Add to `pkg/query/`
- **New middleware**: Add to `pkg/middleware/`
- **New toolkits**: Add to `pkg/registry/` and register in `pkg/tools/`
- **Authentication methods**: Add to `pkg/auth/`
- **Configuration options**: Modify `pkg/platform/config.go`

## Testing

### Running Tests

```bash
# Run all tests with race detection
go test -race ./...

# Run tests with coverage
go test -race -coverprofile=coverage.out ./...

# Generate HTML coverage report
go tool cover -html=coverage.out

# Run specific package tests
go test -race ./pkg/platform/...
```

## Structural maintainability gates

The `gocyclo`, `gocognit`, `nestif`, and `revive` rules all evaluate code
*inside* a single function or file. They keep individual functions simple but
say nothing about how packages relate to each other. Three additional gates
police structure rather than per-function complexity, so the codebase stays
maintainable as features accrete. Each was landed green against the tree at the
time and is meant to be **ratcheted tighter in follow-up PRs**, never relaxed to
make a violation pass.

### 1. Import boundaries (`depguard`)

The Go compiler forbids import cycles but not layering violations. `depguard`
(configured in `.golangci.yml` under `linters.settings.depguard`) declares which
packages may import which. The current rules, derived from the real import graph:

- **`admin-is-a-leaf`** — `pkg/admin` is the top composition layer, wired in only
  by `cmd/`. Nothing lower in the stack (toolkits, providers, middleware,
  `pkg/platform`) may import it. A toolkit that imports `pkg/admin` fails lint.
- **`toolkits-do-not-import-platform`** — toolkits sit below the platform facade
  (`pkg/platform` composes toolkits, never the reverse), so a toolkit importing
  `pkg/platform` is rejected.

To tighten: add a rule (or a `deny` entry) for the next boundary you want to
lock down, confirm `golangci-lint run --enable-only depguard ./...` is still
green, then commit. To verify the gate bites, temporarily add a denied import
and run the same command.

### 2. Cross-file duplication (`dupl`)

`dupl` flags copy-pasted blocks across files (threshold 150 tokens, the
permissive upstream default). This mechanically enforces our shared-abstraction
principle: per-kind forking ("Mirror of X, kept separate") is a code smell. CI
runs `only-new-issues`, so the handful of pre-existing clones are grandfathered
and only **new** duplication fails the gate. Ratchet the threshold down in a
follow-up once existing clones are consolidated. Test files are exempt
(table-driven and arrange-act-assert structure legitimately repeats).

### 3. Package-size budget (`TestPackageSizeBudget`)

Every per-function gate is satisfied by a god-package built from a hundred
small, low-complexity functions. `TestPackageSizeBudget` (in
`package_budget_test.go`) caps the size of a package as a whole: no package under
`pkg/` may exceed **13,000 non-generated LOC** or **35 non-generated files**.
Generated files (those carrying a `Code generated ... DO NOT EDIT.` marker) are
excluded so embedded specs do not masquerade as hand-written code.

The budgets sit just above today's largest package (`pkg/admin`, ~12.1k LOC, 27
files). They are **ceilings to ratchet down**, not numbers to raise: if a package
hits the budget, decompose it into cohesive sub-packages rather than bumping the
constant. Run it with `go test -run TestPackageSizeBudget .`.

## Security

- Never commit secrets or credentials
- Run `gosec ./...` before submitting PRs
- Report security vulnerabilities via [SECURITY.md](SECURITY.md)
- Follow secure coding practices

## Getting Help

- Open an issue for bugs or feature requests
- Check existing issues before creating new ones
- Join discussions in pull requests

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
