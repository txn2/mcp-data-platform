package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/httpserver/tablehttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/tablesource"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// trinoToolkitKind is the registry kind of the Trino toolkit.
const trinoToolkitKind = "trino"

// newIDLength is the byte length of a registration id, matching the other
// opaque ids the platform mints.
const newIDLength = 16

// buildTableRegistrar assembles the registrar from what the platform already
// has, or returns nil when the deployment cannot register tables: no database
// to record a registration in, no Trino toolkit to run the DDL on, or no
// object client to read a header row with.
//
// It is built here rather than on the Platform because every piece it needs is
// already exposed, and because a registrar that only the HTTP surfaces and the
// asset toolkit reach does not belong on the facade.
func buildTableRegistrar(p *platform.Platform) *tableregister.Registrar {
	if p == nil || p.DB() == nil {
		return nil
	}
	exec := trinoExecutor(p)
	if exec == nil {
		return nil
	}
	objects := tableObjectReaders(p)
	if len(objects) == 0 {
		return nil
	}

	var auditLogger tableregister.AuditLogger
	if store := p.Audit().Store(); store != nil {
		auditLogger = store
	}

	return tableregister.New(tableregister.Deps{
		Store:    tableregister.NewPostgresStore(p.DB()),
		Trino:    exec,
		Objects:  objects,
		Revisers: tableRevisers(p),
		Scope:    connscope.New(connscope.Deps{Registry: p.PersonaRegistry()}),
		Audit:    auditLogger,
		NewID:    newRegistrationID,
		MaxBytes: registrationMaxBytes(p.Config()),
	})
}

// registrationMaxBytes is the object size a registration will read: the
// deployment's own upload ceiling.
//
// A registration reads the object it is pointed at, so the bound has to be the
// largest object this deployment stores. Anything smaller is a platform
// refusing to use a file it just accepted -- which is what a compiled-in
// 100 MB became once #1628 let a deployment raise the write routes' ceiling
// without raising this one (#1634).
//
// It is normalized the way the write routes normalize it, so the two numbers
// are the same number and no file can be taken by one and refused by the
// other. A deployment that configures nothing gets resource.MaxUploadBytes,
// which is what tableregister.DefaultMaxBytes already was.
func registrationMaxBytes(cfg *platform.Config) int64 {
	if cfg == nil {
		return resource.NormalizeMaxUploadBytes(0)
	}
	return resource.NormalizeMaxUploadBytes(cfg.Resources.Managed.MaxUploadBytes)
}

// tableRegistrarOnce caches the registrar per platform so the REST routes, the
// asset toolkit, the discovery lookup and the source hooks all act through one
// instance instead of five that happen to agree.
var tableRegistrarOnce = struct {
	sync.Mutex
	forPlatform *platform.Platform
	registrar   *tableregister.Registrar
}{}

// tableRegistrar returns the one registrar for a platform.
func tableRegistrar(p *platform.Platform) *tableregister.Registrar {
	tableRegistrarOnce.Lock()
	defer tableRegistrarOnce.Unlock()
	if tableRegistrarOnce.forPlatform != p {
		tableRegistrarOnce.forPlatform = p
		tableRegistrarOnce.registrar = buildTableRegistrar(p)
	}
	return tableRegistrarOnce.registrar
}

// newRegistrationID mints an opaque registration id.
func newRegistrationID() (string, error) {
	b := make([]byte, newIDLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a registration id: %w", err)
	}
	return "reg_" + hex.EncodeToString(b), nil
}

// trinoExecutor finds the Trino toolkit that runs registration DDL. There is
// one multi-connection Trino toolkit on a configured deployment; the first of
// its kind is it.
func trinoExecutor(p *platform.Platform) tableregister.Executor {
	registry := p.ToolkitRegistry()
	if registry == nil {
		return nil
	}
	for _, tk := range registry.GetByKind(trinoToolkitKind) {
		if tt, ok := tk.(*trinotoolkit.Toolkit); ok {
			return tt
		}
	}
	return nil
}

