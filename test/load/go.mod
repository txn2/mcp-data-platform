// Module github.com/txn2/mcp-data-platform/test/load is the load-test harness.
//
// It is a SEPARATE Go module on purpose: the root module's coverage, test, and
// lint gates run over `./...`, and a nested module is never matched by the
// root's `./...`, so the harness stays out of the root coverage denominator
// with no build-tag gymnastics (issue #921). Run its own checks from this
// directory: `go test ./...`, `go vet ./...`, `golangci-lint run ./...`.
module github.com/txn2/mcp-data-platform/test/load

go 1.26.2

require (
	github.com/modelcontextprotocol/go-sdk v1.6.1
	golang.org/x/time v0.15.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)
