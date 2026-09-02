package utilhandler

import _ "embed"

// specJSON is the OpenAPI 3.0 document describing this package's
// operations. It is seeded into the "util" catalog at boot so
// api_discover discovers the util
// connection's operations through the same catalog path as any other
// api connection. Handler routes and this document are maintained
// together in this package: adding an operation means adding both the
// mux route and its path item here.
//
//go:embed spec.json
var specJSON string

// SpecJSON returns the embedded OpenAPI document for the util catalog.
func SpecJSON() string { return specJSON }