// tableObjectReaders adapts the platform's S3 clients to what the registrar
// reads, one per source kind.
//
// The two kinds are read through their OWN clients rather than a shared one: a
// deployment names portal.s3_connection and resources.managed.s3_connection
// separately, so they can be different connections onto different stores, and
// reading a resource through the portal's client would look in the wrong place
// wherever they are. A kind whose client is absent or is not the shared adapter
// simply has no entry, which leaves that kind unregisterable rather than
// wrongly readable.
func tableObjectReaders(p *platform.Platform) map[string]tableregister.ObjectReader {
	readers := make(map[string]tableregister.ObjectReader, 2)
	if client, ok := p.PortalS3Client().(*s3adapter.ClientAdapter); ok && client != nil {
		readers[tableregister.KindAsset] = objectReaderAdapter{client: client}
	}
	if client, ok := p.ResourceS3Client().(*s3adapter.ClientAdapter); ok && client != nil {
		readers[tableregister.KindResource] = objectReaderAdapter{client: client}
	}
	return readers
}

// blobStore is the half of the shared S3 adapter the registrar reads through.
// Naming it as an interface is what lets the conversion below be exercised
// without standing up an S3 endpoint.
type blobStore interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	ListDirectory(ctx context.Context, bucket, prefix string) ([]s3adapter.ObjectEntry, bool, error)
}

// objectReaderAdapter converts the S3 adapter's listing entries into the
// registrar's, which is the whole of the impedance between them.
type objectReaderAdapter struct {
	client blobStore
}

// GetObject reads an object's bytes.
func (a objectReaderAdapter) GetObject(
	ctx context.Context, bucket, key string,
) (body []byte, contentType string, err error) {
	return a.client.GetObject(ctx, bucket, key) //nolint:wrapcheck // transparent pass-through
}

// ListDirectory lists the objects directly under a prefix.
func (a objectReaderAdapter) ListDirectory(
	ctx context.Context, bucket, prefix string,
) ([]tableregister.ObjectEntry, bool, error) {
	entries, truncated, err := a.client.ListDirectory(ctx, bucket, prefix)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // transparent pass-through
	}
	out := make([]tableregister.ObjectEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, tableregister.ObjectEntry{Key: e.Key, Size: e.Size})
	}
	return out, truncated, nil
}

// mountTableAPI registers the register / unregister routes for both kinds and
// hands the asset toolkit its registrar.
//
// Nothing is mounted when the deployment cannot register: the action is absent
// rather than present and always refusing, which is what lets the portal hide
// it instead of showing a button that answers with an apology.
func mountTableAPI(
	mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, adminRoles []string,
) {
	registrar := tableRegistrar(p)
	if !registrar.Available() {
		return
	}

	subjects := platformTableSubjects(p, adminRoles)
	handler := tablehttp.New(tablehttp.Deps{
		Registrar:   registrar,
		Resources:   subjects[tableregister.KindResource],
		Assets:      subjects[tableregister.KindAsset],
		Connections: tableConnectionEnumerator(p, adminRoles),
		Visible:     tableVisibility(p),
		Sources:     tableSourceLookup(p, adminRoles),
		Caller:      tableCaller(p, adminRoles),
	})
	if handler == nil {
		return
	}
	handler.Routes(mux, wrap)
	log.Println("Table registration enabled on /api/v1/{resources,portal/assets}/{id}/tables")
	log.Println("Registered tables listed on /api/v1/tables")
}

// TableSourceHooks are the callbacks the surrounding surfaces install at the
// two moments a source changes under its tables: a delete, so a deleted file
// leaves no table behind, and a revision, so a following registration moves
// onto the new head and a pinned one is reported behind (#1536). Every field
// is nil on a deployment that cannot register.
//
// A revise hook returns what happened to each registration over the file, as
// the sentences the write's result carries, and never fails the write.
type TableSourceHooks struct {
	AssetDeleted    func(context.Context, string)
	ResourceDeleted func(context.Context, string)
	AssetRevised    func(ctx context.Context, id string, version int) []string
	ResourceRevised func(ctx context.Context, id string, version int) []string
}

// tableSourceHooks builds the callbacks over the registrar.
func tableSourceHooks(p *platform.Platform) TableSourceHooks {
	registrar := tableRegistrar(p)
	if !registrar.Available() {
		return TableSourceHooks{}
	}
	follower := sourceFollower{registrar: registrar, locate: platformTableLocator(p)}
	return TableSourceHooks{
		AssetDeleted: func(ctx context.Context, id string) {
			registrar.UnregisterAllForSource(ctx, tableregister.KindAsset, id)
		},
		ResourceDeleted: func(ctx context.Context, id string) {
			registrar.UnregisterAllForSource(ctx, tableregister.KindResource, id)
		},
		AssetRevised: func(ctx context.Context, id string, version int) []string {
			return follower.follow(ctx, tableregister.KindAsset, id, version)
		},
		ResourceRevised: func(ctx context.Context, id string, version int) []string {
			return follower.follow(ctx, tableregister.KindResource, id, version)
		},
	}
}

