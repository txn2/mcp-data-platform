package sitehttp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/portalperm"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// #2735: a site's page names the applications that use it only to a caller
// whose persona reaches the Applications pages, and never changes the site it
// was given.
func TestWithApplications(t *testing.T) {
	hello := &site.EnvironmentApplication{ID: "app_1", Name: "hello"}
	st := &site.Site{Name: "hello", Environments: []site.Environment{{Name: "dev", Application: hello}, {Name: "prod"}}}

	full := portalperm.WithAccess(context.Background(), persona.FullPortalAccess())
	got := withApplications(full, st)
	assert.Equal(t, hello, got.Environment("dev").Application)
	assert.Equal(t, []site.Application{{ID: "app_1", Name: "hello", Environments: []string{"dev"}}}, site.ApplicationsOf(got.Environments))

	off := false
	noAutomations := portalperm.WithAccess(context.Background(), persona.PortalAccess{Automations: &off})
	for name, ctx := range map[string]context.Context{"no Automations area": noAutomations, "no gate": context.Background()} {
		got := withApplications(ctx, st)
		assert.Nil(t, got.Environment("dev").Application, name)
		assert.Equal(t, []site.Application{}, site.ApplicationsOf(got.Environments), name)
	}
	assert.Equal(t, hello, st.Environment("dev").Application, "the site given is left as it was")
}
