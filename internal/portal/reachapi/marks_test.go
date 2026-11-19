package reachapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarks(t *testing.T) {
	for query, want := range map[string]struct {
		status   string
		verified bool
	}{
		"":                                {"", false},
		"?status=draft":                   {"draft", false},
		"?status=+Deprecated+":            {"deprecated", false},
		"?status=none":                    {"none", false},
		"?status=archived":                {"", false},
		"?verified=true":                  {"", true},
		"?verified=1":                     {"", false},
		"?status=published&verified=true": {"published", true},
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets"+query, http.NoBody)
		status, verified := Marks(r)
		assert.Equal(t, want.status, status, query)
		assert.Equal(t, want.verified, verified, query)
	}
}
