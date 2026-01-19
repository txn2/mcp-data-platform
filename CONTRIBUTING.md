# Contributing to {{project-name}}

Thank you for your interest in contributing to {{project-name}}! This document provides guidelines and instructions for contributing.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## Getting Started

### Prerequisites

- Go 1.24 or later
- golangci-lint (for linting)

### Setting Up Development Environment

1. Fork and clone the repository:
   ```bash
   git clone https://github.com/YOUR_USERNAME/{{project-name}}.git
   cd {{project-name}}
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
   make verify
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
   make verify
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
feat: add support for query cancellation
fix: handle null values in JSON output
docs: update configuration examples
test: add tests for new tool
```

### Pull Requests

1. Update documentation if needed.
2. Add tests for new functionality.
3. Ensure all tests pass: `make test`
4. Ensure linting passes: `make lint`
5. Ensure security checks pass: `make security`
6. Update CHANGELOG.md if applicable.
7. Submit your pull request.

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
func TestConfig_Validate_MissingHost(t *testing.T) {
    cfg := Config{User: "test"}
    err := cfg.Validate()
    if err == nil {
        t.Error("expected error for missing host")
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
{{project-name}}/
├── cmd/{{project-name}}/   # Main application entry point
├── internal/server/        # Internal server implementation
├── pkg/client/             # Public client API
├── pkg/tools/              # Public MCP tool definitions
└── .github/                # GitHub configuration (workflows, etc.)
```

### Where to Make Changes

- **New MCP tools**: Add to `pkg/tools/`
- **Client functionality**: Modify `pkg/client/`
- **Server behavior**: Modify `internal/server/`
- **CI/CD**: Modify `.github/workflows/`

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make coverage

# Generate HTML coverage report
make coverage-html

# Run short tests only
make test-short
```

## Security

- Never commit secrets or credentials
- Run `make security` before submitting PRs
- Report security vulnerabilities via [SECURITY.md](SECURITY.md)
- Follow secure coding practices

## Getting Help

- Open an issue for bugs or feature requests
- Check existing issues before creating new ones
- Join discussions in pull requests

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
