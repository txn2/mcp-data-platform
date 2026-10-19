package epcmd

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

// JSON schema keys.
const (
	keyType        = "type"
	keyDescription = "description"
	valString      = "string"
)

// Properties is the schema fragment manage_script splices in for the fields
// of Input.
func Properties() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{keyType: valString, keyDescription: desc} }
	integer := func(desc string) map[string]any { return map[string]any{keyType: "integer", keyDescription: desc} }
	strings := func(desc string) map[string]any {
		return map[string]any{keyType: "array", "items": map[string]any{keyType: valString}, keyDescription: desc}
	}
	return map[string]any{
		"endpoint": str("For endpoint_set: the endpoint's name, the segment of /endpoints/{name} it answers at " +
			"(lowercase letters, digits, hyphens, underscores), unique across the deployment. Binding a script to an " +
			"endpoint, and changing or turning one off, is an administrator's."),
		"methods": strings(fmt.Sprintf("For endpoint_set: the HTTP methods the endpoint answers (%v; default GET). "+
			"HEAD is answered wherever GET is.", epbinding.Methods)),
		"path": str("For endpoint_set: the path template under /endpoints/{name}, empty for the name alone. " +
			"\"/{id}\" takes one segment the script reads as req.params[\"id\"]."),
		"auth_mode": map[string]any{
			keyType: valString, "enum": []string{epbinding.AuthPublic, epbinding.AuthSignedIn},
			keyDescription: "For endpoint_set: signed_in answers a browser session or an API key, and req.caller is " +
				"that identity; public answers anyone who passes the deployment's bot check, and req.caller is None.",
		},
		"open_reads": map[string]any{keyType: "boolean", keyDescription: "For endpoint_set on a public endpoint: let GET " +
			"and HEAD through without a bot check, for a read-only lookup a page loads."},
		"max_memory_bytes": integer(fmt.Sprintf("For endpoint_set: the memory one request may hold (default %d). "+
			"timeout_seconds is how long one request may run (default %d, at most %d).",
			epbinding.DefaultMaxMemoryBytes, epbinding.DefaultTimeoutSeconds, epbinding.MaxTimeoutSeconds)),
		"max_body_bytes": integer(fmt.Sprintf("For endpoint_set: the largest request body (default %d, at most %d); "+
			"a larger one is answered 413 without running the script.", epbinding.DefaultMaxBodyBytes, epbinding.MaxMaxBodyBytes)),
		"allowed_origins": strings("For endpoint_set: the browser origins (https://forms.example.com) a cross-origin " +
			"request may come from. Empty answers no cross-origin preflight."),
		"rate_limit": integer(fmt.Sprintf("For endpoint_set: the endpoint's requests a minute on each replica "+
			"(default %d); past it a request is answered 429.", epbinding.DefaultRateLimit)),
		"caller_rate_limit": integer(fmt.Sprintf("For endpoint_set on a signed_in endpoint: one caller's requests a "+
			"minute on each replica (default %d), at most rate_limit.", epbinding.DefaultCallerRateLimit)),
	}
}
