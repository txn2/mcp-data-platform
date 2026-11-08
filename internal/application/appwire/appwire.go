// Package appwire assembles applications (#2411, #2412) from the platform's
// stores: the service the manage_application tool and the portal routes
// share, over the application store, the script and asset stores, the
// webhook sources and the live application database toolkit, and the bundler
// that exports and imports them.
package appwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appbundle"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/application/apptool"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Assets is the portal asset store as applications use it.
type Assets interface {
	appsvc.Assets
	Insert(ctx context.Context, asset portaldomain.Asset) error
	List(ctx context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, int, error)
}

// Versions is the portal version store as applications use it.
type Versions interface {
	portaldomain.VersionReader
	CreateVersion(ctx context.Context, version portaldomain.AssetVersion) (int, error)
}

// Deps is what applications are assembled from.
type Deps struct {
	DB            *sql.DB
	Assets        Assets
	AssetVersions Versions
	Toolkits      *registry.Registry

	// What a bundle is written to and read from, and where an imported
	// asset's content goes.
	Blobs interface {
		GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
		PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	}
	Bucket, Prefix string
	Lander         interface {
		LandResource(ctx context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string) (*toolkit.ResourceLanding, error)
	}
	Reader interface {
		Source(ctx context.Context, ref string, claims resource.Claims, limit int64) (string, error)
	}
	Claims func(ctx context.Context) resource.Claims
	// Endpoints carries out an endpoint command, as manage_script does.
	Endpoints func(ctx context.Context, req epcmd.Request) (map[string]any, error)
	// SaveGate is the gate a script's source save crosses, which an import
	// puts each script through (#2476); nil refuses importing a script.
	SaveGate appbundle.SaveGate
	// Recordings is the store the gate reads recordings from, which an
	// export writes a script's test recordings out of and an import stores
	// them in (#2476).
	Recordings scriptrec.Store
	// Sealer keeps an application's run-role password sealed (#2539); nil
	// adds no schema.
	Sealer appsvc.Sealer
	// Reach lists the application database connections an actor may hold a
	// schema on; nil adds no schema.
	Reach func(ctx context.Context, actor application.Actor) ([]string, error)
}

// Service builds the application service over deps, nil without a database.
func Service(d Deps) *appsvc.Service {
	if d.DB == nil {
		return nil
	}
	scripts := scriptstore.New(d.DB)
	svc := &appsvc.Service{
		Store:     appstore.New(d.DB),
		Scripts:   scripts,
		Sources:   sources{db: d.DB},
		Schedules: scripts,
		Activity:  sources{db: d.DB},
		Schema:    &appschema.Schema{Kit: Kit(d.Toolkits), Store: migrationstore.New(d.DB)},
		// A connection named at create must be an application database
		// one (#2491).
		HasConnection: hasConnection(d.Toolkits),
		Roles:         liveRoles{reg: d.Toolkits},
		Reach:         d.Reach,
		Members:       members(d.Toolkits),
		Diagrams:      diagramDeps(d),
	}
	if d.Sealer != nil {
		svc.Sealer = d.Sealer
	}
	// A nil store is left nil, never an interface holding a nil.
	if d.Assets != nil {
		svc.Assets = d.Assets
	}
	if d.AssetVersions != nil {
		svc.AssetVersions = d.AssetVersions
	}
	svc.Lister = lister{scripts: scripts, assets: d.Assets, db: d.DB}
	return svc
}

// Bundler builds the exporter and importer over svc.
func Bundler(svc *appsvc.Service, d Deps) *appbundle.Bundler {
	if svc == nil {
		return nil
	}
	scripts := scriptstore.New(d.DB)
	b := appbundle.Deps{
		Service: svc, Migrations: migrationstore.New(d.DB), Bindings: epstore.New(d.DB), Schedules: scripts,
		FormConnection: formConnection(d.DB), Blobs: d.Blobs, Lander: d.Lander, Reader: d.Reader, Claims: d.Claims,
		Scripts: scripts, Bucket: d.Bucket, Prefix: d.Prefix, Endpoints: d.Endpoints,
		HasSchemaConnection: hasConnection(d.Toolkits), SaveGate: d.SaveGate,
	}
	if d.Recordings != nil {
		b.Recordings = d.Recordings
	}
	if d.Assets != nil {
		b.Assets = d.Assets
	}
	if d.AssetVersions != nil {
		b.AssetVersions = d.AssetVersions
	}
	return appbundle.New(b)
}

