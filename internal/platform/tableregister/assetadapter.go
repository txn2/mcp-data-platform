package tableregister

import (
	"context"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// AssetAdapter satisfies the asset toolkit's TableRegistrar over a Registrar.
//
// It exists because the tool's contract carries no caller: the acting identity
// belongs on the PlatformContext the middleware chain put in the request's
// context, and reading it here rather than taking it as an argument is what
// keeps a tool call from presenting an identity the rest of the platform would
// refuse.
type AssetAdapter struct {
	reg *Registrar
	// adminRoles are the roles that make a caller an administrator, resolved
	// from the admin persona the way every other surface resolves it.
	adminRoles []string
}

// NewAssetAdapter adapts a Registrar for the asset toolkit. A nil or unwired
// Registrar yields nil, which the toolkit renders as "this deployment cannot
// register tables" rather than as a failure.
func NewAssetAdapter(reg *Registrar, adminRoles []string) *AssetAdapter {
	if !reg.Available() {
		return nil
	}
	return &AssetAdapter{reg: reg, adminRoles: adminRoles}
}

// RegisterAsset registers a table over an asset's current content.
func (a *AssetAdapter) RegisterAsset(
	ctx context.Context, asset portal.Asset, connection, tableName string,
) (*portaltoolkit.TableRegistration, error) {
	reg, err := a.reg.Register(ctx, a.callerFrom(ctx), sourceFromAsset(asset), Request{
		Connection: connection,
		TableName:  tableName,
		Source:     "mcp",
	})
	if err != nil {
		return nil, err
	}
	view := toolView(*reg, asset)
	return &view, nil
}

// UnregisterAsset drops a registered table.
func (a *AssetAdapter) UnregisterAsset(ctx context.Context, registrationID string) error {
	return a.reg.Unregister(ctx, a.callerFrom(ctx), registrationID, "mcp")
}

// AssetTables reports what is registered over an asset.
func (a *AssetAdapter) AssetTables(ctx context.Context, asset portal.Asset) ([]portaltoolkit.TableRegistration, error) {
	regs, err := a.reg.BySource(ctx, KindAsset, asset.ID)
	if err != nil {
		return nil, err
	}
	out := make([]portaltoolkit.TableRegistration, 0, len(regs))
	for _, reg := range regs {
		out = append(out, toolView(reg, asset))
	}
	return out, nil
}

// DropAssetTables removes every table registered over a deleted asset.
func (a *AssetAdapter) DropAssetTables(ctx context.Context, assetID string) {
	a.reg.UnregisterAllForSource(ctx, KindAsset, assetID)
}

// sourceFromAsset is the one place an asset becomes something the registrar
// understands.
func sourceFromAsset(asset portal.Asset) Source {
	return Source{
		Kind:        KindAsset,
		ID:          asset.ID,
		Name:        asset.Name,
		Bucket:      asset.S3Bucket,
		HeadKey:     asset.S3Key,
		ContentType: asset.ContentType,
		OwnerID:     asset.OwnerID,
	}
}

// Record is what a caller already holds about a stored file, in the terms both
// kinds share. It is the argument SourceFromResource and SourceFromAssetRecord
// take, so neither depends on the portal or resource types.
type Record struct {
	ID          string
	Name        string
	Bucket      string
	Key         string
	ContentType string
	OwnerID     string
}

// SourceFromResource builds a managed-resource source: its head key is the
// current revision, and its directory holds only that file.
func SourceFromResource(rec Record) Source {
	return sourceFrom(KindResource, rec)
}

// SourceFromAssetRecord builds a portal-asset source.
//
// The kind is what separates it from SourceFromResource, and it is not a
// detail: it selects which object store the file is read from and which rows a
// delete sweeps.
func SourceFromAssetRecord(rec Record) Source {
	return sourceFrom(KindAsset, rec)
}

func sourceFrom(kind string, rec Record) Source {
	return Source{
		Kind:        kind,
		ID:          rec.ID,
		Name:        rec.Name,
		Bucket:      rec.Bucket,
		HeadKey:     rec.Key,
		ContentType: rec.ContentType,
		OwnerID:     rec.OwnerID,
	}
}

// toolView renders a registration the way the tool reports it.
func toolView(reg Registration, asset portal.Asset) portaltoolkit.TableRegistration {
	names := make([]string, 0, len(reg.Columns))
	for _, c := range reg.Columns {
		names = append(names, c.Name)
	}
	return portaltoolkit.TableRegistration{
		RegistrationID: reg.ID,
		Connection:     reg.Connection,
		QueryTable:     reg.QualifiedName(),
		Columns:        names,
		SampleSQL:      SampleJoinSQL(reg),
		RegisteredBy:   reg.RegisteredBy,
		Stale:          reg.IsStale(asset.S3Bucket, asset.S3Key),
	}
}

// callerFrom builds the registrar's view of whoever is making this tool call.
//
// A call with no PlatformContext carries no identity, which leaves the persona
// empty; the connection boundary denies every connection for an unresolvable
// persona, so an unauthenticated call registers nothing.
func (a *AssetAdapter) callerFrom(ctx context.Context) Caller {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return Caller{}
	}
	return Caller{
		UserID:  pc.UserID,
		Email:   pc.UserEmail,
		Persona: pc.PersonaName,
		Roles:   pc.Roles,
		IsAdmin: hasAnyRole(pc.Roles, a.adminRoles),
	}
}

// hasAnyRole reports whether the caller holds any of the administrator roles.
func hasAnyRole(held, admin []string) bool {
	for _, r := range held {
		if slices.Contains(admin, r) {
			return true
		}
	}
	return false
}

// Verify the adapter satisfies the toolkit-side capability.
var _ portaltoolkit.TableRegistrar = (*AssetAdapter)(nil)
