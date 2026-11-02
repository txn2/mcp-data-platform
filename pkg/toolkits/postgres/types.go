package postgres

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

// typeInfo is what the toolkit knows of one PostgreSQL type: the internal name
// the value mapping keys on ("int4", "_int4", "timestamptz"), the name a caller
// reads ("integer", "integer[]", "timestamp with time zone"), and whether it is
// an array.
type typeInfo struct {
	name    string
	display string
	array   bool
}

// typeCache resolves type OIDs to typeInfo from pg_type, once per OID per
// connection. A type's OID does not change while it exists; a type dropped and
// created again is a new OID.
type typeCache struct {
	mu    sync.RWMutex
	types map[uint32]typeInfo
}

func newTypeCache() *typeCache {
	return &typeCache{types: make(map[uint32]typeInfo)}
}

// typeQuery reads the types the cache does not hold yet.
const typeQuery = `SELECT t.oid, t.typname, format_type(t.oid, NULL), t.typcategory = 'A'
FROM pg_catalog.pg_type t WHERE t.oid = ANY($1::oid[])`

// querier is the part of a pgx connection or transaction the cache reads with.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// resolve returns the typeInfo of every oid, reading the unknown ones in one
// statement on q. An OID pg_type does not hold is named by its number.
func (c *typeCache) resolve(ctx context.Context, q querier, oids []uint32) (map[uint32]typeInfo, error) {
	out := make(map[uint32]typeInfo, len(oids))
	missing := c.known(oids, out)
	if len(missing) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, typeQuery, oidArray(missing))
	if err != nil {
		return nil, fmt.Errorf("reading result types: %w", err)
	}
	defer rows.Close()
	read := make(map[uint32]typeInfo, len(missing))
	for rows.Next() {
		var (
			oid uint32
			ti  typeInfo
		)
		if err := rows.Scan(&oid, &ti.name, &ti.display, &ti.array); err != nil {
			return nil, fmt.Errorf("reading result types: %w", err)
		}
		read[oid] = ti
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading result types: %w", err)
	}
	c.store(read)
	for _, oid := range missing {
		ti, ok := read[oid]
		if !ok {
			ti = typeInfo{name: fmt.Sprint(oid), display: fmt.Sprint(oid)}
		}
		out[oid] = ti
	}
	return out, nil
}

// oidArray writes oids as an array literal, sent as text for the server to
// read as oid[].
func oidArray(oids []uint32) string {
	parts := make([]string, len(oids))
	for i, oid := range oids {
		parts[i] = strconv.FormatUint(uint64(oid), decimal)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// known copies the cached oids into out and returns the rest, deduplicated.
func (c *typeCache) known(oids []uint32, out map[uint32]typeInfo) []uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var missing []uint32
	seen := make(map[uint32]bool, len(oids))
	for _, oid := range oids {
		if ti, ok := c.types[oid]; ok {
			out[oid] = ti
			continue
		}
		if !seen[oid] {
			seen[oid] = true
			missing = append(missing, oid)
		}
	}
	return missing
}

func (c *typeCache) store(read map[uint32]typeInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	maps.Copy(c.types, read)
}
