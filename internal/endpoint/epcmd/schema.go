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
			keyType: valString, "enum": epbinding.AuthModes,
			keyDescription: "For endpoint_set: authenticated answers a platform API key or a browser session among " +
				"callers, and req.caller is that identity; public answers anyone with no check; bot_check answers anyone " +
				"who passes the bot check named in bot_check. req.caller is None on public and bot_check. An automated " +
				"caller, a partner system included, calls an authenticated endpoint with an API key.",
		},
		"callers": strings("For endpoint_set on an authenticated endpoint: who it admits. \"*\" admits every " +
			"authenticated caller and is named alone; \"persona:<name>\" admits the callers whose persona it is; " +
			"\"api_key:<name>\" admits that API key. Empty admits no one; a caller not admitted is answered 403. " +
			"The administrator is admitted whatever the list says."),
		"bot_check": str("For endpoint_set on a bot_check endpoint: the name of the bot check it requires, one an " +
			"administrator configured under Admin > Settings > Bot checks. A proof of work makes each request cost work " +
			"any program can pay; Turnstile and hCaptcha need a browser."),
		"checked_methods": strings("For endpoint_set on a bot_check endpoint: the methods the check applies to, " +
			"empty for every method it answers. [\"POST\"] serves a form's page on GET without a check and checks " +
			"its submission. HEAD is checked where GET is."),
		"max_memory_bytes": integer(fmt.Sprintf("For endpoint_set: the memory one request may hold (default %d). "+
			"timeout_seconds is how long one request may run (default %d, at most %d).",
			epbinding.DefaultMaxMemoryBytes, epbinding.DefaultTimeoutSeconds, epbinding.MaxTimeoutSeconds)),
		"max_body_bytes": integer(fmt.Sprintf("For endpoint_set: the largest request body (default %d, at most %d); "+
			"a larger one is answered 413 without running the script.", epbinding.DefaultMaxBodyBytes, epbinding.MaxMaxBodyBytes)),
		"allowed_origins": strings("For endpoint_set: the browser origins (https://forms.example.com) a cross-origin " +
			"request may come from. Empty answers no cross-origin preflight."),
		"rate_limit": integer(fmt.Sprintf("For endpoint_set: the endpoint's requests a minute on each replica "+
			"(default %d); past it a request is answered 429.", epbinding.DefaultRateLimit)),
		"caller_rate_limit": integer(fmt.Sprintf("For endpoint_set on an authenticated endpoint: one caller's requests a "+
			"minute on each replica (default %d), at most rate_limit.", epbinding.DefaultCallerRateLimit)),
	}
}
