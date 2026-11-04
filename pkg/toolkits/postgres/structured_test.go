package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	tInt4   = typeInfo{oid: pgtype.Int4OID, name: "int4", display: "integer", kind: kindBase}
	tText   = typeInfo{oid: pgtype.TextOID, name: "text", display: "text", kind: kindBase}
	tTstz   = typeInfo{oid: pgtype.TimestamptzOID, name: "timestamptz", display: "timestamp with time zone", kind: kindBase}
	tInt4R  = typeInfo{oid: 3904, name: "int4range", display: "int4range", kind: kindRange, elem: &tInt4}
	tTstzR  = typeInfo{oid: 3910, name: "tstzrange", display: "tstzrange", kind: kindRange, elem: &tTstz}
	tInt4MR = typeInfo{oid: 4451, name: "int4multirange", display: "int4multirange", kind: kindMultirange, elem: &tInt4R}
	tAddr   = typeInfo{oid: 90001, name: "addr", display: "addr", kind: kindComposite, attrs: []attrInfo{
		{name: "street", typ: &tText}, {name: "zip", typ: &tInt4}, {name: "span", typ: &tInt4R},
	}}
	tIv     = typeInfo{oid: pgtype.IntervalOID, name: "interval", display: "interval", kind: kindBase}
	tHstore = typeInfo{oid: 90002, name: "hstore", display: "hstore", kind: kindBase}
	tVector = typeInfo{oid: 90003, name: "vector", display: "vector", kind: kindBase}
	tEnumV  = typeInfo{oid: 90004, name: "vector", display: "vector", kind: "e"}
)

func TestStructuredValue(t *testing.T) {
	cases := []struct {
		ti   typeInfo
		text string
		want any
	}{
		{tInt4R, "[1,5)", map[string]any{"lower": int64(1), "upper": int64(5), "lower_inc": true, "upper_inc": false, "empty": false}},
		{tInt4R, "empty", map[string]any{"lower": nil, "upper": nil, "lower_inc": false, "upper_inc": false, "empty": true}},
		{tTstzR, `["2026-11-03 10:00:00+00",)`, map[string]any{"lower": "2026-11-03T10:00:00Z", "upper": nil, "lower_inc": true, "upper_inc": false, "empty": false}},
		{tInt4MR, "{[1,3),[7,10)}", []any{
			map[string]any{"lower": int64(1), "upper": int64(3), "lower_inc": true, "upper_inc": false, "empty": false},
			map[string]any{"lower": int64(7), "upper": int64(10), "lower_inc": true, "upper_inc": false, "empty": false},
		}},
		{tAddr, `("1 Main",90210,"[1,2)")`, map[string]any{
			"street": "1 Main", "zip": int64(90210),
			"span": map[string]any{"lower": int64(1), "upper": int64(2), "lower_inc": true, "upper_inc": false, "empty": false},
		}},
		{tAddr, `(,,)`, map[string]any{"street": nil, "zip": nil, "span": nil}},
		{tIv, "1 year 2 mons 3 days 00:00:04.000005", map[string]any{"months": int64(14), "days": int64(3), "microseconds": int64(4000005), "text": "1 year 2 mons 3 days 00:00:04.000005"}},
		{tHstore, `"k"=>"v", "n"=>NULL`, map[string]any{"k": "v", "n": nil}},
		{tVector, "[1.5,2,-3]", []any{1.5, float64(2), float64(-3)}},
	}
	for _, c := range cases {
		require.True(t, structuredKind(&c.ti), c.ti.name)
		got, err := structuredValue(&c.ti, c.text)
		require.NoError(t, err, c.text)
		assert.Equal(t, c.want, got, c.text)
	}
	assert.False(t, structuredKind(&tEnumV), "an enum named vector is not pgvector's")
	assert.False(t, structuredKind(&tText))
	for _, c := range []struct {
		ti   typeInfo
		text string
	}{{tInt4R, "nope"}, {tInt4MR, "nope"}, {tAddr, "nope"}, {tIv, "nope"}, {tHstore, `"k"="v"`}, {tVector, "nope"}} {
		_, err := structuredValue(&c.ti, c.text)
		assert.Error(t, err, c.ti.name)
	}
	// A part that does not parse by its type is its text, never an error.
	assert.Equal(t, "x", partValue(new("x"), &tInt4))
	assert.Equal(t, "oops", partValue(new("oops"), &tInt4R))
	assert.Equal(t, "raw", partValue(new("raw"), nil))
	assert.Equal(t, "u", partValue(new("u"), &typeInfo{oid: 999999, name: "int4"}))
}

