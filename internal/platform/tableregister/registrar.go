package tableregister

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Source is the object a registration is built over: where it lives, what it
// is, and who it belongs to. Each surface builds one from its own record, so
// the registrar never learns what a resource or an asset is.
type Source struct {
	Kind        string
	ID          string
	Name        string
	Bucket      string
	HeadKey     string
	ContentType string
	// OwnerID is who the source belongs to. It decides whether a caller may
	// replace an existing registration of the same name.
	OwnerID string
}

// Caller is who is asking. Persona drives the connection boundary and the
// table-name prefix; IsAdmin lifts the ownership check on replacing a
// registration.
type Caller struct {
	UserID  string
	Email   string
	Persona string
	Roles   []string
	IsAdmin bool
	// OnBehalfOf is the address of the person an unattended caller acts for,
	// empty for a person acting as themselves. A managed-script run
	// authenticates as a principal that owns no stored file, so the authority
	// checks over the file being registered read this to reach what the run's
	// author reaches (#1419, #1487). The portal path never sets it: a browser
	// request is always somebody acting as themselves.
	OnBehalfOf string
}

// Request is one registration.
type Request struct {
	Connection string
	// TableName is the caller's choice. Empty takes a slug of the source's
	// filename. Either way it is slugified and persona-prefixed.
	TableName string
	// Source names where the registration comes from: "portal" for a REST or
	// UI action, "mcp" for a tool call. It is recorded on the audit event.
	Source string
	// Repair asks the platform to save a corrected version of the file when it
	// cannot be read as a table the way it is stored, and to register that
	// version. Without it such a file is refused and the refusal says what is
	// wrong with it, because correcting somebody's file is not something to do
	// on the way to something they asked for.
	Repair bool
	// Follow moves the registration to the file's new head whenever a
	// revision or version of it is written (#1536). Off, the registration is
	// pinned to the directory it was registered over, and a later write
	// leaves it behind the file and says so. The surfaces default it to on;
	// the registrar takes the resolved choice.
	Follow bool
}

// Deps is what a Registrar needs.
type Deps struct {
	Store Store
	Trino Executor
	// Objects reads a source's bytes, keyed by source kind. It is per kind
	// because the two kinds do not have to share an object store: a
	// deployment names the portal's S3 connection and the managed-resources
	// one separately, and reading a resource through the portal's client
	// would look in the wrong bucket on any deployment that split them.
	Objects map[string]ObjectReader
	// Revisers save a corrected copy of a source as a new version of itself,
	// keyed by source kind the way Objects is and for the same reason: the two
	// kinds keep their version trails in different places. A kind with no
	// entry can be registered and refused, but not corrected.
	Revisers map[string]Reviser
	Scope    ConnectionScope
	Audit    AuditLogger
	NewID    func() (string, error)
	MaxBytes int64
}

// DefaultMaxBytes bounds the object the registrar reads to find a header row.
//
// Neither S3 adapter has a range read, so learning the first line costs a full
// GetObject. The bound matches the managed-resource upload cap, which is the
// largest object either surface can have put there.
const DefaultMaxBytes = 100 << 20

// Log field names shared by this package's warnings.
const (
	logFieldError = "error"
	logFieldTable = "table"
)

// Registrar registers and unregisters tables over stored objects.
type Registrar struct {
	deps Deps
}

// New creates a Registrar. A nil Store or Trino executor makes every call
// report that registration is unavailable rather than panicking, which is the
// state of a deployment with no database or no Trino toolkit.
func New(deps Deps) *Registrar {
	if deps.MaxBytes <= 0 {
		deps.MaxBytes = DefaultMaxBytes
	}
	return &Registrar{deps: deps}
}

// ErrUnavailable means the deployment has no registration mechanism wired.
var ErrUnavailable = errors.New("table registration is not available on this deployment")

// Available reports whether registration is wired at all, so a surface can
// hide the action rather than offering one that always refuses.
func (r *Registrar) Available() bool {
	return r != nil && r.deps.Store != nil && r.deps.Trino != nil && len(r.deps.Objects) > 0
}

