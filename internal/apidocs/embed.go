package apidocs

import (
	_ "embed"
	"regexp"
	"strings"
)

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
// returned value is the same spec served at the live /swagger endpoints, and
// carries the generator's `host` — the loopback address the self-connection
// reaches the platform on. A copy served to a reader over the network names
// the origin that served it instead; see SwaggerJSONForHost.
func SwaggerJSON() string {
	return swaggerJSON
}

// hostKey matches the document's top-level `host` member. swag writes it once,
// at the top level, and scripts/swagger-tag-groups.py re-serializes the
// document with the same four-space indent, so the first match is that member.
var hostKey = regexp.MustCompile(`(?m)^(\s*"host":\s*)"[^"]*"`)

// hostPrefix, hostSuffix split the embedded document either side of its host
// value, computed once so that serving a request is a concatenation rather
// than a parse of a 1 MB document.
var hostPrefix, hostSuffix string

// hostValueStart is the index, in hostKey's submatch list, of where the host's
// quoted value begins: the end of capture group 1.
const hostValueStart = 3

// validHost is a host[:port] a document may name: a DNS name, an IPv4 address,
// or a bracketed IPv6 literal, each optionally followed by a port. The Host
// header is the client's to write, and it is reflected into a document other
// people read, so what may be reflected is stated here rather than assumed.
var validHost = regexp.MustCompile(`^(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(?::\d{1,5})?$`)

// splitAtHost splits a document either side of its top-level `host` value,
// quotes included, and reports whether it has one. A document with no host is
// served unchanged, which is what the generator produced.
func splitAtHost(doc string) (prefix, suffix string, ok bool) {
	loc := hostKey.FindStringSubmatchIndex(doc)
	if loc == nil {
		return "", "", false
	}
	return doc[:loc[hostValueStart]], doc[loc[1]:], true
}

func init() {
	hostPrefix, hostSuffix, _ = splitAtHost(swaggerJSON)
}

// SwaggerJSONForHost returns the document with its `host` set to the origin it
// is being served from.
//
// The generated document names `localhost:8080`, which is the developer's
// laptop the annotations were written on and no deployment's address (#1750).
// Every reader fetches this document over the origin it describes, so that
// origin is the correct answer everywhere and the served copy is where it
// belongs: the embedded constant keeps the generator's value, because the
// self-connection reaches the platform on the loopback address rather than on
// whatever name a browser used.
//
// A host that is not a host is ignored rather than reflected: the value is
// written by the client and read by everyone else who opens the reference.
func SwaggerJSONForHost(host string) string {
	if hostPrefix == "" || !validHost.MatchString(host) {
		return swaggerJSON
	}
	// Written into the document as-is rather than marshaled: validHost admits
	// no quote, backslash or control character, so there is nothing for a JSON
	// encoder to escape, and no error for a caller to have to consider.
	var b strings.Builder
	b.Grow(len(hostPrefix) + len(host) + len(hostSuffix) + 2)
	// strings.Builder's writes never fail; its Write methods return an error
	// only to satisfy io.Writer.
	_, _ = b.WriteString(hostPrefix)
	_, _ = b.WriteString(`"`)
	_, _ = b.WriteString(host)
	_, _ = b.WriteString(`"`)
	_, _ = b.WriteString(hostSuffix)
	return b.String()
}
