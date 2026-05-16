package catalog

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// stubResolver lets tests pin a hostname to a chosen set of IPs
// without touching the real DNS.
type stubResolver struct {
	ips map[string][]net.IP
	err error
}

func (s *stubResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	if s.err != nil {
		return nil, s.err
	}
	if ips, ok := s.ips[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host")
}

func TestFetchFromURL_RejectsNonHTTPSWhenInsecureNotAllowed(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "http://example.com/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
	if !strings.Contains(err.Error(), "scheme must be https") {
		t.Fatalf("err message=%q", err.Error())
	}
}

func TestFetchFromURL_RejectsBogusScheme(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "file:///etc/passwd",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsEmptyHost(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://", FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsUnparseable(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://%zz", FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsLoopbackLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://127.0.0.1/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err message=%q", err.Error())
	}
}

func TestFetchFromURL_RejectsIPv6LoopbackLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://[::1]/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsLinkLocalLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://169.254.169.254/spec.json",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsPrivateLiteral(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"https://10.0.0.1/spec", "https://172.16.0.1/spec", "https://192.168.1.1/spec"} {
		_, err := FetchFromURL(context.Background(), addr, FetchOptions{})
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Fatalf("%s err=%v want ErrSSRFBlocked", addr, err)
		}
	}
}

func TestFetchFromURL_RejectsCGNATLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://100.64.1.1/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsMulticastLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://224.0.0.1/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsUnspecifiedLiteral(t *testing.T) {
	t.Parallel()
	_, err := FetchFromURL(context.Background(), "https://0.0.0.0/spec",
		FetchOptions{})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsPrivateViaDNS(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{ips: map[string][]net.IP{
		"trap.example.com": {net.ParseIP("10.20.30.40")},
	}}
	_, err := FetchFromURL(context.Background(),
		"https://trap.example.com/spec.json",
		FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsResolverError(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{err: errors.New("nx")}
	_, err := FetchFromURL(context.Background(), "https://x.example.com/y",
		FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_RejectsEmptyResolverResult(t *testing.T) {
	t.Parallel()
	stub := &stubResolver{ips: map[string][]net.IP{"empty.example.com": {}}}
	_, err := FetchFromURL(context.Background(),
		"https://empty.example.com/y", FetchOptions{Resolver: stub})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestFetchFromURL_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("openapi: 3.0.0\ninfo:\n  title: t\n  version: '1'\npaths: {}\n"))
	}))
	defer srv.Close()
	res, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			HTTPClient:           srv.Client(),
		})
	if err != nil {
		t.Fatalf("FetchFromURL: %v", err)
	}
	if !strings.Contains(res.Content, "openapi:") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
	if res.ETag != `"abc123"` {
		t.Fatalf("etag=%q", res.ETag)
	}
	if res.FetchedAt.IsZero() {
		t.Fatal("FetchedAt zero")
	}
}

func TestFetchFromURL_RejectsNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			HTTPClient:           srv.Client(),
		})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err=%v want ErrUpstream", err)
	}
}

func TestFetchFromURL_TooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	_, err := FetchFromURL(context.Background(), srv.URL,
		FetchOptions{
			AllowInsecureScheme:  true,
			AllowPrivateNetworks: true,
			MaxBytes:             512,
			HTTPClient:           srv.Client(),
		})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v want ErrTooLarge", err)
	}
}

func TestFetchFromURL_DialError(t *testing.T) {
	t.Parallel()
	// Pin a public-looking hostname to an unroutable public IP so
	// preflight passes but the dial fails. The dialer error path is
	// what we want exercised here.
	stub := &stubResolver{ips: map[string][]net.IP{
		"public.example.com": {net.ParseIP("203.0.113.1")}, // TEST-NET-3
	}}
	_, err := FetchFromURL(context.Background(),
		"https://public.example.com:1/spec",
		FetchOptions{
			Resolver:       stub,
			ConnectTimeout: 50 * time.Millisecond,
			TotalTimeout:   200 * time.Millisecond,
		})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestApplyFetchDefaults(t *testing.T) {
	t.Parallel()
	out := applyFetchDefaults(FetchOptions{})
	if out.MaxBytes != defaultFetchMaxBytes ||
		out.ConnectTimeout != defaultFetchConnectTimeout ||
		out.TotalTimeout != defaultFetchTotalTimeout {
		t.Fatalf("defaults not applied: %+v", out)
	}
}

func TestCheckDialAddress_AcceptsPublic(t *testing.T) {
	t.Parallel()
	if err := checkDialAddress("tcp4", "8.8.8.8:443"); err != nil {
		t.Fatalf("public dial address blocked: %v", err)
	}
}

func TestCheckDialAddress_RejectsLoopback(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "127.0.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_RejectsPrivate(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "10.0.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_RejectsCGNAT(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "100.64.0.1:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_BadHostPort(t *testing.T) {
	t.Parallel()
	err := checkDialAddress("tcp4", "garbage")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestCheckDialAddress_NonIPHost(t *testing.T) {
	t.Parallel()
	// Dialer only ever sees IPs, but the safety branch must report
	// rather than silently allow.
	err := checkDialAddress("tcp4", "example.com:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err=%v want ErrSSRFBlocked", err)
	}
}

func TestNewFetchClient_BuildsBoth(t *testing.T) {
	t.Parallel()
	c := newFetchClient(FetchOptions{
		ConnectTimeout: time.Second,
		TotalTimeout:   2 * time.Second,
	})
	if c.Timeout != 2*time.Second {
		t.Fatalf("Timeout=%v", c.Timeout)
	}
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect nil")
	}
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect returned %v want ErrUseLastResponse", err)
	}
	cAllow := newFetchClient(FetchOptions{
		ConnectTimeout:       time.Second,
		TotalTimeout:         2 * time.Second,
		AllowPrivateNetworks: true,
	})
	if cAllow.Transport == nil {
		t.Fatal("nil transport when private allowed")
	}
}

