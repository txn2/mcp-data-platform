# Contributing to mcp-data-platform

Thank you for your interest in contributing to mcp-data-platform! This document provides guidelines and instructions for contributing.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## Getting Started

### Prerequisites

- Go 1.26 or later
- golangci-lint (for linting; pinned version, see below)
- gosec (for security scanning; pinned version, see below)

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

   `golangci-lint` and `gosec` must match the versions pinned in the
   [`Makefile`](Makefile) (`GOLANGCI_LINT_VERSION` and `GOSEC_VERSION`). The
   `tools-check` gate in `make verify` hard-fails on any mismatch, so installing
   `@latest` will block your build. Install the pinned versions:

   ```bash
   # Versions must equal the pins in the Makefile (currently v2.11.4 / v2.27.1).
   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
   go install github.com/securego/gosec/v2/cmd/gosec@v2.27.1
   go install golang.org/x/vuln/cmd/govulncheck@latest
   ```

   Run `make tools-check` to verify your installed versions match the pins; it
   prints the exact install command for anything missing or mismatched. Treat
   the Makefile as the source of truth if the versions above have since moved.

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

### API stability

Only a defined subset of `pkg/` is a supported integration surface (`pkg/platform`,
`pkg/toolkit`, `pkg/registry`, `pkg/semantic`, `pkg/query`, `pkg/middleware`, and the
toolkit adapters' config types). Facade-internal seams live under `internal/platform/`
and are unimportable from outside the module. Before moving a package into or out of
the supported surface, read the [API stability policy](docs/library/stability.md) and
update it in the same change.

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
say nothing about how packages relate to each other. Several additional gates
police structure rather than per-function complexity, so the codebase stays
maintainable as features accrete. Each was landed green against the tree at the
time and is meant to be **ratcheted tighter in follow-up PRs**, never relaxed to
make a violation pass.

Two gates bound a package's *volume*: the LOC/file size budget (gate 3) and the
exported-surface budget (gate 6, its harder-to-game public-API counterpart). The
other three measure the *structure of its relationships* — which direction
dependencies point (gates 1 and 4) and whether a package's own declarations
actually cohere (gate 5). Structure is what distinguishes good decomposition from
mechanical shattering. The direction gates (1 and 4) are **exact**: they cannot
be satisfied by moving lines around. The cohesion gate (5) is a graph
**heuristic** — much harder to game than size, but not impossible (see its
section for the known blind spot). Together they raise the cost of the shattering
move far above what the size budget alone does (issue #738).

### 1. Import boundaries (`depguard`)

The Go compiler forbids import cycles but not layering violations. `depguard`
(configured in `.golangci.yml` under `linters.settings.depguard`) declares which
packages may import which. This is the **direction** half of the dependency
contract: it encodes intent — dependencies must point toward the stable,
abstract layers (Dependency Inversion / Stable Dependencies Principle) — which no
volume metric can express. The current rules, derived from the real import graph:

- **`admin-is-a-leaf`** — `pkg/admin` is the top composition layer, wired in only
  by `cmd/`. Nothing lower in the stack (toolkits, providers, middleware,
  `pkg/platform`) may import it. A toolkit that imports `pkg/admin` fails lint.
- **`toolkits-do-not-import-platform`** — toolkits sit below the platform facade
  (`pkg/platform` composes toolkits, never the reverse), so a toolkit importing
  `pkg/platform` is rejected.
- **`entry-point-is-a-sink`** — `cmd/` holds the composition roots (entry
  points); nothing may import them. Shared code belongs in `pkg/`, not in a
  `cmd` main.
- **`base-types-are-a-root`** — `pkg/toolkit` holds the shared toolkit types
  every toolkit implements and must stay a dependency root: it may import nothing
  first-party. When it needs behaviour from a higher layer, accept an interface
  instead of importing the implementation.
- **`providers-do-not-depend-up`** — the provider abstractions (`pkg/semantic`,
  `pkg/query`, `pkg/storage`) are depended upon by the layers above them, so they
  must not import `pkg/platform`, `pkg/middleware`, `pkg/admin`, or a toolkit.

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
`pkg/` may exceed **11,800 non-generated LOC** or **35 non-generated files**.
Generated files (those carrying a `Code generated ... DO NOT EDIT.` marker) are
excluded so embedded specs do not masquerade as hand-written code.

The LOC budget sits just above today's largest packages (`pkg/admin` and
`pkg/platform`, both ~11.5–11.8k LOC). It is a **ceiling to ratchet down**, not a
number to raise: if a package hits the budget, decompose it into cohesive
sub-packages rather than bumping the constant. The budget started at 13,000 and
was ratcheted to 11,800 after `pkg/pkcestore` was extracted from `pkg/admin`
(#636); further ratchets drive the decomposition of `admin` and `platform`. Run
it with `go test -run TestPackageSizeBudget .`.

### 4. Import ratchet (`TestPackageImportRatchet`)

The depguard rules above lock down specific *directions*. The ratchet (in
`pkg_relationship_test.go`) is the complementary backstop: it freezes the
**entire** first-party import graph. The allowed edges are stored in
`testdata/allowed_internal_imports.txt`, seeded from the current graph, and the
gate fails on **any** new edge — including a same-direction edge the depguard
rules permit. New coupling between two internal packages is therefore never
accidental; it is a reviewable diff to the golden file.

When you genuinely need a new internal dependency, regenerate the golden and
justify the coupling in your PR:

```bash
go test -run TestPackageImportRatchet . -args -update-imports
```

The golden is a **ceiling to ratchet down**: as coupling is removed, edges drop
out on the next regeneration and the surface shrinks. It is not a list to pad.

### 5. Package cohesion (`TestPackageCohesion`)

The size budget is gamed by shattering a god-package into several tightly-coupled
fragments — smaller by LOC, worse by design. Cohesion catches the opposite smell:
a package whose declarations split into two or more independent islands is two
packages sharing one import path, which the size budget cannot see.

`TestPackageCohesion` (in `pkg_relationship_test.go`) builds each package's
declaration reference graph — nodes are package-level funcs, methods, types,
vars and consts; edges connect a declaration to every package-level identifier it
references, so two declarations that share a common type or helper are connected
through it. It then fails any package holding **more than one significant
cluster** (a connected component of five or more declarations). Lone leaves and
tiny helper groups are tolerated as appendages; two substantial islands that
touch nothing in each other are the pathology. The failure names each cluster's
members so the seam to cut is explicit.

Packages with two or more significant clusters today are seeded into
`cohesionAllowlist`, keeping the gate green while flagging them for decomposition
in follow-ups. The allowlist is meant to **shrink**, never grow: once a seeded
package is split, remove its entry (the gate fails if a stale entry no longer
has multiple clusters). Run it with `go test -run TestPackageCohesion .`.

**Known blind spot.** The shared-identifier edge is deliberate — it stops the
gate from false-flagging independent handlers that legitimately cohere over one
shared `Store` (issue #738 calls this out explicitly). The cost is a false
*negative*: two genuinely-unrelated islands that both happen to touch a single
*incidental* package-level symbol (a shared `log` var, a sentinel `errNotFound`,
one common options struct) are joined through it and read as cohesive. So the gate
reliably catches fragmentation where the islands share nothing, but a determined
author can evade it by threading one common reference through both halves. This is
why cohesion is a heuristic backstop, not a proof; the exact direction gates (1
and 4) are the un-gameable half. A future refinement could weight edges by the
referenced symbol's kind (type vs. incidental value) to narrow the blind spot.

### 6. Exported-surface budget (`TestPackageExportedSurfaceBudget`)

The public-API counterpart to the LOC budget: where gate 3 bounds how much a
package *weighs*, this bounds how much of it is *exported*. A small public
surface is the idiomatic Go goal — minimal API, internals unexported — and
unlike an LOC cap it cannot be satisfied by reshuffling whitespace or splitting
files. The only way under the budget is to unexport helpers or move detail into
`internal/`, which is the behaviour we want to pressure toward.

`TestPackageExportedSurfaceBudget` (in `pkg_surface_budget_test.go`) counts
**top-level exported identifiers** per `pkg/` package — exported package-scope
funcs, types, vars and consts, one unit per exported name (each name in a grouped
var/const block counts), regardless of a type's fields or methods — and fails any
package exporting more than **150**. The
ceiling sits just above today's largest surfaces (`pkg/middleware` and
`pkg/portal` at 142, `pkg/platform` at 140). Like the LOC budget it is a
**ceiling to ratchet down**: if a package hits it, shrink the public API
(unexport module-internal helpers, hide detail behind interfaces or in
`internal/`) rather than raising the constant. Run it with
`go test -run TestPackageExportedSurfaceBudget .`.

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
