package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/httpserver/tablehttp"
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
		Store:   tableregister.NewPostgresStore(p.DB()),
		Trino:   exec,
		Objects: objects,
		Scope:   connscope.New(connscope.Deps{Registry: p.PersonaRegistry()}),
		Audit:   auditLogger,
		NewID:   newRegistrationID,
	})
}

// tableRegistrarOnce caches the registrar per platform so the REST routes, the
// asset toolkit, the discovery lookup and the two delete hooks all act through
// one instance instead of five that happen to agree.
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

	handler := tablehttp.New(tablehttp.Deps{
		Registrar:   registrar,
		Resources:   resourceSubject(p),
		Assets:      assetSubject(p, adminRoles),
		Connections: tableConnectionEnumerator(p, adminRoles),
		Caller:      tableCaller(p, adminRoles),
	})
	if handler == nil {
		return
	}
	handler.Routes(mux, wrap)
	log.Println("Table registration enabled on /api/v1/{resources,portal/assets}/{id}/tables")
}

// TableCleanupHooks are the delete callbacks the surrounding surfaces install
// so a deleted file leaves no table behind. Both are nil on a deployment that
// cannot register.
type TableCleanupHooks struct {
	AssetDeleted    func(context.Context, string)
	ResourceDeleted func(context.Context, string)
}

// tableCleanupHooks builds the delete callbacks over the registrar.
func tableCleanupHooks(p *platform.Platform) TableCleanupHooks {
	registrar := tableRegistrar(p)
	if !registrar.Available() {
		return TableCleanupHooks{}
	}
	return TableCleanupHooks{
		AssetDeleted: func(ctx context.Context, id string) {
			registrar.UnregisterAllForSource(ctx, tableregister.KindAsset, id)
		},
		ResourceDeleted: func(ctx context.Context, id string) {
			registrar.UnregisterAllForSource(ctx, tableregister.KindResource, id)
		},
	}
}

// wireTableToolRegistrar hands the asset toolkit the registrar behind
// manage_asset's register_table, and the discovery layer the lookup that puts
// a table reference on a search hit. Toolkits are built before this registrar
// exists, so it runs here for the same reason the feedback notifications do.
func wireTableToolRegistrar(p *platform.Platform, adminRoles []string) {
	registrar := tableRegistrar(p)
	adapter := tableregister.NewAssetAdapter(registrar, adminRoles)
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

// resourceSubject resolves a managed resource for the table routes, applying
// the same visibility rule the resources API applies to a read: a resource
// outside the caller's scopes is indistinguishable from one that never
// existed.
func resourceSubject(p *platform.Platform) tablehttp.Subject {
	store := p.ResourceStore()
	if store == nil {
		return nil
	}
	bucket := p.Config().Resources.Managed.S3Bucket
	pr := p.PersonaRegistry()
	adminPersona := p.Config().Admin.Persona

	return func(ctx context.Context, id string, user *portal.User) (tableregister.Source, bool) {
		res, err := store.Get(ctx, id)
		if err != nil || res == nil {
			return tableregister.Source{}, false
		}
		claims, err := buildResourceClaims(user, pr, adminPersona)
		if err != nil || !resource.CanReadResource(*claims, res) {
			return tableregister.Source{}, false
		}
		return tableregister.SourceFromResource(tableregister.Record{
			ID: res.ID, Name: res.DisplayName, Bucket: bucket,
			Key: res.S3Key, ContentType: res.MIMEType, OwnerID: res.UploaderSub,
		}), true
	}
}

// assetSubject resolves a portal asset for the table routes.
func assetSubject(p *platform.Platform, adminRoles []string) tablehttp.Subject {
	store := p.PortalAssetStore()
	if store == nil {
		return nil
	}
	return func(ctx context.Context, id string, user *portal.User) (tableregister.Source, bool) {
		asset, err := store.Get(ctx, id)
		if err != nil || asset == nil || asset.DeletedAt != nil {
			return tableregister.Source{}, false
		}
		if !assetVisibleTo(*asset, user, adminRoles) {
			return tableregister.Source{}, false
		}
		return tableregister.SourceFromAssetRecord(tableregister.Record{
			ID: asset.ID, Name: asset.Name, Bucket: asset.S3Bucket,
			Key: asset.S3Key, ContentType: asset.ContentType, OwnerID: asset.OwnerID,
		}), true
	}
}

// assetVisibleTo reports whether this caller may act on an asset through the
// table routes.
//
// An asset belongs to one person, so the owner and an administrator reach it
// and nobody else does; an editor share does not carry it, because registering
// publishes the file's contents into a schema everyone with the connection can
// read, which is owner authority the way sharing is.
//
// Both halves of an identity match must be non-empty. A caller with no id and
// an asset with no owner id are not the same person, and matching them would
// hand an unauthenticated request every unattributed asset on the platform.
func assetVisibleTo(asset portal.Asset, user *portal.User, adminRoles []string) bool {
	if user == nil {
		return false
	}
	if hasAnyRoleIn(user.Roles, adminRoles) {
		return true
	}
	if asset.OwnerID != "" && asset.OwnerID == user.UserID {
		return true
	}
	return asset.OwnerEmail != "" && user.Email != "" &&
		strings.EqualFold(asset.OwnerEmail, user.Email)
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
		isAdmin := hasAnyRoleIn(user.Roles, adminRoles)

		return scratchConnectionChoices(lister.ForPersona(ctx, personaName, isAdmin), exec)
	}
}

// scratchConnectionChoices narrows what a caller reaches to what can actually
// hold a table: a Trino connection carrying a scratch catalog and schema.
//
// It is the picker's only source, so a choice it offers is one the registrar
// accepts. A form offering a connection the registration then refuses, and a
// registration refusing a connection the form offered, are the same defect
// read from opposite ends.
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
		choices = append(choices, tablehttp.ConnectionChoice{
			Name:        conn.Name,
			Description: conn.Description,
			Catalog:     target.Catalog,
			Schema:      target.Schema,
		})
	}
	return choices
}

// tableCaller builds the registrar's view of an authenticated portal user.
func tableCaller(p *platform.Platform, adminRoles []string) func(*portal.User) tableregister.Caller {
	resolver := buildPersonaResolver(p.PersonaRegistry(), p.ToolkitRegistry())
	return func(user *portal.User) tableregister.Caller {
		caller := tableregister.Caller{
			UserID:  user.UserID,
			Email:   user.Email,
			Roles:   user.Roles,
			IsAdmin: hasAnyRoleIn(user.Roles, adminRoles),
		}
		if resolver != nil {
			if info := resolver(user.Roles); info != nil {
				caller.Persona = info.Name
			}
		}
		return caller
	}
}

// hasAnyRoleIn reports whether held contains any of want.
func hasAnyRoleIn(held, want []string) bool {
	for _, h := range held {
		if slices.Contains(want, h) {
			return true
		}
	}
	return false
}