func TestBlockedIPReason_Public(t *testing.T) {
	t.Parallel()
	if r := blockedIPReason(net.ParseIP("8.8.8.8")); r != "" {
		t.Fatalf("public IP blocked: %q", r)
	}
	if r := blockedIPReason(net.ParseIP("2001:4860:4860::8888")); r != "" {
		t.Fatalf("public IPv6 blocked: %q", r)
	}
}

// TestParseSpec_AcceptsExampleSchemaMismatch reproduces a real-world
// drift pattern: a vendor declares a property as `type: object` but
// supplies string examples ("Blue", an ISO timestamp). Strict
// kin-openapi validation rejects this; production OpenAPI consumers
// (Swagger UI, Postman, Insomnia) accept it. ParseSpec must match
// the lenient behavior because examples are documentation, not part
// of the wire contract.
func TestParseSpec_AcceptsExampleSchemaMismatch(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/x": {
      "post": {
        "operationId": "addCustomField",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/CustomFieldAddArray"}
            }
          }
        },
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "components": {
    "schemas": {
      "CustomFieldAddArray": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "value": {"type": "object"}
          },
          "example": {"value": "Blue"}
        },
        "example": [
          {"value": "1986-01-22T00:00:00.0000000+00:00"},
          {"value": "Blue"}
        ]
      }
    }
  }
}`
	doc, err := ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec rejected a spec with example/schema drift: %v", err)
	}
	if doc == nil {
		t.Fatal("ParseSpec returned nil doc with nil error")
	}
}

// TestParseSpec_AcceptsUnsupportedPattern covers the second lenient
// category: schema patterns using regex constructs Go does not
// support. Strict validation rejects them; lenient accepts.
func TestParseSpec_AcceptsUnsupportedPattern(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/x": {
      "get": {
        "operationId": "lookahead",
        "parameters": [{
          "name": "q",
          "in": "query",
          "schema": {"type": "string", "pattern": "(?=.*foo).+"}
        }],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`
	if _, err := ParseSpec(spec); err != nil {
		t.Fatalf("ParseSpec rejected a spec with a Go-unsupported regex: %v", err)
	}
}

