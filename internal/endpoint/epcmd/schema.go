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
			"endpoint, and changing or turning one off, is an administrator's, except a form's owner, who binds their " +
			"own form authenticated, and public or bot_check once an administrator approved a version of it " +
			"(manage_approval)."),
		"methods": strings(fmt.Sprintf("For endpoint_set: the HTTP methods the endpoint answers (%v; default GET). "+
			"HEAD is answered wherever GET is.", epbinding.Methods)),
		"path": str("For endpoint_set: the path template under /endpoints/{name}, empty for the name alone. " +
			"\"/{id}\" takes one segment the script reads as req.params[\"id\"]. A last segment \"/{route...}\" takes the " +
			"rest of the path at any depth, read as req.params[\"route\"] with no leading slash: an application's root " +
			"endpoint bound at \"/{route...}\" answers every path of its page origin but /endpoints/, /_static/, /_viewer " +
			"and /_auth, and routes them itself (robots.txt, a 404 page)."),
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
		"response_mode": map[string]any{
			keyType: valString, "enum": epbinding.ResponseModes,
			keyDescription: "For endpoint_set: api (default) answers under a sandbox, so HTML runs nothing; page serves a " +
				"working browser page on the application's own origin (<application>.<pages_domain>), where its script runs, " +
				"loads the application's assets from /_static/<asset name>, and calls the application's endpoints at " +
				"/endpoints/<name>. The script must be a part of an application, and the deployment needs sites.pages_domain. " +
				"An inline script needs nonce=\"req[\"csp_nonce\"]\". Read fetch mcp:knowledge_page:platform-pages-and-sites.",
		},
		"page_cdns": strings("For endpoint_set on a page: the https origins it may load scripts, styles, images and fonts " +
			"from besides its own, such as https://cdn.jsdelivr.net. Each list here takes at most 20 origins, no path, and " +
			"one leading *. label with two labels after it (https://*.js.stripe.com)."),
		"page_frame_origins": strings("For endpoint_set on a page: the https origins it may show in a frame (frame-src), " +
			"such as a payment processor's hosted card fields: https://js.stripe.com, https://*.js.stripe.com."),
		"page_connect_origins": strings("For endpoint_set on a page: the https origins its scripts may call besides its own " +
			"(connect-src), such as https://api.stripe.com."),
		"page_form_origins": strings("For endpoint_set on a page: the https origins its forms may post to, or be redirected " +
			"to after a post, besides its own (form-action)."),
		"ip_rate_limit": integer("For endpoint_set: one client address's requests a minute on each replica, at most " +
			"rate_limit; past it a request is answered 429. An IPv6 client is counted by its /64. 0 removes the limit. The " +
			"handler reads the address as req[\"client_ip\"]; behind a proxy it is right only when server.trusted_proxies " +
			"names the proxy."),
		"rate_limit": integer(fmt.Sprintf("For endpoint_set: the endpoint's requests a minute on each replica "+
			"(default %d); past it a request is answered 429.", epbinding.DefaultRateLimit)),
		"connection": str("For endpoint_set on a form (a script defining form_spec and form_submit): the Trino " +
			"connection whose scratch catalog holds the table the form's submissions are stored in, " +
			"webhook_form_<endpoint>. Defaults to the only such connection; named once, when the form is first bound."),
		"decrypt_personas": strings("For endpoint_set on a form: the personas a script may decrypt the form's " +
			"sensitive fields as, with platform.decrypt. The administrator always may. Omit to keep them; [] removes them."),
		"preview_version": integer("For form_preview: the saved version whose form to preview, default the latest. " +
			"With source, the unsaved source is previewed instead. The link the result names opens for you only, " +
			"expires within the hour, and runs form_spec and form_submit as drafts: a submission there stores nothing."),
		"caller_rate_limit": integer(fmt.Sprintf("For endpoint_set on an authenticated endpoint: one caller's requests a "+
			"minute on each replica (default %d), at most rate_limit.", epbinding.DefaultCallerRateLimit)),
	}
}