// objectsFor returns the reader for a source kind, or nil when the deployment
// has no store for it -- a platform with a portal but no managed resources
// configured, and the reverse.
func (r *Registrar) objectsFor(kind string) ObjectReader {
	return r.deps.Objects[kind]
}

// Register makes the source's directory readable as a table and records it.
//
// The order is deliberate: everything that can refuse does so before any
// statement runs, so a refused registration leaves nothing behind in Trino.
// The record is written last, because a row naming a table that was never
// created is worse than a table with no row -- the first is a lie a search hit
// repeats, the second is an object in a scratch schema.
func (r *Registrar) Register(ctx context.Context, caller Caller, src Source, req Request) (*Result, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if caller.Email == "" {
		return nil, ErrNoIdentity
	}

	p, err := r.plan(ctx, caller, src, req)
	if err != nil {
		// A correction is written before the last of the checks have run, so a
		// refusal can arrive after the person's file has already changed. It is
		// audited either way, and the refusal says so, because a message about
		// the table alone would leave them not knowing their file moved.
		if p.repair != nil {
			r.audit(ctx, auditRecord{caller: caller, reg: p.reg, source: req.Source, repair: p.repair, err: err})
		}
		return nil, repairedFailure(p.repair, err)
	}

	a := attempt{caller: caller, req: req, p: p}
	ddl := BuildDDL(p.reg, p.existing != nil)
	ran, execErr := r.runDDL(ctx, req.Connection, ddl)
	r.audit(ctx, auditRecord{
		caller: caller, reg: p.reg, ddl: ddl, source: req.Source, repair: p.repair, err: execErr,
	})
	if execErr != nil {
		r.forgetDroppedRegistration(ctx, a, ran, execErr)
		return nil, repairedFailure(p.repair, execErr)
	}

	// The DROP already removed the replaced table, so its row is stale
	// whatever happens next; dropping it before the insert is also what frees
	// the unique name for this one.
	//
	// Neither store write can be undone by the one that follows it, so both
	// take the table back out when they fail. The state that leaves -- a row
	// naming a table that is gone, or neither -- is one a query fails loudly
	// on and a second registration repairs. The state it avoids is the one
	// nothing reports: the table now reads the new file with the new columns,
	// and a surviving row would go on advertising the old ones from
	// toolView and SampleJoinSQL. IsStale does not cover it, because a
	// replacement that re-registers the same key to pick up a changed header
	// leaves the location it compares identical.
	//
	// Both failures also carry the correction the way the two above them do.
	// A file corrected on the way here stays corrected whatever the store
	// does, and an answer about the registration alone would leave its owner
	// not knowing a new version of their file exists.
	if p.existing != nil {
		if err := r.deps.Store.Delete(ctx, p.existing.ID); err != nil {
			r.rollBackTable(ctx, a, err)
			return nil, repairedFailure(p.repair, fmt.Errorf("replacing the previous registration: %w", err))
		}
	}
	if err := r.deps.Store.Insert(ctx, p.reg); err != nil {
		r.rollBackTable(ctx, a, err)
		return nil, repairedFailure(p.repair, fmt.Errorf("recording the registration: %w", err))
	}
	// A correction moved the file's head, which is the same move every other
	// write makes, so every OTHER registration over the file is followed or
	// reported behind exactly as it would be after a revision uploaded by
	// hand. The one just written is current by construction.
	if p.repair != nil {
		p.repair.Followed = r.followOthers(ctx, p.src, p.repair.Version, p.reg.ID)
	}
	return &Result{Registration: p.reg, Source: p.src, Repair: p.repair}, nil
}

// planned is one registration under construction: where it lands, the source
// it is built over -- which is not the source that was handed in when the file
// had to be corrected first -- the record to write, the registration it
// replaces, and what a correction changed.
type planned struct {
	target   trino.ScratchConfig
	src      Source
	reg      Registration
	existing *Registration
	repair   *RepairReport
}