// TestParseSpec_AcceptsArrayWithoutItems reproduces the third
// real-world drift pattern: a parameter declared `type: array`
// without an `items` clause. OpenAPI 3.0 requires items for arrays,
// but vendor specs routinely omit them to mean "items of unknown
// shape." Swagger UI / Postman / Insomnia tolerate this. ParseSpec
// must match by injecting a permissive items schema before strict
// validation runs.
func TestParseSpec_AcceptsArrayWithoutItems(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/consents": {
      "get": {
        "operationId": "getConsents",
        "parameters": [
          {
            "name": "channels",
            "in": "query",
            "schema": {"type": "array"}
          }
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`
	doc, err := ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec rejected a spec with type:array sans items: %v", err)
	}
	if doc == nil {
		t.Fatal("ParseSpec returned nil doc with nil error")
	}
}

// TestParseSpec_AcceptsArrayWithoutItems_InComponentSchema covers
// the same drift inside a component schema rather than an inline
// parameter, plus the nested case (an object property of array type
// without items).
func TestParseSpec_AcceptsArrayWithoutItems_InComponentSchema(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/x": {
      "post": {
        "operationId": "create",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/Wrapper"}
            }
          }
        },
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "components": {
    "schemas": {
      "Wrapper": {
        "type": "object",
        "properties": {
          "tags": {"type": "array"},
          "nested": {
            "type": "object",
            "properties": {
              "inner_tags": {"type": "array"}
            }
          }
        }
      }
    }
  }
}`
	if _, err := ParseSpec(spec); err != nil {
		t.Fatalf("ParseSpec rejected nested array-without-items: %v", err)
	}
}

// TestParseSpec_AcceptsArrayWithoutItems_AllCallSites covers every
// place the normalizer walks: component schemas, component
// parameters, component headers, component request bodies,
// component responses (including their headers), path-level
// parameters, operation-level parameters (with both inline schema
// and content), operation request bodies, and operation responses
// (with headers). One spec, one assertion: it parses.
func TestParseSpec_AcceptsArrayWithoutItems_AllCallSites(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/x": {
      "parameters": [
        {"name": "trace", "in": "query", "schema": {"type": "array"}}
      ],
      "get": {
        "operationId": "op",
        "parameters": [
          {"name": "filter", "in": "query", "schema": {"type": "array"}},
          {
            "name": "body",
            "in": "query",
            "content": {
              "application/json": {"schema": {"type": "array"}}
            }
          },
          {"$ref": "#/components/parameters/Pager"}
        ],
        "requestBody": {"$ref": "#/components/requestBodies/Body"},
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {"schema": {"type": "array"}}
            },
            "headers": {
              "X-Tags": {"schema": {"type": "array"}}
            }
          },
          "default": {"$ref": "#/components/responses/Generic"}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "WithAllOf": {"allOf": [{"type": "array"}]},
      "WithAnyOf": {"anyOf": [{"type": "array"}]},
      "WithOneOf": {"oneOf": [{"type": "array"}]},
      "WithNot":   {"not": {"type": "array"}},
      "WithAddl":  {
        "type": "object",
        "additionalProperties": {"type": "array"}
      }
    },
    "parameters": {
      "Pager": {"name": "page", "in": "query", "schema": {"type": "array"}}
    },
    "headers": {
      "HArr": {"schema": {"type": "array"}}
    },
    "requestBodies": {
      "Body": {
        "required": true,
        "content": {"application/json": {"schema": {"type": "array"}}}
      }
    },
    "responses": {
      "Generic": {
        "description": "g",
        "content": {"application/json": {"schema": {"type": "array"}}},
        "headers": {"X-Total": {"schema": {"type": "array"}}}
      }
    }
  }
}`
	if _, err := ParseSpec(spec); err != nil {
		t.Fatalf("normalizer left a structural array-items error in place: %v", err)
	}
}

// TestParseSpec_AcceptsPascalCaseType reproduces the fourth
// real-world drift pattern: vendor SDK generators (.NET-style)
// emit PascalCase primitive type names ("String", "Integer") that
// strict kin-openapi rejects with "unsupported 'type' value
// 'String'." Swagger UI, Postman, and Insomnia accept these.
// ParseSpec must match by lowercasing primitive names case-
// insensitively before strict validation runs, in every place
// schemas appear.
func TestParseSpec_AcceptsPascalCaseType(t *testing.T) {
	t.Parallel()
	const spec = `{
  "openapi": "3.0.1",
  "info": {"title": "T", "version": "1.0"},
  "paths": {
    "/items": {
      "post": {
        "operationId": "createItem",
        "parameters": [
          {"name": "tag", "in": "query", "schema": {"type": "String"}}
        ],
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/Item"}
            }
          }
        },
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "components": {
    "schemas": {
      "Item": {
        "type": "Object",
        "properties": {
          "id":     {"type": "Integer"},
          "label":  {"type": "String"},
          "active": {"type": "Boolean"},
          "score":  {"type": "Number"},
          "tags":   {"type": "Array", "items": {"type": "String"}}
        }
      }
    }
  }
}`
	doc, err := ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec rejected a spec with PascalCase types: %v", err)
	}
	if doc == nil {
		t.Fatal("ParseSpec returned nil doc with nil error")
	}

	item := doc.Components.Schemas["Item"].Value
	if !item.Type.Is(openapi3.TypeObject) {
		t.Errorf("Item.Type = %v, want object", item.Type)
	}
	props := item.Properties
	wantTypes := map[string]string{
		"id":     openapi3.TypeInteger,
		"label":  openapi3.TypeString,
		"active": openapi3.TypeBoolean,
		"score":  openapi3.TypeNumber,
		"tags":   openapi3.TypeArray,
	}
	for name, want := range wantTypes {
		if !props[name].Value.Type.Is(want) {
			t.Errorf("Item.%s.Type = %v, want %s", name, props[name].Value.Type, want)
		}
	}
}

// TestCanonicalPrimitiveType verifies the case-insensitive
// primitive lookup that backs PascalCase normalization. Only the
// seven OpenAPI 3.x primitives are recognized; any other input
// (including typos) is left alone for strict validation to flag.
func TestCanonicalPrimitiveType(t *testing.T) {
	t.Parallel()
	primitives := []string{"string", "number", "integer", "boolean", "array", "object", "null"}
	for _, p := range primitives {
		variants := []string{p, strings.ToUpper(p[:1]) + p[1:], strings.ToUpper(p)}
		for _, v := range variants {
			got, ok := canonicalPrimitiveType(v)
			if !ok {
				t.Errorf("canonicalPrimitiveType(%q) ok=false, want true", v)
				continue
			}
			if got != p {
				t.Errorf("canonicalPrimitiveType(%q) = %q, want %q", v, got, p)
			}
		}
	}
	rejects := []string{"", "Strung", "stringy", "int", "bool", "Long", "Map", "List"}
	for _, r := range rejects {
		if got, ok := canonicalPrimitiveType(r); ok {
			t.Errorf("canonicalPrimitiveType(%q) ok=true got=%q, want false", r, got)
		}
	}
}

// TestParseSpec_RejectsStructuralErrors guards against over-leniency:
// structural problems (missing required field, malformed JSON) must
// still fail. The disabled options are scoped to documentation
// drift, not contract integrity.
func TestParseSpec_RejectsStructuralErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty":                  "",
		"whitespace":             "   \n  ",
		"not-json":               "not a spec",
		"missing-openapi":        `{"info": {"title": "T", "version": "1.0"}, "paths": {}}`,
		"missing-info":           `{"openapi": "3.0.1", "paths": {}}`,
		"truncated":              `{"openapi": "3.0.1", "info":`,
		"invalid-ref":            `{"openapi":"3.0.1","info":{"title":"T","version":"1.0"},"paths":{"/x":{"get":{"responses":{"200":{"$ref":"#/components/responses/Missing"}}}}}}`,
		"bad-type-not-primitive": `{"openapi":"3.0.1","info":{"title":"T","version":"1.0"},"paths":{},"components":{"schemas":{"X":{"type":"Strung"}}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSpec(raw)
			if err == nil {
				t.Fatalf("ParseSpec accepted %s spec; structural validation regressed", name)
			}
			if !errors.Is(err, ErrInvalidContent) {
				t.Fatalf("ParseSpec returned %v, want wrapping ErrInvalidContent", err)
			}
		})
	}
}