func TestEncodeStructuredParams(t *testing.T) {
	cases := []struct {
		ti   typeInfo
		in   any
		want string
	}{
		{tInt4R, map[string]any{"lower": 1, "upper": 5}, `["1","5")`},
		{tInt4R, map[string]any{"lower": nil, "upper": 5, "upper_inc": true, "lower_inc": false}, `(,"5"]`},
		{tInt4R, map[string]any{"empty": true}, "empty"},
		{tInt4R, "[1,2)", "[1,2)"},
		{tInt4MR, []any{map[string]any{"lower": 1, "upper": 3}}, `{["1","3")}`},
		{tAddr, map[string]any{"street": `a "b"`, "span": map[string]any{"lower": 1, "upper": 2}}, `("a \"b\"",,"[\"1\",\"2\")")`},
		{tIv, map[string]any{"months": 1, "microseconds": 5}, "1 months 0 days 5 microseconds"},
		{tIv, map[string]any{"text": "2 hours"}, "2 hours"},
		{tHstore, map[string]any{"b": "2", "a": nil}, `"a"=>NULL, "b"=>"2"`},
		{tVector, []any{1, 2.5}, "[1,2.5]"},
	}
	for _, c := range cases {
		got, err := encodeArg(c.in, c.ti)
		require.NoError(t, err, c.ti.name)
		assert.Equal(t, c.want, got, c.ti.name)
	}
	refusals := []struct {
		ti typeInfo
		in any
	}{
		{tInt4R, 5},
		{tInt4R, map[string]any{"from": 1}},
		{tInt4R, map[string]any{"upper": []any{func() {}}}},
		{tInt4MR, 5},
		{tInt4MR, []any{5}},
		{tAddr, 5},
		{tAddr, map[string]any{"nope": 1}},
		{tAddr, map[string]any{"span": 5}},
		{tIv, 5},
		{tIv, map[string]any{"days": func() {}}},
		{tHstore, 5},
		{tHstore, map[string]any{"k": 1}},
	}
	for _, r := range refusals {
		_, err := encodeArg(r.in, r.ti)
		assert.Error(t, err, "%s %v", r.ti.name, r.in)
	}
	_, err := encodeArg(map[string]any{}, tVector)
	assert.Error(t, err, "a vector takes a list")
	text, err := partText(5, nil)
	require.NoError(t, err)
	assert.Equal(t, "5", *text)
	_, err = partText(func() {}, nil)
	assert.Error(t, err)
}

// typeQuerier answers the type and attribute queries from fixed rows.
type typeQuerier struct {
	types   map[uint32][]any
	attrs   [][]any
	err     error
	attrErr error
}

func (q *typeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if q.err != nil {
		return nil, q.err
	}
	if sql == attrQuery {
		if q.attrErr != nil {
			return nil, q.attrErr
		}
		return &fakeRows{rows: q.attrs, tag: "SELECT"}, nil
	}
	var rows [][]any
	for _, r := range q.types {
		rows = append(rows, r)
	}
	return &fakeRows{rows: rows, tag: "SELECT"}, nil
}

func TestResolveLinksStructuredParts(t *testing.T) {
	q := &typeQuerier{
		types: map[uint32][]any{
			90001: {uint32(90001), "addr", "addr", false, "c", uint32(0)},
			3904:  {uint32(3904), "int4range", "int4range", false, "r", uint32(23)},
			23:    {uint32(23), "int4", "integer", false, "b", uint32(0)},
			25:    {uint32(25), "text", "text", false, "b", uint32(0)},
		},
		attrs: [][]any{{uint32(90001), "street", uint32(25)}, {uint32(90001), "span", uint32(3904)}},
	}
	got, err := newTypeCache().resolve(context.Background(), q, []uint32{90001})
	require.NoError(t, err)
	addr := got[90001]
	require.Len(t, addr.attrs, 2)
	assert.Equal(t, "street", addr.attrs[0].name)
	assert.Equal(t, "text", addr.attrs[0].typ.name)
	require.NotNil(t, addr.attrs[1].typ.elem)
	assert.Equal(t, "int4", addr.attrs[1].typ.elem.name)

	unknown, err := newTypeCache().resolve(context.Background(), &typeQuerier{types: map[uint32][]any{}}, []uint32{777})
	require.NoError(t, err)
	assert.Equal(t, "777", unknown[777].name)

	_, err = newTypeCache().resolve(context.Background(), &typeQuerier{err: assert.AnError}, []uint32{1})
	assert.Error(t, err)
	q.attrErr = assert.AnError
	_, err = newTypeCache().resolve(context.Background(), q, []uint32{90001})
	assert.ErrorContains(t, err, "composite attributes")
}
