package platform

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcetemplates"
)

// TestRegisterResourceTemplates covers what stays here after the templates
// moved to internal/platform/resourcetemplates (#1628): the resources.enabled
// gate, and that an enabled deployment's providers actually reach a client.
//
// The templates' own behavior -- what each URI answers, and what a missing
// provider answers -- is tested in that package.
func TestRegisterResourceTemplates(t *testing.T) {
	tests := []struct {
		name    string
		enabled *bool
		offered bool
	}{
		{"disabled registers nothing", new(false), false},
		{"enabled registers the three templates", new(true), true},
		{"unset is enabled, the documented default", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
			p := &Platform{
				config:    &Config{Resources: ResourcesConfig{Enabled: tt.enabled}},
				mcpServer: server,
			}
			p.registerResourceTemplates()

			if got := templateOffered(t, server, resourcetemplates.SchemaURI); got != tt.offered {
				t.Errorf("schema template offered = %v, want %v", got, tt.offered)
			}
		})
	}
}

// templateOffered reports whether a client asking this server what it offers is
// told about the named template.
func templateOffered(t *testing.T, server *mcp.Server, uriTemplate string) bool {
	t.Helper()

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("connecting the server: %v", err)
	}
	defer serverSession.Close() //nolint:errcheck // best-effort close

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.1"}, nil).
		Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	defer session.Close() //nolint:errcheck // best-effort close

	res, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("listing resource templates: %v", err)
	}
	for _, rt := range res.ResourceTemplates {
		if rt.URITemplate == uriTemplate {
			return true
		}
	}
	return false
}