// plan works out everything a registration is made of and refuses on anything
// it cannot establish, before any statement runs.
//
// The order is what keeps a refusal from arriving after a write. Where the
// table lands and what it is called are settled first, so a caller who is not
// granted the connection, or who is asking for a name somebody else holds,
// learns that before their file is read -- and long before a correction of it
// would be saved.
func (r *Registrar) plan(ctx context.Context, caller Caller, src Source, req Request) (*planned, error) {
	// The plan is returned even when it fails, because part of it may already
	// have happened: a correction is a write, and the caller has to be able to
	// see one that a later refusal followed.
	p := &planned{src: src}
	if err := r.claim(ctx, caller, req, p); err != nil {
		return p, err
	}
	body, err := r.contentFor(ctx, p.src)
	if err != nil {
		return p, err
	}
	body, err = r.correct(ctx, caller, req, body, p)
	if err != nil {
		return p, err
	}
	return p, r.describe(ctx, p.src, body, &p.reg)
}

// claim settles where the registration lands and what it is called, refusing a
// name somebody else registered.
func (r *Registrar) claim(ctx context.Context, caller Caller, req Request, p *planned) error {
	target, err := r.resolveTarget(caller, req.Connection)
	if err != nil {
		return err
	}

	table := tableNameFor(caller, p.src, req)
	if table == "" {
		return refusedf("a table name could not be derived; give one explicitly")
	}

	// Everything a registration is except what the file itself decides. It is
	// filled in here rather than at the end so an audit event written for a
	// correction that a later refusal followed names the table it was for.
	p.target = target
	p.reg = Registration{
		SourceKind:   p.src.Kind,
		SourceID:     p.src.ID,
		Connection:   req.Connection,
		Catalog:      target.Catalog,
		Schema:       target.Schema,
		Table:        table,
		RegisteredBy: caller.Email,
		Follow:       req.Follow,
	}

	p.existing, err = r.deps.Store.ByName(ctx, req.Connection, target.Catalog, target.Schema, table)
	if err != nil {
		return fmt.Errorf("checking the table name: %w", err)
	}
	return mayReplace(caller, p.existing, target, table)
}

// correct answers a file that cannot be read as a table the way it is stored.
//
// Unasked, the answer is a refusal naming what is wrong with it. Asked, a
// corrected copy is saved as a new version of the file itself, through the
// version mechanism the source kind already has: the bytes that were uploaded
// stay as the version they are, the correction is the version on top of them,
// and it is revertible from the same panel every other version is. The
// registration is then built over the new version's directory, which holds
// that one file.
func (r *Registrar) correct(
	ctx context.Context, caller Caller, req Request, body []byte, p *planned,
) ([]byte, error) {
	defect := InspectCSV(body)
	if defect == nil {
		return body, nil
	}
	// Nothing is offered that the platform cannot honestly do. Bytes in an
	// encoding it does not convert are read wrongly by everything downstream,
	// including the correction, so a repair of that file would replace the
	// person's data with mojibake and report it as a fix; records that do not
	// match the header, or that cannot be parsed through, are ones the
	// correction refuses in turn, and offering it would answer the caller
	// twice with two different problems (#1449).
	if !defect.Correctable() {
		return nil, refusedf("%s %s", defect.Reason(), defect.remedy())
	}
	if !req.Repair {
		return nil, needsRepairf("%s Register it again asking for the file to be corrected, and a corrected"+
			" version is saved and registered; the file as it was uploaded stays as the version before it.",
			defect.Reason())
	}
	reviser := r.deps.Revisers[p.src.Kind]
	if reviser == nil {
		return nil, refusedf("%s This deployment keeps no version history for a stored %s, so there is nowhere to"+
			" save a corrected version; correct the file where it was written and upload it again.",
			defect.Reason(), p.src.Kind)
	}

	corrected, report, err := NormalizeCSV(body)
	if err != nil {
		return nil, err
	}
	revised, err := reviser.Revise(ctx, p.src, caller, corrected, repairSummary(report))
	if err != nil {
		return nil, fmt.Errorf("saving a corrected version of the file: %w", err)
	}

	p.src.Bucket, p.src.HeadKey, p.src.ContentType = revised.Bucket, revised.Key, contenttype.CSV
	p.repair = &RepairReport{NormalizeReport: report, Version: revised.Version}
	return corrected, nil
}

