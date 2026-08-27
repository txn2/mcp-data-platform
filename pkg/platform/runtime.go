package platform

import (
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// Transport identifiers for the runtime wiring gate. The platform accepts
// stdio, streamable HTTP, and legacy SSE; only the HTTP-family transports mount
// the gateway REST surface and the admin API the self-connection points at.
const (
	transportHTTP = "http"
	transportSSE  = "sse"
)

// RuntimeConfig carries the transport-dependent inputs the entry point resolves
// after config overrides are applied — the runtime transport and listen
// address. These are not known at platform.New / Start time (the factory
// constructs and starts the platform before flags/config decide the transport),
// so they are threaded in through WireRuntime rather than the constructor.
type RuntimeConfig struct {
	// Transport is the resolved server transport ("stdio", "http", or "sse").
	Transport string
	// Address is the server's listen address (e.g. ":8080"). It supplies the
	// port for the loopback base URL of the platform-admin self-connection.
	Address string
}

// isHTTPTransport reports whether t is one of the HTTP-family transports
// (streamable HTTP or legacy SSE). Gateway integrations and the admin
// self-connection are HTTP-only; stdio skips them.
func isHTTPTransport(t string) bool {
	return t == transportHTTP || t == transportSSE
}

// WireRuntime performs the post-Start, transport-aware wiring sequence in one
// code-defined order. It replaces the loose run of Wire* calls that used to sit
// in main.go, whose correct sequence was "documented nowhere but main.go
// itself" (#756): a reordering compiled and passed unit tests yet failed at
// runtime.
//
// The order encodes a real data-flow dependency, not a convention:
//
//   - WireAPIGatewayMetrics and WireAPIGatewayMemBudget instrument the
//     api-gateway toolkits and install the process-wide in-flight memory budget
//     (OOM guard, #535). Both transports.
//   - WireGatewayIntegrations wires the gateway/api-gateway stores — including
//     the api-catalog store and the embed-jobs queue. HTTP only.
//   - WireAdminSelfConnection then seeds the platform-admin self-connection,
//     which READS the catalog store and embed-jobs queue that
//     WireGatewayIntegrations just wired. Run it before that step and the seed
//     finds no catalog store and silently no-ops — the exact "compiles, unit
//     tests pass, fails at runtime" trap #756 calls out.
//   - markAPIGatewayCatalogWiringComplete runs last, on both transports. It
//     settles the question the api-gateway toolkit cannot answer during
//     construction: whether a connection's catalog_id has a store behind it
//     (#1509).
//
// Every step is individually idempotent and nil-safe, so WireRuntime is safe to
// call once per boot regardless of which subsystems are configured.
func (p *Platform) WireRuntime(rc RuntimeConfig) {
	// Both transports: instrument api-gateway toolkits and install the
	// process-wide in-flight memory budget so the OOM guard applies whether the
	// platform runs in stdio or HTTP mode.
	p.WireAPIGatewayMetrics()
	p.WireAPIGatewayMemBudget()

	if isHTTPTransport(rc.Transport) {
		// HTTP transports only. WireGatewayIntegrations MUST precede
		// WireAdminSelfConnection: the self-connection seed depends on the
		// catalog store and embed-jobs queue this step wires. The admin gate
		// mirrors the admin API mount in the entry point — the self-connection
		// is only useful when the admin REST surface it loops back to is
		// actually served.
		p.WireGatewayIntegrations()
		// The util connection seed (issue #1005) also reads the catalog store
		// and embed-jobs queue wired above. Unlike the admin self-connection
		// it does not depend on the admin REST surface, so it is not gated on
		// Admin.IsEnabled.
		wireUtilConnection(p)
		if p.config.Admin.IsEnabled() {
			p.WireAdminSelfConnection(rc.Address)
		}
	}

	// Last on both transports: every store that can back an api-gateway
	// catalog has now been wired, or was never going to be. Only from here
	// is "connection references a catalog with no store" a state an operator
	// can act on (#1509).
	markAPIGatewayCatalogWiringComplete(p)
}

// markAPIGatewayCatalogWiringComplete tells every live api gateway toolkit that
// startup wiring has finished, so a connection whose catalog_id still has no
// store behind it is reported once (#1509).
//
// A package-level function rather than a Platform method, matching
// wireUtilConnection: the wiring sequence grows without growing the facade.
func markAPIGatewayCatalogWiringComplete(p *Platform) {
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.MarkCatalogWiringComplete()
		}
	}
}
