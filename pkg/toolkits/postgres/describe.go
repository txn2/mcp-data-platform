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
	// RowSecurity and ForceRowSecurity are the table's row-level security:
	// enabled, and applied to its owner as well (#2447).
	RowSecurity      bool        `json:"row_security"`
	ForceRowSecurity bool        `json:"force_row_security"`
	Policies         []Policy    `json:"policies"`
	Triggers         []Trigger   `json:"triggers"`
	PartitionKey     string      `json:"partition_key,omitempty"`
	Partitions       []Partition `json:"partitions"`
	// View is a view's or materialized view's definition.
	View *View `json:"view,omitempty"`
}

// Description is what describe reports of one schema: the server, the
// database's extensions, and every object the applied migrations produced.
// An empty list is [], never null.
type Description struct {
	Server              Server               `json:"server"`
	Extensions          []Extension          `json:"extensions"`
	AvailableExtensions []AvailableExtension `json:"available_extensions"`
	Tables              []Table              `json:"tables"`
	Functions           []Function           `json:"functions"`
	Enums               []Enum               `json:"enums"`
	Domains             []Domain             `json:"domains"`
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

const describeTablesSQL = `/* mcp-data-platform */ SELECT c.relname, c.relkind::text, COALESCE(obj_description(c.oid, 'pg_class'), ''),
c.relrowsecurity, c.relforcerowsecurity, CASE WHEN c.relkind = 'p' THEN pg_get_partkeydef(c.oid) ELSE '' END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm') ORDER BY c.relname`

const describeColumnsSQL = `/* mcp-data-platform */ SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), COALESCE(col_description(c.oid, a.attnum), '')
FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm') AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`

// describeConstraintsSQL leaves out the NOT NULL constraints PostgreSQL 18
// records in pg_constraint (contype n): a column's nullable says the same on
// every major.
const describeConstraintsSQL = `/* mcp-data-platform */ SELECT c.relname, con.conname, con.contype::text, pg_get_constraintdef(con.oid)
FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND con.contype <> 'n' ORDER BY c.relname, con.conname`

const describeIndexesSQL = `/* mcp-data-platform */ SELECT t.relname, i.relname, pg_get_indexdef(i.oid)
FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = $1 ORDER BY t.relname, i.relname`

// Describe reads what schema holds from the target's catalog: its tables
// and their parts, functions, enums and domains, with the server's version
// and the database's extensions.
func (t *Toolkit) Describe(ctx context.Context, connection, schema string) (*Description, error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return nil, err
	}
	d, err := c.describeAll(ctx, schema)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	return d, nil
}

// describeAll runs the catalog reads in one READ ONLY transaction, so they
// see one state of the schema.
func (c *conn) describeAll(ctx context.Context, schema string) (*Description, error) {
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, c.cfg.StatementTimeout, false, func(tx pgx.Tx) (any, error) {
		d := &Description{
			Extensions: []Extension{}, AvailableExtensions: []AvailableExtension{}, Tables: []Table{},
			Functions: []Function{}, Enums: []Enum{}, Domains: []Domain{},
		}
		if err := describeServer(ctx, tx, d); err != nil {
			return nil, err
		}
		index := map[string]int{}
		err := scanAll(ctx, tx, describeTablesSQL, schema, func(rows pgx.Rows) error {
			tb := Table{
				Columns: []TableColumn{}, Constraints: []Constraint{}, Indexes: []TableIndex{},
				Policies: []Policy{}, Triggers: []Trigger{}, Partitions: []Partition{},
			}
			var kind string
			if err := rows.Scan(&tb.Name, &kind, &tb.Comment, &tb.RowSecurity, &tb.ForceRowSecurity, &tb.PartitionKey); err != nil {
				return err //nolint:wrapcheck // read by its type
			}
			tb.Kind = relKinds[kind]
			index[tb.Name] = len(d.Tables)
			d.Tables = append(d.Tables, tb)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if err := describeParts(ctx, tx, schema, d.Tables, index); err != nil {
			return nil, err
		}
		if err := describeTableObjects(ctx, tx, schema, d, index); err != nil {
			return nil, err
		}
		if err := describeTypes(ctx, tx, schema, d); err != nil {
			return nil, err
		}
		return d, nil
	})
	if err != nil {
		return nil, err
	}
	d, _ := out.(*Description)
	return d, nil
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
	return scanRows(ctx, tx, sql, []any{arg}, each)
}

// scanRows runs sql with args and hands each row to each.
func scanRows(ctx context.Context, tx pgx.Tx, sql string, args []any, each func(pgx.Rows) error) error {
	rows, err := tx.Query(ctx, sql, args...)
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
