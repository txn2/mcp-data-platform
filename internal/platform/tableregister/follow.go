package tableregister

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// A registration pins a directory, and a revision of a managed resource or a
// version of a portal asset moves the source's head to a new one (#1536). A
// registration made with Follow is moved along: the same DDL a re-registration
// runs, over the new head, under the same registered_by, from the one place
// each kind's head moves. A pinned registration is left where it is and the
// write is told so, which is the half that removes the surprise for a pinned
// table too -- the write that put it behind says it did.

// FollowOutcome is what a write did to one registration over the file it
// changed: the table followed, is pinned and now behind, or follows and could
// not be moved.
type FollowOutcome struct {
	RegistrationID string `json:"registration_id"`
	// Table is the qualified name a query writes, and Connection where it
	// lives; together they name the table the way every surface does.
	Table      string `json:"table"`
	Connection string `json:"connection"`
	// Followed means the table now reads the version the write produced.
	Followed bool `json:"followed"`
	// Version is the version the write produced, which a followed table now
	// reads and a table that did not follow is behind.
	Version int `json:"version"`
	// Pinned means the registration was made without follow, so it stays on
	// the version it was registered over by design.
	Pinned bool `json:"pinned,omitempty"`
	// Reason is why a following registration was not moved. Empty when it
	// followed or is pinned.
	Reason string `json:"reason,omitempty"`
	// ColumnsChanged means the new version's header differs from the one the
	// table declared, so the table was rebuilt with the new columns.
	ColumnsChanged bool `json:"columns_changed,omitempty"`
	// Missing means the table this registration names no longer exists on
	// its connection: a write that ran DROP TABLE on the connection found it
	// gone afterwards (#1546). Reason says which write. The registration is
	// kept, with the reason recorded on it, so the listing says so too.
	Missing bool `json:"missing,omitempty"`
}

// Sentence renders the outcome as the write reports it: the table, what
// happened to it, and what to do when something is left to do.
func (o FollowOutcome) Sentence() string {
	name := o.Table + " on " + o.Connection
	switch {
	case o.Followed:
		s := name + " now reads version " + strconv.Itoa(o.Version) + "."
		if o.ColumnsChanged {
			s += " Its columns changed with the file."
		}
		return s
	case o.Missing:
		return name + " no longer exists: " + o.Reason + " Register it again to restore it."
	case o.Pinned:
		return name + " is pinned to the version it was registered over and is now behind this file;" +
			" register it again to move it, with follow left on if it should keep up with the file."
	default:
		return name + " follows this file but could not be moved to version " + strconv.Itoa(o.Version) +
			": " + o.Reason + " It is behind the file until it is registered again."
	}
}

// Sentences renders every outcome, in order, as the lines a write's result
// carries.
func Sentences(outcomes []FollowOutcome) []string {
	if len(outcomes) == 0 {
		return nil
	}
	out := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		out = append(out, o.Sentence())
	}
	return out
}

// followEvent is the audit tool name a follow is recorded under, beside the
// table_register events of the registrations it moves.
const followEvent = "table_follow"

// FollowSource moves every following registration of a source onto the head
// the write just produced, and reports what happened to each registration
// over the file, pinned ones included.
//
// It never fails the write. The file changed; that is the caller's write and
// it succeeded. A follow whose DDL fails leaves the registration where it
// was, records why, and says so in the outcome, so the registration is behind
// the file exactly as a pinned one is, with the reason attached.
//
// version is the revision or version number the write produced, which is what
// the outcome names.
func (r *Registrar) FollowSource(ctx context.Context, src Source, version int) []FollowOutcome {
	return r.followOthers(ctx, src, version, "")
}

