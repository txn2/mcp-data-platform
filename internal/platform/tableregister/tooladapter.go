package tableregister

import (
	"context"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
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
	// locate resolves the record a write just changed, with no caller: a
	// follow runs under the registration's own registrant, after a write
	// whose authority was already decided by the surface that made it.
	locate Locator
}

// Locator resolves a source by kind and id without deciding authority over
// it. It serves the follow a write triggers (#1536), where the caller's
// authority over the file was settled by the write itself and the follow acts
// for the registrant. A kind the deployment does not store answers ok=false.
type Locator func(ctx context.Context, kind, id string) (Source, bool)

// NewToolAdapter adapts a Registrar for the table tool. A nil or unwired
// Registrar, or one with no kind to resolve, yields nil, which the toolkit
// renders as "this deployment cannot register tables" rather than as a
// failure.
func NewToolAdapter(reg *Registrar, adminRoles []string, subjects map[string]Subject, locate Locator) *ToolAdapter {
	if !reg.Available() || len(subjects) == 0 {
		return nil
	}
	return &ToolAdapter{reg: reg, adminRoles: adminRoles, subjects: subjects, locate: locate}
}

// Register registers a table over the current content of the file a reference
// names.
func (a *ToolAdapter) Register(
	ctx context.Context, reference, connection, tableName string, opts portaltoolkit.RegisterOptions,
) (*portaltoolkit.TableRegistration, error) {
	src, err := a.resolve(ctx, reference)
	if err != nil {
		return nil, err
	}
	res, err := a.reg.Register(ctx, a.callerFrom(ctx), src, Request{
		Connection: connection,
		TableName:  tableName,
		Source:     "mcp",
		Repair:     opts.Repair,
		Follow:     opts.Follow,
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
	view.Tables = Sentences(res.Siblings)
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

// FollowAssetTables moves the following registrations over an asset onto the
// version a write just produced, and reports every registration over it.
func (a *ToolAdapter) FollowAssetTables(ctx context.Context, assetID string, version int) []string {
	return a.follow(ctx, KindAsset, assetID, version)
}

// FollowResourceTables is FollowAssetTables for a managed resource whose
// content was just replaced.
func (a *ToolAdapter) FollowResourceTables(ctx context.Context, resourceID string, version int) []string {
	return a.follow(ctx, KindResource, resourceID, version)
}

// follow resolves the source a write changed and follows it. A kind the
// deployment cannot locate, or a record that is gone, has nothing to report:
// the write happened, and there is no table to say anything about.
func (a *ToolAdapter) follow(ctx context.Context, kind, id string, version int) []string {
	if a.locate == nil {
		return nil
	}
	src, ok := a.locate(ctx, kind, id)
	if !ok {
		return nil
	}
	return Sentences(a.reg.FollowSource(ctx, src, version))
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

// ParseReference resolves the canonical reference an agent already holds --
// the string `search` emits on a hit and `fetch` dereferences -- into the kind
// and id a registration is built over.
//
// It is what makes one action serve every kind of stored file. The platform
// has exactly one vocabulary for naming a record across tools, so a
// registration keyed by that vocabulary needs no per-kind argument and no
// second tool; the kind travels inside the reference.
//
// Only the two stored-file kinds resolve. Any other well-formed reference (a
// knowledge page, a dataset, a memory record) parses and is then refused by
// name, because naming what was passed tells the caller what to pass instead.
func ParseReference(reference string) (kind, id string, err error) {
	ref, parseErr := knowledgepage.ParseEntityRef(reference)
	if parseErr != nil {
		return "", "", fmt.Errorf("reference %q is %w: %s", reference, ErrBadReference, parseErr.Error())
	}
	switch ref.TargetType {
	case knowledgepage.RefTargetAsset:
		return KindAsset, ref.AssetID, nil
	case knowledgepage.RefTargetResource:
		return KindResource, ref.ResourceID, nil
	default:
		return "", "", fmt.Errorf(
			"reference %q names a %s, which is %w -- only a stored file can be a table, so pass the "+
				"mcp:resource: or mcp:asset: reference a search hit carries",
			reference, ref.TargetType, ErrBadReference)
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
		Follow:         reg.Follow,
		FollowError:    reg.FollowError,
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
