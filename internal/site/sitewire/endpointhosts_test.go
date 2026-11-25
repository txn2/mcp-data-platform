package sitewire

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

var domainCols = []string{
	"hostname", "site_id", "name", "environment", "application_id", "application", "created_by", "created_at", "public", "access_changed_at", "is_primary", "mode", "redirects_to", "primary_public",
}

// TestEndpointHosts is where an application's endpoints answer besides the
// platform host (#2822): its page origin and its own domains, and each site
// part's addresses under the site environment's name.
func TestEndpointHosts(t *testing.T) {
	ctx := context.Background()
	shop := &application.Application{ID: "app_1", Name: "shop"}

	rt, mock := newDB(t)
	rt.Service.Live = rt.Live
	mock.ExpectQuery("ORDER BY d.hostname").WillReturnRows(sqlmock.NewRows(domainCols).
		AddRow("shop.client.example", "", "", "", "app_1", "shop", "a", time.Now(), false, time.Now(), false, "serve", "", false).
		AddRow("www.client.example", "site_1", "marketing", "prod", "", "", "a", time.Now(), true, time.Now(), false, "serve", "", false))
	mock.ExpectQuery("FROM application_parts p").WithArgs("app_1", pagesDomain).
		WillReturnRows(sqlmock.NewRows([]string{"env", "hostname"}).AddRow("prod", "www.client.example").AddRow("dev", "marketing--dev."+pagesDomain))
	h := rt.EndpointHosts(ctx, shop)
	assert.Equal(t, "shop", h.App)
	assert.Equal(t, []string{"https://shop." + pagesDomain, "https://shop.client.example"}, h.PageOrigins)
	assert.Equal(t, map[string][]string{"prod": {"https://www.client.example"}, "dev": {"https://marketing--dev." + pagesDomain}}, h.Sites)
	require.NoError(t, mock.ExpectationsWereMet())

	// Reads that fail leave their hosts out.
	rt, mock = newDB(t)
	rt.Service.Live = rt.Live
	mock.ExpectQuery("ORDER BY d.hostname").WillReturnError(errors.New("down"))
	mock.ExpectQuery("FROM application_parts p").WillReturnError(errors.New("down"))
	h = rt.EndpointHosts(ctx, shop)
	assert.Equal(t, []string{"https://shop." + pagesDomain}, h.PageOrigins)
	assert.Empty(t, h.Sites)
}