// followOthers is FollowSource without one registration: the one a correction
// just registered over the version it wrote, which is current by construction
// and would otherwise be reported as having followed itself.
func (r *Registrar) followOthers(ctx context.Context, src Source, version int, exceptID string) []FollowOutcome {
	if !r.Available() {
		return nil
	}
	regs, err := r.deps.Store.BySource(ctx, src.Kind, src.ID)
	if err != nil {
		slog.Warn("table registration: could not list the registrations of a revised source",
			"kind", logsan.SanitizeForLog(src.Kind), "source", logsan.SanitizeForLog(src.ID),
			logFieldError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	var (
		out  []FollowOutcome
		head *followHead
		// movedOn is each connection a follow ran DDL on, with the table
		// that moved: every such connection is checked afterwards for the
		// other tables the DROP may have taken with it (#1546), once,
		// whatever the number of tables moved on it.
		movedOn = map[string]string{}
	)
	for _, reg := range regs {
		if reg.ID == exceptID {
			continue
		}
		o, moved := r.followOne(ctx, reg, src, version, &head)
		out = append(out, o)
		if moved {
			if _, seen := movedOn[o.Connection]; !seen {
				movedOn[o.Connection] = o.Table
			}
		}
	}
	for _, connection := range sortedKeys(movedOn) {
		out = append(out, r.reconcileConnection(ctx, connection, "",
			"the table was removed while "+movedOn[connection]+" was moved to version "+strconv.Itoa(version)+".")...)
	}
	return out
}

// sortedKeys orders a set of connection names so a report is stable.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// reconcileConnection asks the connection whether every registration on it,
// except one, still holds its table, records the ones that do not, and
// reports each as an outcome (#1546).
//
// It runs after a write that issued DROP TABLE on the connection. Trino's
// file metastore deletes a table by listing its metadata directory, and an
// object store whose prefix listing does not stop at a directory boundary
// answers that listing with a name-prefix sibling's files too, so dropping
// `x` can take `x_pinned` with it. The write that did it is the one place
// that can say so; a registration already recorded missing is not recorded
// again, and a lookup that fails is logged rather than reported, since a
// connection that cannot answer has not said the table is gone.
func (r *Registrar) reconcileConnection(ctx context.Context, connection, exceptID, reason string) []FollowOutcome {
	var out []FollowOutcome
	for offset := 0; ; offset += MaxListLimit {
		page, total, err := r.deps.Store.List(ctx, Filter{Connections: []string{connection}, Limit: MaxListLimit, Offset: offset})
		if err != nil {
			slog.Warn("table registration: could not list a connection's registrations after a drop",
				"connection", logsan.SanitizeForLog(connection), logFieldError, logsan.SanitizeForLog(err.Error()))
			return nil
		}
		for _, reg := range page {
			if o, missing := r.reconcileOne(ctx, reg, exceptID, reason); missing {
				out = append(out, o)
			}
		}
		if offset+len(page) >= total || len(page) == 0 {
			return out
		}
	}
}

// reconcileOne checks one registration and records it when its table is
// gone. It reports false for a registration that is fine, excepted, already
// recorded missing, or whose lookup failed.
func (r *Registrar) reconcileOne(ctx context.Context, reg Registration, exceptID, reason string) (FollowOutcome, bool) {
	if reg.ID == exceptID || strings.HasPrefix(reg.FollowError, missingPrefix) {
		return FollowOutcome{}, false
	}
	exists, err := r.deps.Trino.TableExists(ctx, reg.Connection, reg.Catalog, reg.Schema, reg.Table)
	if err != nil {
		slog.Warn("table registration: could not check whether a table still exists after a drop",
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()), logFieldError, logsan.SanitizeForLog(err.Error()))
		return FollowOutcome{}, false
	}
	if exists {
		return FollowOutcome{}, false
	}
	recorded := missingPrefix + reason
	if err := r.deps.Store.RecordFollowFailure(ctx, reg.ID, recorded); err != nil {
		slog.Warn("table registration: could not record that a table no longer exists",
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()), logFieldError, logsan.SanitizeForLog(err.Error()))
	}
	return FollowOutcome{
		RegistrationID: reg.ID, Table: reg.QualifiedName(), Connection: reg.Connection,
		Missing: true, Reason: reason,
	}, true
}

// missingPrefix opens the follow_error a registration whose table is gone
// carries, so the listing's reader and a later check both recognize it.
const missingPrefix = "The table no longer exists: "

