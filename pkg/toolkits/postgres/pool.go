package postgres

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/txn2/mcp-data-platform/internal/apigwtls"
)

// applicationName is what pg_stat_activity shows for the platform's sessions.
const applicationName = "mcp-data-platform"

// decimal is the base numbers are written in.
const decimal = 10

// ConnectGuard is asked about every physical connection a pool opens, before
// the connection serves a statement. A non-nil error closes the connection and
// fails the call that needed it. The platform's guard refuses a connection
// that reached the platform's own database (ownguard.go).
type ConnectGuard func(ctx context.Context, conn *pgx.Conn) error

// poolConfig is the pgxpool configuration for c.
//
// The connection string pgx parses carries only the address, the database, the
// user and sslmode=disable. Everything a connection string would otherwise let
// the process environment supply is set here instead: the password (so a
// .pgpass match pgconn reads when none is given is overwritten), the TLS
// configuration (built from the material the operator saved, never from
// PGSSL* files), the fallbacks (none, so a TLS failure is never retried in the
// clear), and the runtime parameters (so PGOPTIONS cannot add settings to the
// operator's sessions).
func poolConfig(c Config, guard ConnectGuard, tracer pgx.QueryTracer) (*pgxpool.Config, error) {
	// Validate bounds max_conns too; the pool size is checked here as well
	// because it is narrowed to int32 below, and a Config reaches this
	// function from callers other than ParseConfig.
	if c.MaxConns < 1 || c.MaxConns > MaxMaxConns {
		return nil, fmt.Errorf("postgres: max_conns must be between 1 and %d", MaxMaxConns)
	}
	pc, err := pgxpool.ParseConfig(connString(c))
	if err != nil {
		return nil, fmt.Errorf("postgres: %s", scrubConnString(err.Error()))
	}
	tlsCfg, err := tlsConfig(c)
	if err != nil {
		return nil, err
	}
	cc := pc.ConnConfig
	cc.Password = c.Password
	cc.TLSConfig = tlsCfg
	cc.Fallbacks = nil
	cc.ConnectTimeout = DefaultConnectTimeout
	cc.RuntimeParams = map[string]string{
		"application_name":  applicationName,
		"statement_timeout": strconv.FormatInt(c.StatementTimeout.Milliseconds(), decimal),
	}
	cc.Tracer = tracer
	// pgx's own caches stay unused: a statement is described, then run with
	// its parameters typed by that description (run.go). With statement_cache
	// on, the description is a statement prepared on the physical connection
	// under a name derived from its text (stmtcache.go), which the reset below
	// keeps; with it off, every call describes the statement again.
	cc.DefaultQueryExecMode = pgx.QueryExecModeExec
	pc.MaxConns = int32(c.MaxConns) // #nosec G115 -- bounded by the check at the top of this function
	pc.MinConns = 0
	if guard != nil {
		pc.AfterConnect = guard
	}
	pc.AfterRelease = resetSession(c.StatementCache)
	return pc, nil
}

// resetTimeout bounds the reset a released connection runs.
const resetTimeout = 5 * time.Second

// discardAll returns a session to the state it opened in.
const discardAll = "DISCARD ALL"

// discardAllButPrepared is DISCARD ALL without its DEALLOCATE ALL: the
// sequence PostgreSQL documents DISCARD ALL as equivalent to, less the one
// step that would empty the statement cache. Every prepared statement on a
// connection is the platform's own, because a script's PREPARE, DEALLOCATE and
// DISCARD are refused (run.go).
const discardAllButPrepared = "CLOSE ALL; SET SESSION AUTHORIZATION DEFAULT; RESET ALL; UNLISTEN *; " +
	"SELECT pg_advisory_unlock_all(); DISCARD PLANS; DISCARD TEMP; DISCARD SEQUENCES"

// resetSession returns the reset a released connection runs before another
// call uses it: it undoes a session-level SET (a role, a search_path, a
// timeout), LISTEN, temporary tables and advisory locks a caller's statement
// left behind. Without it one caller's SET ROLE would be the next caller's
// role. With the statement cache on, the connection's prepared statements are
// kept (discardAllButPrepared). A connection the reset fails on is closed.
func resetSession(keepPrepared bool) func(*pgx.Conn) bool {
	reset := discardAll
	if keepPrepared {
		reset = discardAllButPrepared
	}
	return func(c *pgx.Conn) bool {
		ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
		defer cancel()
		_, err := c.PgConn().Exec(ctx, reset).ReadAll()
		return err == nil
	}
}

// connString is the keyword/value string pgx parses: no password, no TLS.
func connString(c Config) string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s sslmode=disable",
		quoteValue(c.Host), c.Port, quoteValue(c.Database), quoteValue(c.User))
}

// quoteValue quotes a keyword/value connection string value.
func quoteValue(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// scrubConnString keeps a parse error from repeating more than the keys: the
// string carries no secret, but nothing about the address is worth echoing.
func scrubConnString(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 {
		return "invalid connection settings" + msg[i:]
	}
	return "invalid connection settings"
}

// tlsConfig is the TLS configuration sslmode asks for, or nil for disable.
func tlsConfig(c Config) (*tls.Config, error) {
	if c.SSLMode == SSLModeDisable {
		return nil, nil //nolint:nilnil // nil means no TLS, which is what disable asks for
	}
	base, err := apigwtls.Build(c.tlsMaterial())
	if err != nil {
		//nolint:wrapcheck // already in this kind's voice
		return nil, err
	}
	if base == nil {
		base = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	switch c.SSLMode {
	case SSLModeRequire:
		// libpq's require encrypts and does not authenticate the server.
		base.InsecureSkipVerify = true // #nosec G402 -- sslmode=require is the operator's explicit choice: encrypt, authenticate nothing
	case SSLModeVerifyCA:
		base.InsecureSkipVerify = true // #nosec G402 -- verify-ca: VerifyConnection checks the chain, not the host name
		base.VerifyConnection = verifyChain(base.RootCAs)
	default:
		base.ServerName = c.Host
	}
	return base, nil
}

// verifyChain verifies the server's certificate chain against roots (the
// system pool when nil) without checking the host name, which is what
// verify-ca means. It is a VerifyConnection hook rather than a
// VerifyPeerCertificate one because VerifyConnection runs on every handshake,
// a resumed session's included, which VerifyPeerCertificate does not.
func verifyChain(roots *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		certs := cs.PeerCertificates
		if len(certs) == 0 {
			return errors.New("postgres: the server presented no certificate")
		}
		inter := x509.NewCertPool()
		for _, cert := range certs[1:] {
			inter.AddCert(cert)
		}
		if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter}); err != nil {
			return fmt.Errorf("postgres: verifying the server certificate: %w", err)
		}
		return nil
	}
}