// sourceFollower is the follow a revise hook runs: resolve the record the
// write changed, then follow the registrations over it. A record that cannot
// be resolved has nothing to report -- the write happened, and there is no
// table to say anything about.
type sourceFollower struct {
	registrar *tableregister.Registrar
	locate    tableregister.Locator
}

func (f sourceFollower) follow(ctx context.Context, kind, id string, version int) []string {
	src, ok := f.locate(ctx, kind, id)
	if !ok {
		return nil
	}
	return tableregister.Sentences(f.registrar.FollowSource(ctx, src, version))
}

// wireTableToolRegistrar hands the asset toolkit the registrar behind
// manage_table, and the discovery layer the lookup that puts a table reference
// on a search hit. Toolkits are built before this registrar exists, so it runs
// here for the same reason the feedback notifications do.
//
// The tool is given the same per-kind resolvers the REST routes use, so the
// rule for who may register a file is applied once and cannot differ between
// the two doors.
func wireTableToolRegistrar(p *platform.Platform, adminRoles []string) {
	registrar := tableRegistrar(p)
	if !registrar.Available() {
		return
	}
	adapter := tableregister.NewToolAdapter(registrar, adminRoles, platformTableSubjects(p, adminRoles),
		platformTableLocator(p))
	if adapter == nil {
		return
	}
	registry := p.ToolkitRegistry()
	if registry == nil {
		return
	}
	for _, tk := range registry.GetByKind(portalToolkitKind) {
		if sink, ok := tk.(tableRegistrarSink); ok {
			sink.SetTableRegistrar(adapter)
		}
	}
	wireTableLookup(p, tableregister.NewLookup(registrar))
}

// tableRegistrarSink is satisfied by the portal toolkit, which serves
// manage_asset.
type tableRegistrarSink interface {
	SetTableRegistrar(portaltoolkit.TableRegistrar)
}

// wireTableLookup binds the lookup on the search federation, which pushes it
// into every provider that can carry a table reference. A deployment with no
// router leaves hits as they were.
func wireTableLookup(p *platform.Platform, lookup *tableregister.Lookup) {
	if p == nil {
		return
	}
	if router := p.KnowledgeRouter(); router != nil {
		router.SetTableLookup(lookup)
	}
}

// platformTableSubjects reads the two stores off the platform and builds the
// resolvers over them.
func platformTableSubjects(p *platform.Platform, adminRoles []string) map[string]tableregister.Subject {
	return tablesource.Subjects(
		p.ResourceStore(), p.Config().Resources.Managed.S3Bucket, p.PortalAssetStore(), adminRoles)
}

// platformTableLocator reads the two stores off the platform and builds the
// caller-less resolver a follow uses.
func platformTableLocator(p *platform.Platform) tableregister.Locator {
	return tablesource.Locator(p.ResourceStore(), p.Config().Resources.Managed.S3Bucket, p.PortalAssetStore())
}

// tableConnectionEnumerator fills the connection picker with the connections
// this caller reaches that can actually hold a table: granted to their persona
// and carrying a scratch catalog and schema. A picker that offered anything
// else would offer a choice the registrar then refuses.
func tableConnectionEnumerator(p *platform.Platform, adminRoles []string) tablehttp.ConnectionEnumerator {
	lister := connreach.New(connreach.Deps{Toolkits: p.ToolkitRegistry(), Personas: p.PersonaRegistry()})
	if lister == nil {
		return nil
	}
	exec := trinoExecutor(p)
	if exec == nil {
		return nil
	}
	resolver := buildPersonaResolver(p.PersonaRegistry(), p.ToolkitRegistry())

	return func(ctx context.Context, user *portal.User) []tablehttp.ConnectionChoice {
		personaName := ""
		if resolver != nil {
			if info := resolver(user.Roles); info != nil {
				personaName = info.Name
			}
		}
		isAdmin := tablesource.HasAnyRole(user.Roles, adminRoles)

		return scratchConnectionChoices(lister.ForPersona(ctx, personaName, isAdmin), exec)
	}
}