// followHead is what the source's new head decides for every registration
// over it, read once per write rather than once per registration.
type followHead struct {
	location string
	columns  []Column
	err      error
}

// followOne moves one registration, or explains why it stays.
func (r *Registrar) followOne(
	ctx context.Context, reg Registration, src Source, version int, head **followHead,
) (FollowOutcome, bool) {
	outcome := FollowOutcome{
		RegistrationID: reg.ID, Table: reg.QualifiedName(), Connection: reg.Connection, Version: version,
	}
	current := !reg.IsStale(src.Bucket, src.HeadKey)
	if !reg.Follow {
		// A pinned registration that already reads the new head -- the head
		// was written twice at the same directory -- is current, and saying
		// it is behind would be false.
		outcome.Followed, outcome.Pinned = current, !current
		return outcome, false
	}
	if *head == nil {
		*head = r.readHead(ctx, src)
	}
	if (*head).err != nil {
		return r.followFailed(ctx, reg, outcome, (*head).err), false
	}
	target := **head
	outcome.ColumnsChanged = !sameColumns(reg.Columns, target.columns)
	if reg.Location == target.location && !outcome.ColumnsChanged {
		return r.alreadyThere(ctx, reg, outcome), false
	}
	return r.moveTable(ctx, reg, target, outcome)
}

// readHead reads what the new head decides: the directory the table has to
// point at and the columns its header declares. It is the registration's own
// description of a file, with one difference: nothing is corrected. A file
// that cannot be read as a table the way it is stored leaves the registration
// where it was, and the reason says how to move it.
func (r *Registrar) readHead(ctx context.Context, src Source) *followHead {
	body, err := r.contentFor(ctx, src)
	if err != nil {
		return &followHead{err: err}
	}
	if defect := tablecsv.Inspect(body); defect != nil {
		if !defect.Correctable() {
			return &followHead{err: refusedf("%s %s", defect.Reason(), defect.Remedy())}
		}
		return &followHead{err: refusedf(
			"%s Register it again asking for the file to be corrected, and the corrected version is registered.",
			defect.Reason())}
	}
	location, err := r.locationFor(ctx, src)
	if err != nil {
		return &followHead{err: err}
	}
	columns, err := ReadHeaderColumns(body)
	if err != nil {
		return &followHead{err: err}
	}
	return &followHead{location: location, columns: columns}
}

// alreadyThere answers a following registration that already reads the new
// head with nothing to move. A failure recorded by an earlier follow is
// cleared, because the registration is where the file is.
func (r *Registrar) alreadyThere(ctx context.Context, reg Registration, outcome FollowOutcome) FollowOutcome {
	if reg.FollowError != "" {
		if err := r.deps.Store.Relocate(ctx, reg.ID, reg.Location, reg.Columns); err != nil {
			return r.followFailed(ctx, reg, outcome, fmt.Errorf("updating the registration: %w", err))
		}
	}
	outcome.Followed = true
	return outcome
}

// moveTable runs the DDL that points the table at the new head and records
// where it now reads. The second result says whether the DDL ran to
// completion, which is what decides whether the connection is checked for
// tables the DROP took with it.
//
// The DDL is a replacement's: DROP, then CREATE at the new location with the
// new columns. A CREATE that fails after the DROP ran has taken the table
// away, and a registration "left where it was" would then name a table that
// is not there, so the table is put back at its old location before the
// failure is reported. A DROP that fails changed nothing.
func (r *Registrar) moveTable(
	ctx context.Context, reg Registration, target followHead, outcome FollowOutcome,
) (FollowOutcome, bool) {
	moved := reg
	moved.Location, moved.Columns = target.location, target.columns
	ddl := BuildDDL(moved, true)
	ran, execErr := r.runDDL(ctx, reg.Connection, ddl)
	r.auditFollow(ctx, followRecord{from: reg, to: moved, ddl: ran, version: outcome.Version, err: execErr})
	if execErr != nil {
		r.restoreDroppedTable(ctx, reg, ran, execErr)
		return r.followFailed(ctx, reg, outcome, execErr), false
	}
	if err := r.deps.Store.Relocate(ctx, reg.ID, moved.Location, moved.Columns); err != nil {
		return r.followFailed(ctx, reg, outcome,
			fmt.Errorf("the table was moved but its record could not be updated: %w", err)), false
	}
	outcome.Followed = true
	return outcome, true
}