// describe fills in what only the file decides: the directory the table reads
// and the columns it declares.
func (r *Registrar) describe(ctx context.Context, src Source, body []byte, reg *Registration) error {
	location, err := r.locationFor(ctx, src)
	if err != nil {
		return err
	}
	columns, err := ReadHeaderColumns(body)
	if err != nil {
		return err
	}
	reg.Location, reg.Columns = location, columns
	reg.ID, err = r.newID()
	return err
}

// repairedFailure keeps a correction visible when what it was made for failed
// afterwards. The person's file changed either way, and an answer about the
// table alone would leave them not knowing that.
func repairedFailure(repair *RepairReport, err error) error {
	if repair == nil {
		return err
	}
	return &repaired{repair: repair, err: err}
}

// repaired is an error carrying the correction that preceded it, so a surface
// can say what happened to the file even when it cannot repeat what happened
// to the table.
type repaired struct {
	repair *RepairReport
	err    error
}

func (e *repaired) Error() string {
	return e.repair.Summary() + " The table was not created: " + e.err.Error()
}

func (e *repaired) Unwrap() error { return e.err }

// RepairOf returns the correction an error carries, or nil when it carries
// none. A surface that replaces a platform failure's text with its own uses it
// to keep the part about the file.
func RepairOf(err error) *RepairReport {
	var carried *repaired
	if errors.As(err, &carried) {
		return carried.repair
	}
	return nil
}

// resolveTarget applies the connection boundary and finds where registrations
// land on that connection.
func (r *Registrar) resolveTarget(caller Caller, connection string) (trino.ScratchConfig, error) {
	if r.deps.Scope != nil && !r.deps.Scope.AllowConnection(caller.Persona, connection) {
		return trino.ScratchConfig{}, ErrConnectionDenied
	}
	target, ok := r.deps.Trino.ScratchTarget(connection)
	if !ok || !target.Configured() {
		return trino.ScratchConfig{}, ErrNoScratchTarget
	}
	// Asked here rather than left to the DDL. Creating the table is write SQL,
	// so a read-only connection refuses it however its target is configured --
	// but it refuses from inside the Trino interceptor, as an error this layer
	// cannot classify, and the surface above can then only call it a platform
	// failure. Asking first turns the same outcome into a refusal that says
	// which connection and why.
	if !r.deps.Trino.AcceptsWrites(connection) {
		return trino.ScratchConfig{}, ErrConnectionReadOnly
	}
	return target, nil
}

// mayReplace decides whether this caller may take over a name someone already
// registered. A registration is replaced only by the person who made it or by
// an administrator; anyone else is told who holds it, because a shared schema
// where the last writer silently wins is a schema nobody can rely on.
func mayReplace(caller Caller, existing *Registration, target trino.ScratchConfig, table string) error {
	if existing == nil {
		return nil
	}
	if caller.IsAdmin || existing.RegisteredBy == caller.Email {
		return nil
	}
	return refusedf("%s.%s.%s is already registered by %s; choose another table name",
		target.Catalog, target.Schema, table, existing.RegisteredBy)
}

// locationFor returns the directory the external table points at, refusing a
// directory that holds anything besides the source object and the files Hive
// skips.
//
// The refusal is the whole protection. Hive reads every non-hidden object
// under an external location and parses it as CSV, so a stray file beside the
// content does not fail the query -- it comes back as rows. A hidden file is
// not read at all, which is why an asset's thumbnails are written under
// leading-dot names; anything Hive would read is named back to the caller.
//
// The rule cuts both ways: a source object under a hidden name of its own is
// refused, because a table over it would be built, recorded and queried
// without error and return nothing.
func (r *Registrar) locationFor(ctx context.Context, src Source) (string, error) {
	dir := DirectoryOf(src.HeadKey)
	if dir == "" {
		return "", refusedf("this file is not stored under a directory of its own, so no table can be pointed at it")
	}
	if hiddenToHive(fileNameOf(src.HeadKey)) {
		return "", refusedf(
			"a name beginning with \".\" or \"_\" is skipped by Trino, so a table over this file would return no rows; upload it under another name and register that")
	}
	objects := r.objectsFor(src.Kind)
	if objects == nil {
		return "", ErrUnavailable
	}

	entries, truncated, err := objects.ListDirectory(ctx, src.Bucket, dir)
	if err != nil {
		return "", fmt.Errorf("listing the file's directory: %w", err)
	}
	if truncated {
		return "", refusedf("this file's directory holds more objects than can be checked, so it cannot be registered")
	}

	var siblings []string
	for _, e := range entries {
		if e.Key == src.HeadKey {
			continue
		}
		name := fileNameOf(e.Key)
		if hiddenToHive(name) {
			continue
		}
		siblings = append(siblings, name)
	}
	if len(siblings) > 0 {
		sort.Strings(siblings)
		return "", refusedf(
			"a table reads every file in this file's directory, and %s sits beside it; move or remove it and register again",
			joinAnd(siblings))
	}

	return LocationURI(src.Bucket, dir), nil
}

