// Command apisvc runs the API-connection study's fixture HTTP service
// (issue #1027): the full tier-2 catalog served from in-memory seeded
// state, with the harness control plane under /_bench/. State derives
// from the fixed seeds; POST /_bench/reset restores it between attempts.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
)

func main() {
	addr := flag.String("addr", ":8110", "listen address")
	apiKey := flag.String("api-key", os.Getenv("APISVC_KEY"), "static X-API-Key credential (empty disables auth)")
	flag.Parse()
	if err := run(*addr, *apiKey); err != nil {
		fmt.Fprintln(os.Stderr, "apisvc:", err)
		os.Exit(1)
	}
}

// run builds the service and serves until the process is stopped.
func run(addr, apiKey string) error {
	handler := apisvc.New(apisvc.Options{APIKey: apiKey})
	log.Printf("apisvc: serving fixture catalog on %s (auth %s)", addr, authLabel(apiKey))
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// authLabel renders the auth mode for the startup log line.
func authLabel(apiKey string) string {
	if apiKey == "" {
		return "disabled"
	}
	return "X-API-Key"
}