// scratchConnectionChoices narrows what a caller reaches to what can actually
// hold a table: a Trino connection carrying a scratch catalog and schema, that
// will also accept the statement creating it.
//
// It is the picker's only source, so a choice it offers is one the registrar
// accepts. A form offering a connection the registration then refuses, and a
// registration refusing a connection the form offered, are the same defect
// read from opposite ends.
//
// The write check is the half this was missing. A scratch target is a
// destination, not a permission: a read-only connection can name one and still
// refuse the CREATE TABLE, which surfaced as a picker offering a connection and
// a 500 "the registration could not be completed" the moment it was chosen. The
// read-only flag is per connection and can be changed on a live connection, so
// it is asked at the moment the form is built rather than assumed from config.
func scratchConnectionChoices(
	reachable []connreach.Connection, exec tableregister.Executor,
) []tablehttp.ConnectionChoice {
	var choices []tablehttp.ConnectionChoice
	for _, conn := range reachable {
		if conn.Kind != trinoToolkitKind {
			continue
		}
		target, ok := exec.ScratchTarget(conn.Name)
		if !ok || !target.Configured() {
			continue
		}
		if !exec.AcceptsWrites(conn.Name) {
			continue
		}
		choices = append(choices, tablehttp.ConnectionChoice{
			Name:        conn.Name,
			Description: conn.Description,
			Catalog:     target.Catalog,
			Schema:      target.Schema,
		})
	}
	return choices
}

// tableVisibility reports the connections a caller may SEE registrations on
// (#1472).
//
// It is deliberately wider than the register picker. The picker narrows to
// connections that can hold a NEW table -- a scratch target, and a connection
// that accepts writes -- and neither is a property of a table that already
// exists. A connection turned read-only after a registration would otherwise
// drop that table out of the listing for the person who made it, while Trino
// went on answering queries against it.
//
// The boundary it does apply is the persona's, which is what a tool call meets
// and what Trino's own access control enforces. An administrator is
// unrestricted.
func tableVisibility(p *platform.Platform) tablehttp.Visibility {
	return connectionVisibility(
		connreach.New(connreach.Deps{Toolkits: p.ToolkitRegistry(), Personas: p.PersonaRegistry()}))
}

// connectionVisibility is tableVisibility over an enumeration, which is the
// whole of what it decides.
func connectionVisibility(lister *connreach.Lister) tablehttp.Visibility {
	return func(ctx context.Context, caller tableregister.Caller) ([]string, bool) {
		if caller.IsAdmin {
			return nil, true
		}
		var names []string
		for _, conn := range lister.ForPersona(ctx, caller.Persona, false) {
			if conn.Kind == trinoToolkitKind {
				names = append(names, conn.Name)
			}
		}
		return names, false
	}
}

// tableSourceLookup resolves the records a cross-source listing names: what
// each source is called, where its content sits now, and whether this caller
// may change it.
//
// Authority here is a field on the answer rather than the answer itself, which
// is what separates it from the Subject resolvers above. The listing shows
// what a caller may QUERY, decided by connection; the unregister action needs
// authority over the SOURCE, which is a different and narrower question, and a
// row is shown either way.
func tableSourceLookup(p *platform.Platform, adminRoles []string) tableregister.Sources {
	return tablesource.RefLookup(
		p.ResourceStore(), p.Config().Resources.Managed.S3Bucket, p.PortalAssetStore(), adminRoles)
}

// tableCaller builds the registrar's view of an authenticated portal user.
func tableCaller(p *platform.Platform, adminRoles []string) func(*portal.User) tableregister.Caller {
	resolver := buildPersonaResolver(p.PersonaRegistry(), p.ToolkitRegistry())
	return func(user *portal.User) tableregister.Caller {
		caller := tableregister.Caller{
			UserID:  user.UserID,
			Email:   user.Email,
			Roles:   user.Roles,
			IsAdmin: tablesource.HasAnyRole(user.Roles, adminRoles),
		}
		if resolver != nil {
			if info := resolver(user.Roles); info != nil {
				caller.Persona = info.Name
			}
		}
		return caller
	}
}
