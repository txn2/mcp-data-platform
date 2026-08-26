package tableregister

import (
	"context"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// Subject resolves the record an id names into what the registrar needs, and
// decides whether this caller may act on it. Returning ok=false means the
// caller may not act on the record at all, which every surface answers as a
// not-found so none of them reveals a record the caller cannot reach.
//
// One resolver per kind serves both surfaces: the REST routes convert their
// authenticated portal user into a Caller and the tool reads one from the
// platform context, so the authorization rule for a kind is written once and
// cannot drift between the two doors.
type Subject func(ctx context.Context, id string, caller Caller) (Source, bool)

// ToolAdapter satisfies the asset toolkit's TableRegistrar over a Registrar,
// keyed by the canonical reference a caller already holds rather than by the
// id of one kind of record.
//
// It exists because the tool's contract carries no caller: the acting identity
// belongs on the PlatformContext the middleware chain put in the request's
// context, and reading it here rather than taking it as an argument is what
// keeps a tool call from presenting an identity the rest of the platform would
// refuse.
type ToolAdapter struct {
	reg *Registrar
	// adminRoles are the roles that make a caller an administrator, resolved
	// from the admin persona the way every other surface resolves it.
	adminRoles []string
	// subjects resolves a reference's kind to the records of that kind. A kind
	// with no entry cannot be registered through the tool, which is what a
	// deployment with no resource store or no asset store gets.
	subjects map[string]Subject
}

// NewToolAdapter adapts a Registrar for the table tool. A nil or unwired
// Registrar, or one with no kind to resolve, yields nil, which the toolkit
// renders as "this deployment cannot register tables" rather than as a
// failure.
func NewToolAdapter(reg *Registrar, adminRoles []string, subjects map[string]Subject) *ToolAdapter {
	if !reg.Available() || len(subjects) == 0 {
		return nil
	}
	return &ToolAdapter{reg: reg, adminRoles: adminRoles, subjects: subjects}
}

// Register registers a table over the current content of the file a reference
// names.
func (a *ToolAdapter) Register(
	ctx context.Context, reference, connection, tableName string, repair bool,
) (*portaltoolkit.TableRegistration, error) {
	src, err := a.resolve(ctx, reference)
	if err != nil {
		return nil, err
	}
	res, err := a.reg.Register(ctx, a.callerFrom(ctx), src, Request{
		Connection: connection,
		TableName:  tableName,
		Source:     "mcp",
		Repair:     repair,
	})
	if err != nil {
		return nil, err
	}
	// The source the registration was built over, not the one that was
	// resolved: a file that had to be corrected first is registered over the
	// version the correction wrote, and reporting staleness against the
	// version it replaced would call a fresh registration stale.
	view := toolView(res.Registration, res.Source)
	view.Repaired = res.Repair.Summary()
	return &view, nil
}

// Unregister drops a registered table.
func (a *ToolAdapter) Unregister(ctx context.Context, registrationID string) error {
	return a.reg.Unregister(ctx, a.callerFrom(ctx), registrationID, "mcp")
}

// Tables reports what is registered over the file a reference names.
func (a *ToolAdapter) Tables(ctx context.Context, reference string) ([]portaltoolkit.TableRegistration, error) {
	src, err := a.resolve(ctx, reference)
	if err != nil {
		return nil, err
	}
	regs, err := a.reg.BySource(ctx, src.Kind, src.ID)
	if err != nil {
		return nil, err
	}
	out := make([]portaltoolkit.TableRegistration, 0, len(regs))
	for _, reg := range regs {
		out = append(out, toolView(reg, src))
	}
	return out, nil
}

// DropAssetTables removes every table registered over a deleted asset.
func (a *ToolAdapter) DropAssetTables(ctx context.Context, assetID string) {
	a.reg.UnregisterAllForSource(ctx, KindAsset, assetID)
}

// resolve turns a reference into the source it names, refusing anything this
// caller may not register.
//
// A reference that resolves to no record and one that resolves to a record
// belonging to somebody else both come back as ErrNoSuchFile, so the tool
// cannot be used to discover which files exist.
func (a *ToolAdapter) resolve(ctx context.Context, reference string) (Source, error) {
	kind, id, err := ParseReference(reference)
	if err != nil {
		return Source{}, err
	}
	subject := a.subjects[kind]
	if subject == nil {
		return Source{}, fmt.Errorf("this deployment stores no %s files (%w)", kind, ErrUnavailable)
	}
	src, ok := subject(ctx, id, a.callerFrom(ctx))
	if !ok {
		return Source{}, ErrNoSuchFile
	}
	return src, nil
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
func toolView(reg Registration, src Source) portaltoolkit.TableRegistration {
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
		Stale:          reg.IsStale(src.Bucket, src.HeadKey),
	}
}

// callerFrom builds the registrar's view of whoever is making this tool call.
//
// A call with no PlatformContext carries no identity, which leaves the persona
// empty; the connection boundary denies every connection for an unresolvable
// persona, so an unauthenticated call registers nothing.
func (a *ToolAdapter) callerFrom(ctx context.Context) Caller {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return Caller{}
	}
	return Caller{
		UserID:     pc.UserID,
		Email:      pc.UserEmail,
		Persona:    pc.PersonaName,
		Roles:      pc.Roles,
		IsAdmin:    hasAnyRole(pc.Roles, a.adminRoles),
		OnBehalfOf: pc.OnBehalfOfEmail,
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
var _ portaltoolkit.TableRegistrar = (*ToolAdapter)(nil)
