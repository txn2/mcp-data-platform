// Package embedwire assembles the embed hosts list (#2069) from the platform:
// the administrator's stored section, the configuration file's
// portal.embeds.hosts, and the admin routes that edit it.
package embedwire

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/portal/embedhosts"
)

// New builds the resolver. Without a database it serves the configuration
// file's list and stores nothing.
func New(db *sql.DB, cfg embedhosts.Config) *embedhosts.Resolver {
	if db == nil {
		return embedhosts.NewResolver(nil, cfg)
	}
	return embedhosts.NewResolver(section{embedhostsSection(db)}, cfg)
}

// embedhostsSection is the platform_settings row the list is stored in.
func embedhostsSection(db *sql.DB) *settingsjson.Section[embedhosts.Settings] {
	return settingsjson.New[embedhosts.Settings](db, embedhosts.SettingsSection)
}

// Mount registers the admin routes; a nil wrap or a resolver without a store
// mounts none.
func Mount(mux *http.ServeMux, r *embedhosts.Resolver, db *sql.DB, wrap func(http.Handler) http.Handler, author func(*http.Request) string) {
	if wrap == nil || db == nil {
		return
	}
	embedhosts.RegisterSettings(mux, wrap, r, author)
}

// section adapts the settingsjson section to the resolver.
type section struct {
	s *settingsjson.Section[embedhosts.Settings]
}

func (s section) Read(ctx context.Context) (embedhosts.Settings, bool, error) {
	stored, err := s.s.Get(ctx)
	if errors.Is(err, settingsjson.ErrNotFound) {
		return embedhosts.Settings{}, false, nil
	}
	if err != nil {
		return embedhosts.Settings{}, false, err //nolint:wrapcheck // the section names the read
	}
	return stored.Value, true, nil
}

func (s section) Write(ctx context.Context, v embedhosts.Settings, author string) error {
	return s.s.Set(ctx, v, author) //nolint:wrapcheck // the section names the write
}