// hiddenToHive reports whether Hive skips a file of this name. The Hive
// connector's hidden-file filter drops any name beginning with "." or "_",
// confirmed on Trino 476, so such a file sits in an external location without
// contributing rows. An asset's thumbnails are written under those names for
// exactly this reason, and every CSV asset rendered in the portal holds two of
// them beside its content.
func hiddenToHive(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// joinAnd renders a short list in prose so a refusal names what is in the way
// rather than printing a slice.
func joinAnd(items []string) string {
	if len(items) == 0 {
		return ""
	}
	head, last := items[:len(items)-1], items[len(items)-1]
	switch len(head) {
	case 0:
		return last
	case 1:
		return head[0] + " and " + last
	default:
		return strings.Join(head, ", ") + ", and " + last
	}
}

// contentFor reads the whole object a registration is built over.
//
// The whole body, not the first line: the header row is taken from it, and so
// is the answer to whether a line-based reader can read the file at all, which
// is a question only the rest of the bytes settle (#1441).
func (r *Registrar) contentFor(ctx context.Context, src Source) ([]byte, error) {
	if !isCSV(src.ContentType, src.HeadKey) {
		return nil, ErrNotCSV
	}
	objects := r.objectsFor(src.Kind)
	if objects == nil {
		return nil, ErrUnavailable
	}
	body, _, err := objects.GetObject(ctx, src.Bucket, src.HeadKey)
	if err != nil {
		return nil, fmt.Errorf("reading the file: %w", err)
	}
	if int64(len(body)) > r.deps.MaxBytes {
		return nil, refusedf("the file is larger than the %d MB a registration reads", r.deps.MaxBytes>>20)
	}
	return body, nil
}

// isCSV reports whether the source is a CSV. The stored content type decides
// it; the key's extension is the fallback for a record written before
// detection, or one whose type was never set.
func isCSV(declared, key string) bool {
	if ct := strings.ToLower(strings.TrimSpace(declared)); ct != "" {
		if base, _, found := strings.Cut(ct, ";"); found {
			ct = strings.TrimSpace(base)
		}
		if ct != "" && ct != contenttype.OctetStream && ct != "text/plain" {
			return strings.Contains(ct, "csv")
		}
	}
	return strings.HasSuffix(strings.ToLower(key), ".csv")
}

// tableNameFor derives the name the table takes.
func tableNameFor(caller Caller, src Source, req Request) string {
	raw := req.TableName
	if strings.TrimSpace(raw) == "" {
		// The filename is what a person recognizes the file by, and it is what
		// they would have typed.
		raw = fileNameOf(src.HeadKey)
		if SlugifyTableName(raw) == "" {
			raw = src.Name
		}
	}
	slug := SlugifyTableName(raw)
	if slug == "" {
		return ""
	}
	return PrefixedTableName(caller.Persona, slug)
}

// fileNameOf returns the last segment of an object key.
func fileNameOf(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	return key
}

// newID mints a registration id.
func (r *Registrar) newID() (string, error) {
	if r.deps.NewID == nil {
		return "", errors.New("no id generator configured")
	}
	id, err := r.deps.NewID()
	if err != nil {
		return "", fmt.Errorf("generating a registration id: %w", err)
	}
	return id, nil
}

// runDDL issues the statements a registration is made of, in order, and
// returns the ones that ran. What ran matters on a failure: a replacement
// begins by dropping the table it takes over, so whether that statement is
// among them decides whether the registration it replaces still describes
// anything.
func (r *Registrar) runDDL(ctx context.Context, connection string, ddl []string) ([]string, error) {
	ran := make([]string, 0, len(ddl))
	for _, stmt := range ddl {
		if err := r.deps.Trino.Exec(ctx, connection, stmt); err != nil {
			return ran, fmt.Errorf("registering the table: %w", err)
		}
		ran = append(ran, stmt)
	}
	return ran, nil
}

// forgetDroppedRegistration removes the row of a registration whose table the
// DDL already dropped before failing.
//
// A replacement runs DROP then CREATE. When the CREATE is the statement that
// failed, the previous table is gone and its row is the only thing still
// claiming it exists -- listed by toolView and hitTable, offered with a sample
// query, and not marked stale, because a replacement registering the same key
// leaves the location IsStale compares identical. Removing it is the same rule
// rollBackTable applies from the other side: nothing describes what is not
// there.
//
// When the DROP itself failed, nothing ran that changed anything and the row
// is still accurate, so it is left alone. That is the whole reason runDDL
// reports what it got through.
//
// A delete that fails is logged, not returned. It leaves the state this
// function exists to avoid, which is also the state before it existed: the
// answer already tells the caller to register again, and doing so replaces the
// row and rebuilds the table.
func (r *Registrar) forgetDroppedRegistration(ctx context.Context, a attempt, ran []string, cause error) {
	replaced := a.p.existing
	if replaced == nil || !slices.Contains(ran, dropTableStatement(a.p.reg)) {
		return
	}
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	delErr := r.deps.Store.Delete(ctx, replaced.ID)
	r.audit(ctx, auditRecord{
		caller: a.caller, reg: *replaced, ddl: ran, source: a.req.Source, err: errors.Join(cause, delErr),
	})
	if delErr != nil {
		slog.Warn("table registration: the replaced table was dropped and its record could not be removed",
			"registration", logsan.SanitizeForLog(replaced.ID),
			logFieldTable, logsan.SanitizeForLog(replaced.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(delErr.Error()))
	}
}

// attempt is one registration in progress: who asked, what they asked for, and
// the plan it was built from. Both halves of the reconciliation a partial
// failure needs are answered from it.
type attempt struct {
	caller Caller
	req    Request
	p      *planned
}

// cleanupTimeout bounds the reconciliation a failed registration does. The
// context it runs on has had cancellation taken off it, so this is the only
// thing standing between a wedged database and a request goroutine that never
// returns: the audit write goes to the same pool the write that failed came
// from.
const cleanupTimeout = 30 * time.Second

// cleanupContext is the context a reconciliation runs on. Cancellation comes
// off, because the write that failed may have failed BECAUSE the caller
// disconnected, and cleaning up what the request already did is exactly the
// work that has to outlive it; a deadline goes back on for the reason above.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// rollBackTable removes the table a registration just created when the row
// recording it could not be written, or when the row of the registration it
// replaces could not be removed.
//
// Without it a failed registration leaves the table in one of two states that
// no surface reports. Unrecorded, it stands in the scratch schema with nothing
// naming it: nothing lists it, and registering the same source again fails in
// Trino, because BuildDDL issues a DROP only when replacing a registration
// that exists and there is no longer one to find. Recorded by the registration
// it was replacing, it is worse -- the row describes the file and columns of
// the version before it while the table serves the one after.
//
// The drop is audited, because the event written a moment earlier says the
// table was created and the trail would otherwise end there. It runs on a
// context that is not the request's: a store write refused because the caller
// disconnected cancels the request, and cleanup of what the request already
// did is exactly the work that has to outlive it.
//
// A drop that fails is logged rather than returned, and joined onto the cause
// on the event so the trail says the table is still there. The caller is being
// told the registration failed either way, and the store error is the half of
// it they can act on.
func (r *Registrar) rollBackTable(ctx context.Context, a attempt, cause error) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	reg := a.p.reg
	stmt := dropTableStatement(reg)
	execErr := r.deps.Trino.Exec(ctx, a.req.Connection, stmt)
	r.audit(ctx, auditRecord{
		caller: a.caller, reg: reg, ddl: []string{stmt}, source: a.req.Source, err: errors.Join(cause, execErr),
	})
	if execErr != nil {
		slog.Warn("table registration: the record could not be written and the table it made could not be dropped",
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(execErr.Error()))
	}
}

// Unregister drops a registered table and forgets it.
//
// Dropping a Hive external table removes the metastore entry and leaves the
// objects, so unregistering never touches the file the person uploaded. The
// row goes even when the DROP fails: the alternative is a record of a table
// nobody can remove through the platform, and the DROP is IF EXISTS so a table
// already gone is not an error in the first place.
func (r *Registrar) Unregister(ctx context.Context, caller Caller, id, source string) error {
	if !r.Available() {
		return ErrUnavailable
	}
	reg, err := r.mayUnregister(ctx, caller, id)
	if err != nil {
		return err
	}

	stmt := dropTableStatement(*reg)
	execErr := r.deps.Trino.Exec(ctx, reg.Connection, stmt)
	r.audit(ctx, auditRecord{caller: caller, reg: *reg, ddl: []string{stmt}, source: source, err: execErr})
	if execErr != nil {
		slog.Warn("table registration: dropping the table failed; the record is removed anyway",
			"registration", logsan.SanitizeForLog(reg.ID),
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(execErr.Error()))
	}
	if err := r.deps.Store.Delete(ctx, reg.ID); err != nil {
		return fmt.Errorf("removing the registration: %w", err)
	}
	return nil
}

// mayUnregister reads the registration and decides whether this caller may
// drop it: the person who made it, or an administrator. An id that does not
// exist is answered before identity, so a probe learns nothing about what is
// registered.
func (r *Registrar) mayUnregister(ctx context.Context, caller Caller, id string) (*Registration, error) {
	reg, err := r.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's ErrNotFound is what a surface renders
	}
	if reg == nil {
		return nil, ErrNotFound
	}
	if r.deps.Scope != nil && !r.deps.Scope.AllowConnection(caller.Persona, reg.Connection) {
		return nil, ErrConnectionDenied
	}
	if caller.Email == "" {
		return nil, ErrNoIdentity
	}
	if !caller.IsAdmin && reg.RegisteredBy != caller.Email {
		return nil, refusedf("%s was registered by %s and only they or an administrator can remove it",
			reg.QualifiedName(), reg.RegisteredBy)
	}
	return reg, nil
}

