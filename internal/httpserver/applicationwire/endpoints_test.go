package applicationwire

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
)

// bindingStore answers a script's bindings; nothing else is read.
type bindingStore struct{ epbinding.Store }

func (bindingStore) ByScript(context.Context, string) ([]epbinding.Binding, error) {
	return []epbinding.Binding{{Name: "research", Methods: []string{"GET"}, AuthMode: "public"}}, nil
}

// TestEndpointSource reads an application's endpoints on the platform host
// the endpoint commands report (#2822); without bindings there is none.
func TestEndpointSource(t *testing.T) {
	assert.Nil(t, endpointSource(&epwire.Endpoints{}, nil))
	read := endpointSource(&epwire.Endpoints{Bindings: bindingStore{}, Commands: &epcmd.Handler{BaseURL: "https://p.example.com"}},
		func(context.Context, string) string { return "research-api" })
	a := &application.Application{Name: "shop", Parts: []application.Part{{Kind: application.KindScript, ID: "s1"}}, Environments: []application.Environment{{Name: "prod"}}}
	got := read(context.Background(), a)["prod"]
	assert.Len(t, got, 1)
	assert.Equal(t, "research-api", got[0].Script)
	assert.Equal(t, []string{"https://p.example.com/endpoints/research"}, got[0].URLs)

	noBase := endpointSource(&epwire.Endpoints{Bindings: bindingStore{}}, nil)
	assert.Equal(t, []string{"/endpoints/research"}, noBase(context.Background(), a)["prod"][0].URLs)
}
