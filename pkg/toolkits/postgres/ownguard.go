package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
)

// ErrPlatformDatabase refuses a connection that reached the platform's own
// database. A caller with postgres_execute there could rewrite personas,
// grants and the audit log.
var ErrPlatformDatabase = errors.New("postgres: this connection reaches the platform's own database, which is never a connection target")

// ownQuery asks the connected database whether the advisory lock the platform
// just took on its own database is held in it. pg_locks covers the whole
// server and is readable by every role, and its database column is the OID of
// the database the lock was taken in, so the answer is true exactly when this
// connection's database is the platform's, whatever host name, address,
// port or pooler led here. A single-key advisory lock appears with the key's
// high 32 bits in classid, its low 32 bits in objid, and objsubid 1.
const ownQuery = `SELECT EXISTS (
  SELECT 1 FROM pg_catalog.pg_locks l
  JOIN pg_catalog.pg_database d ON d.oid = l.database
  WHERE l.locktype = 'advisory' AND l.granted
    AND d.datname = current_database()
    AND l.classid = $1::bigint::oid AND l.objid = $2::bigint::oid AND l.objsubid = 1)`

// OwnDatabaseGuard is the ConnectGuard that refuses a connection to the
// platform's own database, platform. It takes a random advisory lock on the
// platform's database for the length of one question, asks the connection
// whether it can see that lock in its own database, and releases it.
//
// Comparing host, port and database name is not enough: a DNS alias, an
// address instead of a name, or a connection pooler in front of the same
// server all name the platform's database differently. The lock is the
// database's own answer.
//
// The guard runs on every physical connection a pool opens, so a connection
// saved while the platform's database was unreachable, or whose host name is
// later pointed at it, is refused at its first statement rather than trusted
// from its save. A failure to take the lock or to ask is a refusal: the guard
// cannot say the connection is safe.
func OwnDatabaseGuard(platform *sql.DB) ConnectGuard {
	if platform == nil {
		return nil
	}
	return func(ctx context.Context, conn *pgx.Conn) error {
		key, err := randomKey()
		if err != nil {
			return err
		}
		release, ok, err := advisorylock.Try(ctx, platform, advisorylock.ID(key.id))
		if err != nil {
			return fmt.Errorf("postgres: checking the connection is not the platform's database: %w", err)
		}
		if !ok {
			return errors.New("postgres: checking the connection is not the platform's database: the check's lock was taken")
		}
		defer release()
		var same bool
		if err := conn.QueryRow(ctx, ownQuery, key.high, key.low).Scan(&same); err != nil {
			return fmt.Errorf("postgres: checking the connection is not the platform's database: %w", err)
		}
		if same {
			return ErrPlatformDatabase
		}
		return nil
	}
}

// lockKey is one advisory lock key: the bigint the lock is taken with, and
// its high and low 32 bits as pg_locks shows them (classid, objid).
type lockKey struct {
	id, high, low int64
}

// halfBits is the width of each half of a single-key advisory lock.
const halfBits = 32

// randomKey is an advisory lock key no other holder uses, drawn as two 32-bit
// halves so each is the unsigned value pg_locks reports.
func randomKey() (lockKey, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return lockKey{}, fmt.Errorf("postgres: drawing a lock key: %w", err)
	}
	high, low := int64(binary.BigEndian.Uint32(b[:4])), int64(binary.BigEndian.Uint32(b[4:]))
	return lockKey{id: high<<halfBits | low, high: high, low: low}, nil
}