// UnregisterAllForSource drops every table registered over a source. It is
// what a resource or asset delete calls: the file is going, and a table over
// where it used to be would return nothing and explain nothing.
//
// It is best-effort by design. The delete that triggered it has its own
// reasons to succeed, and failing it because a scratch table could not be
// dropped would make an unrelated Trino outage look like a broken delete.
func (r *Registrar) UnregisterAllForSource(ctx context.Context, kind, sourceID string) {
	if !r.Available() {
		return
	}
	regs, err := r.deps.Store.BySource(ctx, kind, sourceID)
	if err != nil {
		slog.Warn("table registration: could not list registrations of a deleted source",
			"kind", logsan.SanitizeForLog(kind), "source", logsan.SanitizeForLog(sourceID), logFieldError, logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, reg := range regs {
		if err := r.deps.Trino.Exec(ctx, reg.Connection, dropTableStatement(reg)); err != nil {
			slog.Warn("table registration: dropping the table of a deleted source failed",
				logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()), logFieldError, logsan.SanitizeForLog(err.Error()))
		}
		if err := r.deps.Store.Delete(ctx, reg.ID); err != nil {
			slog.Warn("table registration: removing the record of a deleted source failed",
				"registration", logsan.SanitizeForLog(reg.ID), logFieldError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// BySource returns every registration over one source.
func (r *Registrar) BySource(ctx context.Context, kind, sourceID string) ([]Registration, error) {
	if !r.Available() {
		return nil, nil //nolint:nilnil // no registrations is an answer, not a failure
	}
	return r.deps.Store.BySource(ctx, kind, sourceID) //nolint:wrapcheck // transparent read pass-through
}

// ForSources returns the registrations of many sources at once.
func (r *Registrar) ForSources(ctx context.Context, kind string, ids []string) (map[string][]Registration, error) {
	if !r.Available() || len(ids) == 0 {
		return nil, nil //nolint:nilnil // no registrations is an answer, not a failure
	}
	return r.deps.Store.ForSources(ctx, kind, ids) //nolint:wrapcheck // transparent read pass-through
}

// List returns a page of registrations across every source, with the total the
// filter matched.
//
// The connection boundary is the filter's, not this method's: the caller of a
// listing is the surface that enumerated what this person reaches, and pushing
// it into the query is what keeps the count and the page in agreement.
func (r *Registrar) List(ctx context.Context, f Filter) ([]Registration, int, error) {
	if !r.Available() {
		return nil, 0, nil
	}
	return r.deps.Store.List(ctx, f) //nolint:wrapcheck // transparent read pass-through
}

// Visible reads one registration for a caller who may see it.
//
// A registration on a connection the caller's persona is not granted is
// answered as ErrNotFound rather than as a denial: the caller cannot query the
// table, cannot act on it, and telling them it exists discloses a table name
// somebody else registered in a schema they have no reach into. An
// administrator is unrestricted, as everywhere else.
func (r *Registrar) Visible(ctx context.Context, caller Caller, id string) (*Registration, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	reg, err := r.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's ErrNotFound is what a surface renders
	}
	if reg == nil {
		return nil, ErrNotFound
	}
	if !r.maySee(caller, reg.Connection) {
		return nil, ErrNotFound
	}
	return reg, nil
}

// maySee applies the persona connection boundary to a read.
//
// A deployment with no scope wired has no persona rules to apply, which is the
// single-persona shape every connection is reachable in; denying there would
// hide every table on it.
func (r *Registrar) maySee(caller Caller, connection string) bool {
	if caller.IsAdmin || r.deps.Scope == nil {
		return true
	}
	return r.deps.Scope.AllowConnection(caller.Persona, connection)
}

// audit records the statement the registrar ran, the way the portal's DataHub
// writes are recorded: same event kind, same fields, so a registration shows
// up in the audit trail beside every other write the platform makes on a
// caller's behalf. Best-effort -- a logging failure never fails the operation.
func (r *Registrar) audit(ctx context.Context, rec auditRecord) {
	if r.deps.Audit == nil {
		return
	}
	source := rec.source
	if source == "" {
		source = "portal"
	}

	ev := audit.NewEvent("table_register")
	ev.UserID = rec.caller.UserID
	ev.UserEmail = rec.caller.Email
	ev.Persona = rec.caller.Persona
	ev.ToolkitKind = "trino"
	ev.ToolkitName = rec.reg.Connection
	ev.Connection = rec.reg.Connection
	ev.Parameters = map[string]any{
		"sql":         strings.Join(rec.ddl, ";\n"),
		"table":       rec.reg.QualifiedName(),
		"location":    rec.reg.Location,
		"source_kind": rec.reg.SourceKind,
		"source_id":   rec.reg.SourceID,
	}
	// A correction rewrote somebody's file on their behalf, which is a write
	// in its own right and is recorded beside the statement that followed it.
	if rec.repair != nil {
		ev.Parameters["repaired"] = rec.repair.Summary()
	}
	ev.Source = source
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized = true
	ev.Success = rec.err == nil
	if rec.err != nil {
		ev.ErrorMessage = rec.err.Error()
	}
	if err := r.deps.Audit.Log(ctx, *ev); err != nil {
		slog.Warn("table registration audit log failed", logFieldError, logsan.SanitizeForLog(err.Error()),
			logFieldTable, logsan.SanitizeForLog(rec.reg.QualifiedName()))
	}
}

// auditRecord is what one audited registration or unregistration carries: who
// did it, to which registration, with which statements, and how it ended.
type auditRecord struct {
	caller Caller
	reg    Registration
	ddl    []string
	source string
	// repair is what a correction of the file changed, or nil when none was
	// needed.
	repair *RepairReport
	err    error
}
