// Module github.com/txn2/mcp-data-platform/bench is the agent-effectiveness
// benchmark harness (issue #930, phase 1: #942).
//
// It is a SEPARATE Go module on purpose, mirroring test/load (issue #921): the
// root module's coverage, test, and lint gates run over `./...`, and a nested
// module is never matched by the root's `./...`, so the harness stays out of
// the root coverage denominator. Run its own checks from this directory:
// `go test ./...`, `go vet ./...`, `golangci-lint run ./...`.
module github.com/txn2/mcp-data-platform/bench

go 1.26.2

require (
	github.com/anthropics/anthropic-sdk-go v1.19.0
	github.com/getkin/kin-openapi v0.142.0
	github.com/google/jsonschema-go v0.4.3
	github.com/modelcontextprotocol/go-sdk v1.6.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/go-openapi/jsonpointer v0.22.5 // indirect
	github.com/go-openapi/swag/jsonname v0.25.5 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/oasdiff/yaml v0.1.1 // indirect
	github.com/oasdiff/yaml3 v0.0.14 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.27.0 // indirect
)
