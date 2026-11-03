package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Table is one relation of an application's schema as describe reports it
// (#2404): what the applied migrations produced, read from the target's
// pg_catalog.
type Table struct {
	Name string `json:"name"`
	// Kind is table, partitioned_table, view or materialized_view.
	Kind        string        `json:"kind"`
	Comment     string        `json:"comment,omitempty"`
	Columns     []TableColumn `json:"columns"`
	Constraints []Constraint  `json:"constraints"`
	Indexes     []TableIndex  `json:"indexes"`
}

// TableColumn is one column.
type TableColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// Constraint is a primary key, unique, foreign key, check or exclusion
// constraint, as PostgreSQL writes its definition.
type Constraint struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Definition string `json:"definition"`
}

// TableIndex is one index, as PostgreSQL writes its definition.
type TableIndex struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// relKinds names the relation kinds describe reports.
var relKinds = map[string]string{"r": "table", "p": "partitioned_table", "v": "view", "m": "materialized_view"}

// conKinds names the constraint kinds.
var conKinds = map[string]string{"p": "primary_key", "u": "unique", "f": "foreign_key", "c": "check", "x": "exclusion", "t": "trigger"}

const describeTablesSQL = `SELECT c.relname, c.relkind::text, COALESCE(obj_description(c.oid, 'pg_class'), '')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm') ORDER BY c.relname`

const describeColumnsSQL = `SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), COALESCE(col_description(c.oid, a.attnum), '')
FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm') AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`

const describeConstraintsSQL = `SELECT c.relname, con.conname, con.contype::text, pg_get_constraintdef(con.oid)
FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 ORDER BY c.relname, con.conname`

const describeIndexesSQL = `SELECT t.relname, i.relname, pg_get_indexdef(i.oid)
FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = $1 ORDER BY t.relname, i.relname`

// Describe reads schema's tables, columns, constraints and indexes from the
// target's catalog.
func (t *Toolkit) Describe(ctx context.Context, connection, schema string) ([]Table, error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return nil, err
	}
	tables, err := c.describe4(ctx, schema)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	return tables, nil
}

// describe4 runs the four catalog reads in one READ ONLY transaction, so they
// see one state of the schema.
func (c *conn) describe4(ctx context.Context, schema string) ([]Table, error) {
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, c.cfg.StatementTimeout, false, func(tx pgx.Tx) (any, error) {
		tables := []Table{}
		index := map[string]int{}
		err := scanAll(ctx, tx, describeTablesSQL, schema, func(rows pgx.Rows) error {
			var tb Table
			var kind string
			if err := rows.Scan(&tb.Name, &kind, &tb.Comment); err != nil {
				return err //nolint:wrapcheck // read by its type
			}
			tb.Kind = relKinds[kind]
			tb.Columns, tb.Constraints, tb.Indexes = []TableColumn{}, []Constraint{}, []TableIndex{}
			index[tb.Name] = len(tables)
			tables = append(tables, tb)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if err := describeParts(ctx, tx, schema, tables, index); err != nil {
			return nil, err
		}
		return tables, nil
	})
	if err != nil {
		return nil, err
	}
	tables, _ := out.([]Table)
	return tables, nil
}

// describeParts reads the columns, constraints and indexes into tables.
func describeParts(ctx context.Context, tx pgx.Tx, schema string, tables []Table, index map[string]int) error {
	err := scanAll(ctx, tx, describeColumnsSQL, schema, func(rows pgx.Rows) error {
		var table string
		var col TableColumn
		if err := rows.Scan(&table, &col.Name, &col.Type, &col.Nullable, &col.Default, &col.Comment); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		if i, ok := index[table]; ok {
			tables[i].Columns = append(tables[i].Columns, col)
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, tx, describeConstraintsSQL, schema, func(rows pgx.Rows) error {
		var table, kind string
		var con Constraint
		if err := rows.Scan(&table, &con.Name, &kind, &con.Definition); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		con.Kind = conKinds[kind]
		if i, ok := index[table]; ok {
			tables[i].Constraints = append(tables[i].Constraints, con)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return scanAll(ctx, tx, describeIndexesSQL, schema, func(rows pgx.Rows) error {
		var table string
		var idx TableIndex
		if err := rows.Scan(&table, &idx.Name, &idx.Definition); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		if i, ok := index[table]; ok {
			tables[i].Indexes = append(tables[i].Indexes, idx)
		}
		return nil
	})
}

// scanAll runs sql with one argument and hands each row to each.
func scanAll(ctx context.Context, tx pgx.Tx, sql string, arg any, each func(pgx.Rows) error) error {
	rows, err := tx.Query(ctx, sql, arg)
	if err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err() //nolint:wrapcheck // read by its type
}
