//go:build integration

package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// structuredDDL is one table with a column of each structured type #2455 maps,
// on a server with hstore, ltree, btree_gist and pgvector available.
const structuredDDL = `CREATE EXTENSION IF NOT EXISTS hstore;
CREATE EXTENSION IF NOT EXISTS ltree;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TYPE mood AS ENUM ('calm', 'busy');
CREATE TYPE addr AS (street text, zip int, tags text[]);
CREATE TABLE kinds (
  r int4range, ts tstzrange, mr int4multirange, iv interval, ip inet, net cidr,
  m mood, a addr, h hstore, v vector(3), l ltree, tsv tsvector
);
CREATE TABLE bookings (room int, during tstzrange, EXCLUDE USING gist (room WITH =, during WITH &&))`

func TestStructuredValuesRoundTrip_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": rt.target})
		for stmt := range strings.SplitSeq(structuredDDL, ";") {
			execOK(t, tk, Statement{Connection: "target", SQL: stmt})
		}
		// Every value is bound in the shape the result returns it in.
		execOK(t, tk, Statement{
			Connection: "target", SQL: `INSERT INTO kinds VALUES
			($1, $2, $3, $4, '10.1.2.3/24', '10.1.0.0/16', $5, $6, $7, $8, 'top.mid.leaf', 'a fat cat')`,
			Params: []any{
				map[string]any{"lower": 1, "upper": 5},
				map[string]any{"lower": "2026-11-03T10:00:00Z", "upper": nil, "lower_inc": true},
				[]any{map[string]any{"lower": 1, "upper": 3}, map[string]any{"lower": 7, "upper": 9, "upper_inc": true}},
				map[string]any{"months": 14, "days": 3, "microseconds": 4000005},
				"busy",
				map[string]any{"street": `1 "Main", St`, "zip": 90210, "tags": []any{"a", "b"}},
				map[string]any{"k": "v", "empty": nil},
				[]any{1.5, 2, -3},
			},
		})

		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT * FROM kinds`})
		row := map[string]any{}
		types := map[string]string{}
		for i, c := range got.Columns {
			row[c.Name] = got.Rows[0][i]
			types[c.Name] = c.Type
		}
		want := map[string]any{
			"r":  map[string]any{"lower": int64(1), "upper": int64(5), "lower_inc": true, "upper_inc": false, "empty": false},
			"ts": map[string]any{"lower": "2026-11-03T10:00:00Z", "upper": nil, "lower_inc": true, "upper_inc": false, "empty": false},
			"mr": []any{
				map[string]any{"lower": int64(1), "upper": int64(3), "lower_inc": true, "upper_inc": false, "empty": false},
				map[string]any{"lower": int64(7), "upper": int64(10), "lower_inc": true, "upper_inc": false, "empty": false},
			},
			"iv":  map[string]any{"months": int64(14), "days": int64(3), "microseconds": int64(4000005), "text": "1 year 2 mons 3 days 00:00:04.000005"},
			"ip":  "10.1.2.3/24",
			"net": "10.1.0.0/16",
			"m":   "busy",
			"a":   map[string]any{"street": `1 "Main", St`, "zip": int64(90210), "tags": "{a,b}"},
			"h":   map[string]any{"k": "v", "empty": nil},
			"v":   []any{1.5, float64(2), float64(-3)},
			"l":   "top.mid.leaf",
			"tsv": "'a' 'cat' 'fat'",
		}
		for col, w := range want {
			if !reflect.DeepEqual(row[col], w) {
				t.Errorf("%s = %#v, want %#v", col, row[col], w)
			}
		}
		// The column's type names the PostgreSQL type, so an enum is told
		// from text.
		for col, typ := range map[string]string{"m": "mood", "a": "addr", "ip": "inet", "net": "cidr", "v": "vector", "l": "ltree", "tsv": "tsvector", "h": "hstore"} {
			if types[col] != typ {
				t.Errorf("type of %s = %q, want %q", col, types[col], typ)
			}
		}

		// A booking bound as a range object; an overlapping one is refused
		// by the exclusion constraint.
		ctx := context.Background()
		booking := Statement{
			Connection: "target", SQL: `INSERT INTO bookings VALUES (1, $1)`,
			Params: []any{map[string]any{"lower": "2026-11-03T10:00:00Z", "upper": "2026-11-03T11:00:00Z"}},
		}
		if _, err := tk.Execute(ctx, booking); err != nil {
			t.Fatal(err)
		}
		booking.Params = []any{map[string]any{"lower": "2026-11-03T10:30:00Z", "upper": "2026-11-03T12:00:00Z"}}
		_, err := tk.Execute(ctx, booking)
		var failed *FailedError
		if !errors.As(err, &failed) || failed.Statement == nil || failed.Statement.SQLState != "23P01" {
			t.Fatalf("an overlapping booking: err = %v, want SQLSTATE 23P01", err)
		}

		// A range parameter with a key the shape does not have is refused
		// before the statement runs.
		booking.Params = []any{map[string]any{"from": "2026-11-04T10:00:00Z"}}
		var refused *RefusedError
		if _, err := tk.Execute(ctx, booking); !errors.As(err, &refused) {
			t.Fatalf("a range with an unknown key: err = %v, want a refusal", err)
		}
	})
}

// A type named vector that is not pgvector's is read as its text. An enum keeps
// its own type in a result, where a domain is reported as its base type.
func TestVectorWithoutTheExtensionIsText_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": rt.target})
		execOK(t, tk, Statement{Connection: "target", SQL: `CREATE TYPE vector AS ENUM ('[1,2,3]')`})
		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT '[1,2,3]'::vector AS v`})
		if got.Rows[0][0] != "[1,2,3]" || got.Columns[0].Type != "vector" {
			t.Fatalf("v = %#v (%s), want the text", got.Rows[0][0], got.Columns[0].Type)
		}
	})
}
