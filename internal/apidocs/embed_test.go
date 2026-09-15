package apidocs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSwaggerJSON_EmbeddedAndNonEmpty(t *testing.T) {
	s := SwaggerJSON()
	if s == "" {
		t.Fatal("SwaggerJSON() returned empty; embed failed")
	}
	if !strings.Contains(s, `"swagger"`) && !strings.Contains(s, `"openapi"`) {
		t.Error("SwaggerJSON() does not look like an OpenAPI document")
	}
}

// The document the generator writes names localhost:8080, which is the machine
// the annotations were authored on and no deployment's address. A reader always
// fetches it over the origin it describes, so the served copy names that origin
// (#1750). These hold the rewrite to the one member it is allowed to touch.

func TestSwaggerJSONForHost_NamesTheServingOrigin(t *testing.T) {
	for _, host := range []string{
		"mcp.example.com",
		"mcp.example.com:8443",
		"10.0.0.7:8080",
		"[::1]:8080",
		"localhost:5173",
	} {
		t.Run(host, func(t *testing.T) {
			got := SwaggerJSONForHost(host)
			doc := decodeDoc(t, got)
			if doc["host"] != host {
				t.Errorf("host = %v, want %q", doc["host"], host)
			}
			// Everything else is the embedded document: the rewrite replaces
			// one member, and a reader of the reference is reading the spec
			// this binary was built from.
			if doc["basePath"] != "/api/v1" {
				t.Errorf("basePath = %v, want /api/v1", doc["basePath"])
			}
			if len(got) != len(swaggerJSON)-len(`"localhost:8080"`)+len(host)+2 {
				t.Errorf("rewrite changed more than the host value: %d bytes vs %d",
					len(got), len(swaggerJSON))
			}
		})
	}
}

func TestSwaggerJSONForHost_IgnoresAValueThatIsNotAHost(t *testing.T) {
	// The Host header is written by the client and read by everyone else who
	// opens the reference, so anything that is not a host[:port] is dropped
	// rather than reflected into a document other people trust.
	for _, host := range []string{
		"",
		`evil", "x-injected": "`,
		"host with spaces",
		"has/a/path",
		"trailing\nnewline",
		"port:notanumber",
	} {
		t.Run(host, func(t *testing.T) {
			got := SwaggerJSONForHost(host)
			if got != swaggerJSON {
				t.Errorf("a host of %q was reflected into the served document", host)
			}
			// Still a document, whatever was attempted.
			if doc := decodeDoc(t, got); doc["host"] != "localhost:8080" {
				t.Errorf("host = %v, want the embedded value", doc["host"])
			}
		})
	}
}

// decodeDoc parses a served document, failing when the rewrite produced
// something that is no longer JSON.
func decodeDoc(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("the served document is not JSON: %v", err)
	}
	return doc
}

// A document with no host member is served unchanged. The embedded one always
// has one, so the split is a function of its own rather than a branch of init
// that nothing can reach.
func TestSplitAtHost(t *testing.T) {
	prefix, suffix, ok := splitAtHost(swaggerJSON)
	if !ok {
		t.Fatal("the embedded document has no host member to rewrite")
	}
	if prefix+`"localhost:8080"`+suffix != swaggerJSON {
		t.Error("the split does not reassemble into the document it came from")
	}

	if _, _, ok := splitAtHost(`{"swagger": "2.0", "basePath": "/api/v1"}`); ok {
		t.Error("a document with no host member reported one")
	}
	// A `host` property nested in a definition is not the document's own.
	nested := "{\n    \"definitions\": {\n        \"x\": {\n            \"host\": \"nested\"\n        }\n    }\n}"
	if p, _, ok := splitAtHost(nested); ok && !strings.HasSuffix(strings.TrimSpace(p), `"host":`) {
		t.Errorf("split at an unexpected member: %q", p)
	}
}
