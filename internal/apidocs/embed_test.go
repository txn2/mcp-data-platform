package apidocs

import (
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
