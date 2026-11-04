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
//
// kind is pg_type.typtype (b base, c composite, d domain, e enum, r range, m
// multirange), and the structured kinds carry what their values are made of
// (#2455): a range its subtype, a multirange its range type, a composite its
// attributes in order. Each of those is resolved too, so a value is mapped by
// the types it holds.
type typeInfo struct {
	oid     uint32
	name    string
	display string
	array   bool
	kind    string
	elem    *typeInfo
	attrs   []attrInfo
}

// attrInfo is one attribute of a composite type.
type attrInfo struct {
	name string
	typ  *typeInfo
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

// typeQuery reads the types the cache does not hold yet, with the type each
// structured kind is made of: a range's subtype and a multirange's range.
const typeQuery = `SELECT t.oid, t.typname, format_type(t.oid, NULL), t.typcategory = 'A', t.typtype::text,
COALESCE(r.rngsubtype, m.rngtypid, 0::oid)
FROM pg_catalog.pg_type t
LEFT JOIN pg_catalog.pg_range r ON r.rngtypid = t.oid
LEFT JOIN pg_catalog.pg_range m ON m.rngmultitypid = t.oid
WHERE t.oid = ANY($1::oid[])`

// attrQuery reads the attributes of composite types, in order.
const attrQuery = `SELECT t.oid, a.attname, a.atttypid
FROM pg_catalog.pg_type t JOIN pg_catalog.pg_attribute a ON a.attrelid = t.typrelid
WHERE t.oid = ANY($1::oid[]) AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY t.oid, a.attnum`

// maxTypeDepth bounds how deep a structured type's parts are resolved: a
// composite holding a range of a composite is three.
const maxTypeDepth = 4

// querier is the part of a pgx connection or transaction the cache reads with.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// resolve returns the typeInfo of every oid, reading the unknown ones (and the
// types their structured kinds are made of) on q. An OID pg_type does not hold
// is named by its number.
func (c *typeCache) resolve(ctx context.Context, q querier, oids []uint32) (map[uint32]typeInfo, error) {
	out := make(map[uint32]typeInfo, len(oids))
	missing := c.known(oids, out)
	if len(missing) == 0 {
		return out, nil
	}
	read, err := readTypes(ctx, q, missing)
	if err != nil {
		return nil, err
	}
	c.store(read)
	for _, oid := range missing {
		out[oid] = read[oid]
	}
	return out, nil
}

// readTypes reads oids and, level by level, the parts of the structured ones,
// and links each to its parts.
func readTypes(ctx context.Context, q querier, oids []uint32) (map[uint32]typeInfo, error) {
	raw := map[uint32]rawType{}
	next := oids
	for depth := 0; len(next) > 0 && depth <= maxTypeDepth; depth++ {
		level, err := readLevel(ctx, q, next)
		if err != nil {
			return nil, err
		}
		next = nil
		for oid, rt := range level {
			raw[oid] = rt
			for _, part := range rt.parts() {
				if _, seen := raw[part]; !seen {
					next = append(next, part)
				}
			}
		}
	}
	out := make(map[uint32]typeInfo, len(oids))
	for _, oid := range oids {
		out[oid] = link(raw, oid, 0)
	}
	return out, nil
}

// rawType is one pg_type row before its parts are linked.
type rawType struct {
	ti    typeInfo
	elem  uint32
	attrs []rawAttr
}

type rawAttr struct {
	name string
	oid  uint32
}

func (r rawType) parts() []uint32 {
	var out []uint32
	if r.elem != 0 {
		out = append(out, r.elem)
	}
	for _, a := range r.attrs {
		out = append(out, a.oid)
	}
	return out
}

// link builds oid's typeInfo with its parts, an OID never read named by its
// number.
func link(raw map[uint32]rawType, oid uint32, depth int) typeInfo {
	rt, ok := raw[oid]
	if !ok {
		return typeInfo{oid: oid, name: fmt.Sprint(oid), display: fmt.Sprint(oid)}
	}
	ti := rt.ti
	if depth >= maxTypeDepth {
		return ti
	}
	if rt.elem != 0 {
		e := link(raw, rt.elem, depth+1)
		ti.elem = &e
	}
	for _, a := range rt.attrs {
		at := link(raw, a.oid, depth+1)
		ti.attrs = append(ti.attrs, attrInfo{name: a.name, typ: &at})
	}
	return ti
}

// readLevel reads oids from pg_type, and the attributes of the composites
// among them.
func readLevel(ctx context.Context, q querier, oids []uint32) (map[uint32]rawType, error) {
	rows, err := q.Query(ctx, typeQuery, oidArray(oids))
	if err != nil {
		return nil, fmt.Errorf("reading result types: %w", err)
	}
	out := make(map[uint32]rawType, len(oids))
	var composites []uint32
	for rows.Next() {
		var rt rawType
		if err := rows.Scan(&rt.ti.oid, &rt.ti.name, &rt.ti.display, &rt.ti.array, &rt.ti.kind, &rt.elem); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading result types: %w", err)
		}
		if rt.ti.kind == kindComposite {
			composites = append(composites, rt.ti.oid)
		}
		out[rt.ti.oid] = rt
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading result types: %w", err)
	}
	if len(composites) == 0 {
		return out, nil
	}
	return out, readAttrs(ctx, q, composites, out)
}

// readAttrs fills in the attributes of composites.
func readAttrs(ctx context.Context, q querier, composites []uint32, into map[uint32]rawType) error {
	rows, err := q.Query(ctx, attrQuery, oidArray(composites))
	if err != nil {
		return fmt.Errorf("reading composite attributes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			oid uint32
			a   rawAttr
		)
		if err := rows.Scan(&oid, &a.name, &a.oid); err != nil {
			return fmt.Errorf("reading composite attributes: %w", err)
		}
		rt := into[oid]
		rt.attrs = append(rt.attrs, a)
		into[oid] = rt
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading composite attributes: %w", err)
	}
	return nil
}

// The pg_type.typtype values the mapping reads.
const (
	kindBase       = "b"
	kindComposite  = "c"
	kindRange      = "r"
	kindMultirange = "m"
)

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
