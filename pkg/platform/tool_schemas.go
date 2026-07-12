package platform

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Declared OutputSchemas for the stable, platform-owned tools (#925). Each is an
// open object schema (additionalProperties allowed, nothing required, shared
// error envelope declared) derived from the tool's success-body struct; see
// middleware.OpenToolOutputSchema for the rationale on why the schemas are open
// rather than strict. Building them once at package init keeps the reflection
// cost off the per-registration path.
var (
	infoOutputSchema        *jsonschema.Schema = middleware.MustOutputSchema[Info]()
	connectionsOutputSchema *jsonschema.Schema = middleware.MustOutputSchema[listConnectionsOutput]()
	findToolsOutputSchema   *jsonschema.Schema = middleware.MustOutputSchema[findToolsOutput]()
)
