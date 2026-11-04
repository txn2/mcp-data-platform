// Package appdbconsole is an administrator's direct way into an application
// database (#2427): statements run in one transaction as the connection's own
// login, READ ONLY unless asked otherwise, and canceling or terminating a
// backend of the connection's own roles (#2410). The admin API calls it.
package appdbconsole

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// The signals a backend can be sent.
const (
	SignalCancel    = "cancel"
	SignalTerminate = "terminate"
)

// Signaller runs a read-only statement and names the connection's roles: the
// postgres toolkit.
type Signaller interface {
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	ConnectionRoles(connection string) []string
}

// RefusedError is a signal the platform will not send, with why.
type RefusedError struct{ msg string }

func (e *RefusedError) Error() string { return e.msg }

// signalSQL sends the signal in the statement that checks the backend is in
// this database and logged in as one of the connection's roles, so nothing
// changes between the check and the act.
const signalSQL = `SELECT %s(a.pid) AS sent FROM pg_stat_activity a
WHERE a.pid = $1 AND a.datname = current_database() AND a.usename = ANY($2::text[])`

// whoSQL names the role and database of a backend the signal was not sent to.
const whoSQL = `SELECT COALESCE(usename::text, '') AS usename, COALESCE(datname::text, '') AS datname
FROM pg_stat_activity WHERE pid = $1`

// SignalResult is what a signal did.
type SignalResult struct {
	Connection string `json:"connection"`
	Signal     string `json:"signal"`
	PID        int64  `json:"pid"`
	// Sent is whether PostgreSQL delivered it.
	Sent bool `json:"sent"`
}

// Signal cancels the statement a backend runs, or terminates the backend
// (#2410). It acts only on a backend in the connection's database logged in
// as one of the connection's roles: any other backend is not the platform's.
func Signal(ctx context.Context, s Signaller, connection, signal string, pid int64) (*SignalResult, error) {
	fn := map[string]string{SignalCancel: "pg_cancel_backend", SignalTerminate: "pg_terminate_backend"}[signal]
	if fn == "" {
		return nil, &RefusedError{msg: fmt.Sprintf("signal %q is not cancel or terminate", signal)}
	}
	roles := s.ConnectionRoles(connection)
	if len(roles) == 0 {
		return nil, &RefusedError{msg: fmt.Sprintf("no application database named %q", connection)}
	}
	rolesParam := make([]any, len(roles))
	for i, r := range roles {
		rolesParam[i] = r
	}
	res, err := s.Query(ctx, postgres.Statement{Connection: connection, SQL: fmt.Sprintf(signalSQL, fn), Params: []any{pid, rolesParam}})
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	if len(res.Rows) == 1 {
		sent, _ := res.Rows[0][0].(bool)
		return &SignalResult{Connection: connection, Signal: signal, PID: pid, Sent: sent}, nil
	}
	who, err := s.Query(ctx, postgres.Statement{Connection: connection, SQL: whoSQL, Params: []any{pid}})
	if err != nil || len(who.Rows) == 0 {
		return nil, &RefusedError{msg: fmt.Sprintf("there is no backend %d", pid)}
	}
	role, _ := who.Rows[0][0].(string)
	db, _ := who.Rows[0][1].(string)
	return nil, &RefusedError{msg: fmt.Sprintf(
		"backend %d is logged in as %q in database %q, not as one of connection %q's roles in its database; it is not the platform's to %s",
		pid, role, db, connection, signal)}
}