// restoreDroppedTable puts a table back at its old location when the CREATE at
// the new one failed after the DROP had run. A restore that fails too is
// logged and audited: the registration then names a table that is gone, and
// the failure the outcome carries tells the caller to register again, which
// rebuilds it.
func (r *Registrar) restoreDroppedTable(ctx context.Context, reg Registration, ran []string, cause error) {
	if !slices.Contains(ran, dropTableStatement(reg)) {
		return
	}
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	stmt := createTableStatement(reg)
	execErr := r.deps.Trino.Exec(ctx, reg.Connection, stmt)
	r.auditFollow(ctx, followRecord{from: reg, to: reg, ddl: []string{stmt}, err: errors.Join(cause, execErr)})
	if execErr != nil {
		slog.Warn("table registration: a follow dropped the table and could not put it back",
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(execErr.Error()))
	}
}

// followFailed records why a registration was not moved, on the registration
// and in the log, and reports it.
func (r *Registrar) followFailed(
	ctx context.Context, reg Registration, outcome FollowOutcome, cause error,
) FollowOutcome {
	outcome.Reason = cause.Error()
	slog.Warn("table registration: a following registration could not be moved",
		logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
		logFieldError, logsan.SanitizeForLog(cause.Error()))
	if err := r.deps.Store.RecordFollowFailure(ctx, reg.ID, outcome.Reason); err != nil {
		slog.Warn("table registration: the follow failure could not be recorded",
			"registration", logsan.SanitizeForLog(reg.ID), logFieldError, logsan.SanitizeForLog(err.Error()))
	}
	return outcome
}

// sameColumns reports whether two column lists declare the same table.
func sameColumns(a, b []Column) bool {
	return slices.Equal(a, b)
}

// followRecord is one audited follow: the registration as it was, as it is
// after the move, the statements that ran, the version it followed to, and how
// it ended.
type followRecord struct {
	from, to Registration
	ddl      []string
	version  int
	err      error
}

// auditFollow records a follow the way a registration is recorded, under the
// registrant rather than the caller of the write: the follow is the
// registration's own behavior, and the person who asked for it is the one
// whose table moved. A changed header is recorded with the columns before and
// after, which is the one thing about a follow the DDL alone does not say
// plainly.
func (r *Registrar) auditFollow(ctx context.Context, rec followRecord) {
	if r.deps.Audit == nil {
		return
	}
	ev := audit.NewEvent(followEvent)
	ev.UserEmail = rec.from.RegisteredBy
	ev.ToolkitKind = "trino"
	ev.ToolkitName = rec.from.Connection
	ev.Connection = rec.from.Connection
	ev.Parameters = map[string]any{
		"sql":              strings.Join(rec.ddl, ";\n"),
		"table":            rec.from.QualifiedName(),
		"location":         rec.to.Location,
		"source_kind":      rec.from.SourceKind,
		"source_id":        rec.from.SourceID,
		"followed_version": rec.version,
	}
	if !sameColumns(rec.from.Columns, rec.to.Columns) {
		ev.Parameters["columns_before"] = columnNames(rec.from.Columns)
		ev.Parameters["columns_after"] = columnNames(rec.to.Columns)
	}
	ev.Source = "follow"
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized = true
	ev.Success = rec.err == nil
	if rec.err != nil {
		ev.ErrorMessage = rec.err.Error()
	}
	if err := r.deps.Audit.Log(ctx, *ev); err != nil {
		slog.Warn("table follow audit log failed", logFieldError, logsan.SanitizeForLog(err.Error()),
			logFieldTable, logsan.SanitizeForLog(rec.from.QualifiedName()))
	}
}

// columnNames lists the names of a column list, for an audit parameter.
func columnNames(cols []Column) []string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return names
}
