package swagger2

import (
	"encoding/json"
	"testing"
)

// FuzzConvert holds the Swagger 2.0 conversion to an uploaded or refreshed
// spec (#2713): a document converts or is refused, never panics, and what it
// converts to is an OpenAPI 3 JSON document.
func FuzzConvert(f *testing.F) {
	f.Add(`{"swagger":"2.0","info":{"title":"t","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	f.Add("swagger: '2.0'\ninfo: {title: t, version: '1'}\npaths: {}\n")
	f.Add(`{"swagger":"2.0","definitions":{"A":{"$ref":"#/definitions/A"}}}`)
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<15 {
			return
		}
		_ = Is(raw)
		out, err := Convert(raw)
		if err != nil {
			return
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("Convert produced text that is not JSON: %v", err)
		}
		if v, _ := doc["openapi"].(string); v == "" {
			t.Fatalf("Convert produced a document with no openapi version: %.200s", out)
		}
	})
}
