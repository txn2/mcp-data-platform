// Command epmcp runs the study's b0 baseline (issue #1027): an MCP server
// generated at startup from a spec fixture, one tool per operation,
// proxying every call to the fixture HTTP service. Front it with the
// platform's MCP gateway toolkit so the b0 arm shares the same
// auth/audit plumbing as the b1 arms.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/epmcp"
)

func main() {
	addr := flag.String("addr", ":8111", "listen address")
	specPath := flag.String("spec", "specs/t0.json", "OpenAPI spec fixture to expose, one tool per operation")
	target := flag.String("target", "http://127.0.0.1:8110", "fixture service base URL")
	apiKey := flag.String("api-key", os.Getenv("APISVC_KEY"), "X-API-Key sent on proxied calls")
	flag.Parse()
	if err := run(*addr, *specPath, *target, *apiKey); err != nil {
		fmt.Fprintln(os.Stderr, "epmcp:", err)
		os.Exit(1)
	}
}

// run builds the per-endpoint server from the spec and serves MCP over
// streamable HTTP.
func run(addr, specPath, target, apiKey string) error {
	spec, err := os.ReadFile(specPath) // #nosec G304 -- operator-supplied spec fixture path
	if err != nil {
		return err
	}
	server, err := epmcp.BuildServer(spec, epmcp.Options{TargetBaseURL: target, APIKey: apiKey})
	if err != nil {
		return err
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	log.Printf("epmcp: serving %s per-endpoint on %s -> %s", specPath, addr, target)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}
