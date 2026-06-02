package apidocs

import _ "embed"

// swaggerJSON holds the generated OpenAPI 2.0 (Swagger) document for the
// platform's REST API, embedded into the binary at build time. Because the
// document is regenerated from the same source tree's swaggo annotations on
// every build, the embedded bytes are, by construction, the exact API surface
// of the running version. The platform-admin self-connection sources its
// API-gateway catalog from this constant so admin endpoint discovery stays in
// sync with the binary with no manual catalog upkeep.
//
//go:embed swagger.json
var swaggerJSON string

// SwaggerJSON returns the embedded OpenAPI 2.0 document as a JSON string. The
// returned value is the same spec served at the live /swagger endpoints.
func SwaggerJSON() string {
	return swaggerJSON
}
