// Command apisvc runs the fixture HTTP service the API studies share: the
// API-connection study's full tier-2 catalog (issue #1027) or the
// perishable-knowledge study's catalog and world plane (issue #1054),
// selected by -surface. Both serve in-memory state derived from the fixed
// seeds, with the harness control plane under /_bench/: POST /_bench/reset
// restores state between attempts, and POST /_bench/world changes the
// perishable world between sessions.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
)

func main() {
	addr := flag.String("addr", ":8110", "listen address")
	apiKey := flag.String("api-key", os.Getenv("APISVC_KEY"), "static X-API-Key credential (empty disables auth)")
	surface := flag.String("surface", "", "catalog to serve: empty for the #1027 tier-2 catalog, or 'perishable' for the #1054 study")
	world := flag.String("world", "", "starting world profile for the perishable surface (default "+apigen.DefaultWorldProfile+")")
	flag.Parse()
	if err := run(*addr, *apiKey, *surface, *world); err != nil {
		fmt.Fprintln(os.Stderr, "apisvc:", err)
		os.Exit(1)
	}
}

// run builds the service and serves until the process is stopped.
func run(addr, apiKey, surface, world string) error {
	if surface != string(apisvc.SurfaceAPIStudy) && surface != string(apisvc.SurfacePerishable) {
		return fmt.Errorf("unknown surface %q", surface)
	}
	if world != "" {
		if _, ok := apigen.WorldByName(world); !ok {
			return fmt.Errorf("unknown world profile %q", world)
		}
	}
	handler := apisvc.New(apisvc.Options{
		APIKey:       apiKey,
		Surface:      apisvc.Surface(surface),
		WorldProfile: world,
	})
	log.Printf("apisvc: serving %s fixture catalog on %s (auth %s)", surfaceLabel(surface), addr, authLabel(apiKey))
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// surfaceLabel renders the served surface for the startup log line.
func surfaceLabel(surface string) string {
	if surface == string(apisvc.SurfaceAPIStudy) {
		return "api-study"
	}
	return surface
}

// authLabel renders the auth mode for the startup log line.
func authLabel(apiKey string) string {
	if apiKey == "" {
		return "disabled"
	}
	return "X-API-Key"
}