func TestBlockedIPReason_Ranges(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"127.0.0.1":   "loopback",
		"::1":         "loopback",
		"169.254.0.1": "link-local",
		"fe80::1":     "link-local",
		"224.0.0.1":   "link-local", // 224.0.0.0/24 is link-local-scope multicast
		"225.0.0.1":   "multicast",  // outside 224.0.0.0/24
		"0.0.0.0":     "unspecified",
		"10.0.0.1":    "private",
		"192.168.0.1": "private",
		"172.16.0.1":  "private",
		"100.64.1.1":  "carrier-grade-nat",
	}
	for ip, want := range cases {
		got := blockedIPReason(net.ParseIP(ip))
		if got != want {
			t.Fatalf("blockedIPReason(%s)=%q want %q", ip, got, want)
		}
	}
}

// TestCountOperations covers the operation-count helper the
// admin handler uses to stamp api_catalog_specs.operation_count
// on every spec write. The reconciler compares this against the
// embedding row count to detect gaps in pure SQL.
func TestCountOperations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec string
		want int
	}{
		{
			name: "empty paths",
			spec: `openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}`,
			want: 0,
		},
		{
			name: "one operation",
			spec: `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses:
        "200":
          description: ok`,
			want: 1,
		},
		{
			name: "six methods on one path",
			spec: `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /x:
    get:    {operationId: g, responses: {"200": {description: ok}}}
    post:   {operationId: p, responses: {"200": {description: ok}}}
    put:    {operationId: u, responses: {"200": {description: ok}}}
    delete: {operationId: d, responses: {"200": {description: ok}}}
    patch:  {operationId: pa, responses: {"200": {description: ok}}}
    head:   {operationId: h, responses: {"200": {description: ok}}}`,
			want: 6,
		},
		{
			name: "multiple paths",
			spec: `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses: {"200": {description: ok}}
  /b:
    post:
      operationId: b
      responses: {"200": {description: ok}}
  /c:
    put:
      operationId: c
      responses: {"200": {description: ok}}`,
			want: 3,
		},
		{
			name: "unparseable returns zero",
			spec: "::not yaml::",
			want: 0,
		},
		{
			name: "empty string returns zero",
			spec: "",
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := CountOperations(c.spec)
			if got != c.want {
				t.Errorf("CountOperations(%s) = %d; want %d", c.name, got, c.want)
			}
		})
	}
}

// TestCountOperationsOnItem covers the per-PathItem helper
// directly. Nil item returns 0; every method nil-check fires.
func TestCountOperationsOnItem(t *testing.T) {
	t.Parallel()
	if got := countOperationsOnItem(nil); got != 0 {
		t.Errorf("nil item: got %d, want 0", got)
	}
}
