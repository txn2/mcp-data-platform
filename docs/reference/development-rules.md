# Development Rules

`CLAUDE.md` states the rules every change to this repository is held to. Some
are enforced: a Makefile target, a Go test or a CI job fails a change that
breaks them. The rest are steps only a person (or the agent doing the work)
can take, such as writing a threat model or reviewing a diff. This page says
which is which, so a rule that reads as binding is never mistaken for one with
a check behind it.

`TestEveryRuleIsClassified` (`test/gates/claude_rules_test.go`) reads this
table and `CLAUDE.md` together. It fails when `CLAUDE.md` gains a numbered
rule or a CRITICAL section this table does not list, when the table lists a
rule `CLAUDE.md` no longer states, when a row names a gate that does not exist,
and when a rule is classified as anything other than **enforced** or
**human**. A rule with no gate either gains one or is marked a human step;
there is no third class.

A gate in the last two columns is one of:

- `make <target>`: a Makefile target, most of them steps of `make verify`;
- `Test...`: a Go test, run by `make verify` (or by the CI job its package runs in);
- a path: the script or configuration file that holds the rule.

## The rules

| Rule | Section | Status | Gate | What the gate checks |
|---|---|---|---|---|
| CRITICAL - Factual Integrity (No Confabulation) | Factual Integrity | human | | Every claim in prose (a pull request, a commit message, a review) is checked against the diff or a file by the person writing it. |
| Never assert facts you haven't verified | Factual Integrity | human | | Read the file or check the behavior before stating it. |
| Every claim must be evidence-linked | Factual Integrity | human | | Each claim is visible in the diff or cites a file and line. |
| Never pad or embellish | Factual Integrity | human | | Describe the work that was done, and no more. |
| Uncertainty must be explicit | Factual Integrity | human | | A guess is written as a guess. |
| When reviewing, verify claims against evidence | Factual Integrity | human | | A description under review is a claim to check, not context. |
| Omission over fabrication | Factual Integrity | human | | A gap stated honestly, rather than an answer invented. |
| CRITICAL - Security Design Before Code | Security Design | human | | The threat model, the criteria it yields and the attacker's review are a person's work; rules 4 and 5 below have gates. |
| Threat model first | Security Design | human | | Written in chat before the first edit: trust boundaries, every secret and every sink. |
| Every "never" is a criterion | Security Design | human | `make acceptance-check` | The criteria a threat model yields run through the real surface like any other; what they search is the threat model's call. |
| A separate security review before `make verify` | Security Design | human | `make security-diff` `make semgrep-diff` | gosec and the diff-scoped Semgrep rules run on the change; the attacker's reading of the diff is a person's. |
| No tag while a security finding is open | Security Design | enforced | `scripts/release-tag.py` `make release-tag-check` `TestReleaseTag_AnOpenSecurityIssueHoldsEveryTag` | Every tag, candidate or final, is refused while an open issue carries the `security` label, in the release workflow and locally. |
| One list per rule | Security Design | enforced | `TestSanitizeParametersCoversEveryEncryptedKey` | The audit sanitizer redacts every key the encryption layer encrypts. |
| Idiomatic Go | Code Standards | enforced | `make fmt` `make lint` | gofmt, and golangci-lint with the linters `.golangci.yml` enables, on the changed lines. |
| Test Coverage | Code Standards | enforced | `make coverage` `make patch-coverage` `TestCoverageExclusionsAgree` | Total coverage at least `COVERAGE_MIN`, changed lines at least 80%, and the local and Codecov exclusions describing one set of files. |
| Testing Definition | Code Standards | enforced | `make verify` `TestVerifyRunsTheCheapestStepFirst` | `make verify` is the gate, and its steps run in the measured order the table records. |
| CRITICAL - Coverage Verification Before Completion | Code Standards | enforced | `make patch-coverage` | Changed lines at least 80% covered; coverage of each new function is a human step on top of it. |
| CRITICAL - Integration Tests for Cross-Component Behavior | Code Standards | human | | Which change spans components, and the test that assembles them, are a person's call. |
| CRITICAL - Acceptance Criteria Before Implementation | Code Standards | enforced | `make acceptance-check` | Production Go changed with no acceptance file is reported (blocking for an agent); writing the assertions before the code is a human step. |
| CRITICAL - The criteria are then executed through the real surface, locally, before `make verify` | Code Standards | enforced | `make acceptance-check` | A changed acceptance file with no passing run recorded fails. |
| CRITICAL - The acceptance test sends every JSON form the schema admits | Code Standards | enforced | `make acceptance` `make acceptance-check` | A run fails without the file's `// Wire forms:` comment, and the check fails without the transcript; that every form is sent is a human step. |
| CRITICAL - A criterion that did not run blocks the PR | Code Standards | enforced | `make acceptance-check` | Every criterion of a changed acceptance file needs a recorded pass; absent, skipped and failed are one verdict. |
| CRITICAL - Every tool a ticket registers has a criterion that calls it through the real client | Code Standards | enforced | `make acceptance-check` | A tool the diff registers that no changed acceptance file calls fails. |
| Human Review Required | Code Standards | human | | A person reviews and commits every change. |
| Go Report Card | Code Standards | enforced | `make fmt` `make lint` `.golangci.yml` | gofmt, go vet, gocyclo, ineffassign and misspell run as linters; the license file is checked by Go Report Card itself. |
| Diagrams | Code Standards | enforced | `TestDocsDrawDiagramsInMermaid` | No tracked Markdown file draws a box in characters. |
| Pinned Dependencies | Code Standards | enforced | `TestDependenciesArePinnedByDigest` | Every GitHub Action is pinned to a commit, and every Dockerfile base image and compose image to a digest. |
| Documentation Updates | Code Standards | enforced | `make doc-check` `scripts/doc-check.sh` | A change to `docs/` without `docs/llms.txt` and `docs/llms-full.txt` is reported (blocking for an agent). |
| CRITICAL - Documentation Completeness | Code Standards | enforced | `make doc-check` | A documentation-worthy change with no documentation change is reported (blocking for an agent). |
| No Tautological Tests | AI Verification | human | | Whether a test checks behavior is a reviewer's call. |
| Integration Tests for Multi-Component Features | AI Verification | human | | As Integration Tests for Cross-Component Behavior above. |
| Mutation Survival Review | AI Verification | enforced | `make mutate-diff` | The pull request's Mutation (changed lines) job fails on a survivor in a package `scripts/mutation-security-packages.txt` lists. |
| Dead Code Audit | AI Verification | human | `make dead-code` | `make verify` prints deadcode's report; which entries are library-export false positives is a person's call. |
| No Vaporware | AI Verification | enforced | `TestMigrationTablesHaveConsumers` `TestNoDeadPackages` `TestNoopOnlyInterfaces` | Every migration table has DML, every package has an importer, and no interface has only a noop implementation. |
| Dependency-First Verification | AI Verification | human | | That an upstream supports an operation is checked before anything is built on it. |
