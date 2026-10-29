package audit

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
)

// redactedSentinel replaces a sensitive value in a stored parameter map.
const redactedSentinel = "[REDACTED]"

// sensitiveKeyParts are the words that make an argument key sensitive wherever
// it appears in the arguments, compared against the key lowercased with "-"
// read as "_": "password", "previous_secret", "X-API-Key", "Authorization",
// "session_token", "client_secret", "mtls_client_key_pem", "database_dsn".
var sensitiveKeyParts = []string{
	"password", "secret", "token", "api_key", "apikey", "credential",
	"private_key", "authorization", "cookie", "_pem", "service_account_json", "dsn",
}

// sensitiveKeySuffix is the ending that makes a key sensitive on its own:
// "subscription_key", "access_key", "secret_key".
const sensitiveKeySuffix = "_key"

// notSecretSuffixes end keys that name or describe a secret rather than hold
// one: token_path, signature_header, secret_name, token_url, secret_set.
var notSecretSuffixes = []string{
	"_path", "_header", "_name", "_names", "_set", "_url", "_encoding",
	"_until", "_at", "_count", "_type", "_placement", "_id", "_ids",
}

// secretMapKeys hold a map whose every value is a secret under a name the
// operator chose, such as a header the upstream reads its key from: a
// connection's static_headers and session_login_headers (#2359).
var secretMapKeys = map[string]bool{"static_headers": true, "session_login_headers": true}

// secretValuePaths are the admin routes whose request body carries a stored
// secret's value under "value" (#2356).
var secretValuePaths = []string{"/admin/secrets"}

// SanitizeParameters returns a copy of a tool call's arguments with every
// value under a sensitive key replaced, at any depth (#2356). A string that
// holds a JSON object or array, such as an api_invoke_endpoint body sent as
// text, is read and redacted the same way. A call to an admin route that sets
// a stored secret has the "value" in its body replaced too. Nothing that is
// redacted reaches audit_logs.parameters.
func SanitizeParameters(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	s := sanitizer{secretValue: callSetsSecret(params)}
	out, _ := s.value(params).(map[string]any)
	return out
}

// sanitizer walks one call's arguments.
type sanitizer struct {
	// secretValue is set when the call's path is an admin route that takes
	// a stored secret's value under "value".
	secretValue bool
}

// value returns v with every sensitive value replaced.
func (s sanitizer) value(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			switch {
			case s.sensitive(k):
				out[k] = redactedSentinel
			case secretMapKeys[strings.ToLower(k)]:
				out[k] = redactAll(inner)
			default:
				out[k] = s.value(inner)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = s.value(inner)
		}
		return out
	case string:
		return s.jsonText(t)
	}
	return v
}

// redactAll is a secret map with each value replaced and its names kept.
func redactAll(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return redactedSentinel
	}
	out := make(map[string]any, len(m))
	for k := range m {
		out[k] = redactedSentinel
	}
	return out
}

// jsonText redacts a string that holds a JSON object or array, or a query
// string, and returns any other string as it is.
func (s sanitizer) jsonText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return s.queryText(text)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var doc any
	if dec.Decode(&doc) != nil || dec.More() {
		return text
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(s.value(doc)) != nil {
		return redactedSentinel
	}
	return strings.TrimRight(buf.String(), "\n")
}

// queryText redacts the sensitive values of a string written as a query: a
// form-encoded body ("user=a&password=b"), or a path or URL carrying one
// ("/v1/items?api_key=..."). A string holding whitespace is prose or a query
// language, never one of these, and is returned as it is.
func (s sanitizer) queryText(text string) string {
	if !strings.Contains(text, "=") || strings.ContainsAny(text, " \t\n\r") {
		return text
	}
	prefix, query := "", text
	if i := strings.IndexByte(text, '?'); i >= 0 {
		prefix, query = text[:i+1], text[i+1:]
	}
	parts := strings.Split(query, "&")
	changed := false
	for i, part := range parts {
		k, _, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		name, err := url.QueryUnescape(k)
		if err != nil || !s.sensitive(name) {
			continue
		}
		parts[i], changed = k+"="+redactedSentinel, true
	}
	if !changed {
		return text
	}
	return prefix + strings.Join(parts, "&")
}

// sensitive reports whether a key's value is a secret.
func (s sanitizer) sensitive(key string) bool {
	k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
	if s.secretValue && k == "value" {
		return true
	}
	for _, suffix := range notSecretSuffixes {
		if strings.HasSuffix(k, suffix) {
			return false
		}
	}
	if strings.HasSuffix(k, sensitiveKeySuffix) {
		return true
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// callSetsSecret reports whether the call names an admin route that takes a
// stored secret's value, by its "path" or its "operation_id".
func callSetsSecret(params map[string]any) bool {
	for _, key := range []string{"path", "operation_id"} {
		route, _ := params[key].(string)
		for _, p := range secretValuePaths {
			if strings.Contains(route, p) {
				return true
			}
		}
	}
	return false
}