// Register assembles the service and the bundler and registers
// manage_application, returning the service for the portal routes. Nothing
// without a database.
func Register(server *mcp.Server, d Deps) *appsvc.Service {
	svc := Service(d)
	if svc == nil {
		return nil
	}
	(&apptool.Tool{Service: svc, Bundles: Bundler(svc, d)}).Register(server)
	return svc
}

// Kit is the live application database toolkit, nil when none is registered.
func Kit(reg *registry.Registry) func() appschema.Kit {
	return func() appschema.Kit {
		pg := Live(reg)
		if pg == nil {
			return nil // an interface holding a nil *Toolkit is not nil
		}
		return pg
	}
}

// Live is the application database toolkit in the live registry, nil when
// none is registered.
func Live(reg *registry.Registry) *postgreskit.Toolkit {
	if reg == nil {
		return nil
	}
	for _, tk := range reg.All() {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			return pg
		}
	}
	return nil
}

func hasConnection(reg *registry.Registry) func(name string) bool {
	return func(name string) bool {
		pg := Live(reg)
		return pg != nil && pg.HasConnection(name)
	}
}

// formConnection reads the connection a form's webhook source is on, without
// reading the source's configuration, so no secret is decrypted to answer.
func formConnection(db *sql.DB) func(ctx context.Context, name string) (string, error) {
	return func(ctx context.Context, name string) (string, error) {
		var conn string
		err := db.QueryRowContext(ctx, `SELECT connection_name FROM webhook_sources WHERE name = $1`, name).Scan(&conn)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("reading webhook source: %w", err)
		}
		return conn, nil
	}
}

// sources answers whether a webhook source exists without reading its
// configuration, so its secrets are never decrypted to answer.
type sources struct{ db *sql.DB }

// Exists reports whether a webhook source named name exists.
func (s sources) Exists(ctx context.Context, name string) (bool, error) {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM webhook_sources WHERE name = $1)`, name).Scan(&ok); err != nil {
		return false, fmt.Errorf("reading webhook source: %w", err)
	}
	return ok, nil
}

// AcceptedSince counts the requests the webhook source named name accepted
// since a time, from the per-minute totals the receiver records.
func (s sources) AcceptedSince(ctx context.Context, name string, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(count), 0) FROM webhook_request_counts
		WHERE source = $1 AND outcome = 'accepted' AND minute >= $2`, name, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("reading webhook request counts: %w", err)
	}
	return n, nil
}

// lister reads what the Add parts dialog searches (#2527).
type lister struct {
	scripts *scriptstore.Store
	assets  Assets
	db      *sql.DB
}

// ListScripts lists scripts by the filter.
func (l lister) ListScripts(ctx context.Context, f script.ListFilter) ([]script.Script, error) {
	out, err := l.scripts.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("listing scripts: %w", err)
	}
	return out, nil
}

// ListAssets lists assets by the filter, none without an asset store.
func (l lister) ListAssets(ctx context.Context, f portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) {
	if l.assets == nil {
		return nil, 0, nil
	}
	out, n, err := l.assets.List(ctx, f)
	if err != nil {
		return nil, 0, fmt.Errorf("listing assets: %w", err)
	}
	return out, n, nil
}

// ListSources lists the names of webhook sources containing query, without
// reading their configuration.
func (l lister) ListSources(ctx context.Context, query string, limit int) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT name FROM webhook_sources
		WHERE $1 = '' OR name ILIKE '%' || $1 || '%' ORDER BY name LIMIT $2`, likeEscape(query), limit)
	if err != nil {
		return nil, fmt.Errorf("listing webhook sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("listing webhook sources: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing webhook sources: %w", err)
	}
	return out, nil
}

// likeEscape escapes the characters ILIKE reads as patterns.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
