package tableregister

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister/srcdir"
)

// settleCreate answers a registration whose CREATE TABLE was refused because
// the table exists, and returns the statements the registration is now made
// of, the ones that ran, and how it ended (#2131).
//
// A CREATE the platform stopped waiting for -- its deadline passed, or the
// caller went away -- may still be completed by the coordinator. The table it
// leaves has no row, so the next registration of the same file issues a bare
// CREATE and is refused "already exists", and so is every one after it: a
// scheduled export that registers its output fails on every run until someone
// drops the table by hand.
//
// Where no registration holds the name, the table is adopted when it reads
// this file's own directory: nothing but an earlier registration of this file
// can have put it there, so it is replaced the way a recorded registration
// would be. dropAbandoned narrows the window from the other side; this is what
// guarantees the next registration succeeds, since the coordinator may commit
// an abandoned CREATE after the drop.
//
// Any other failure, and a failure of a statement other than the CREATE, is
// returned as it came.
func (r *Registrar) settleCreate(ctx context.Context, a attempt, ddl, ran []string, err error) (planned, done []string, result error) {
	if !createFailed(a, ddl, ran) || a.p.existing != nil || abandoned(ctx, err) || !alreadyExists(err) {
		return ddl, ran, err
	}
	return r.adoptOrphan(ctx, a, ddl, ran, err)
}

// createFailed reports whether the statement that failed is the registration's
// CREATE TABLE: the first of ddl that is not among ran.
func createFailed(a attempt, ddl, ran []string) bool {
	return len(ran) < len(ddl) && ddl[len(ran)] == createTableStatement(a.p.reg)
}

// adoptOrphan replaces a table no registration records when it reads this
// file's own directory, and refuses the name otherwise.
func (r *Registrar) adoptOrphan(ctx context.Context, a attempt, ddl, ran []string, cause error) (planned, done []string, result error) {
	reg := a.p.reg
	location, found, err := r.deps.Trino.TableLocation(ctx, a.req.Connection, reg.Catalog, reg.Schema, reg.Table)
	if err != nil {
		return ddl, ran, failedf("reading the table that holds the name", errors.Join(cause, err))
	}
	if !found {
		// Gone between the refusal and the look: the next attempt creates it.
		return ddl, ran, cause
	}
	if !ownsLocation(a.p.src, location) {
		return ddl, ran, refusedf("%s already exists in the scratch schema and no registration holds it; "+
			"it reads %s, not this file; choose another table name", reg.QualifiedName(), srcdir.Describe(location))
	}
	retry := []string{DropTableStatement(reg), createTableStatement(reg)}
	more, err := r.runDDL(ctx, a.req.Connection, retry)
	return append(ddl, retry...), append(ran, more...), err
}

// dropAbandoned takes out the table a CREATE the platform stopped waiting for
// may have made, when it reads this file's own directory, so the next
// registration starts from nothing. It runs on a context the caller's
// cancellation does not reach, because the caller going away is one of the
// ways a CREATE is abandoned, and the drop is audited after the event of the
// CREATE it follows. A failure is logged: the registration has already
// failed, and the next one adopts what is left.
func (r *Registrar) dropAbandoned(ctx context.Context, a attempt, ddl, ran []string, cause error) {
	if !createFailed(a, ddl, ran) || !abandoned(ctx, cause) {
		return
	}
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	reg := a.p.reg
	location, found, err := r.deps.Trino.TableLocation(ctx, a.req.Connection, reg.Catalog, reg.Schema, reg.Table)
	if err == nil && (!found || !ownsLocation(a.p.src, location)) {
		return
	}
	stmt := DropTableStatement(reg)
	if err == nil {
		err = r.deps.Trino.Exec(ctx, a.req.Connection, stmt)
		r.audit(ctx, auditRecord{
			caller: a.caller, reg: reg, ddl: []string{stmt}, source: a.req.Source, err: errors.Join(cause, err),
		})
	}
	if err != nil {
		slog.WarnContext(ctx, "table registration: a create that did not finish may have left its table behind",
			logFieldTable, logsan.SanitizeForLog(reg.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(err.Error()))
	}
}

// ownsLocation reports whether an external location lies inside the
// directory a source keeps every version of its file under.
func ownsLocation(src Source, location string) bool {
	return srcdir.Owns(src.Bucket, src.ID, src.HeadKey, location)
}

// abandoned reports whether a statement failed because the platform stopped
// waiting for it rather than because the coordinator refused it, which is
// the failure that leaves the statement's outcome unknown.
func abandoned(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil
}

// alreadyExists reports whether a CREATE TABLE was refused because the table
// is there. The client carries the coordinator's message, not its error code.
func alreadyExists(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}
