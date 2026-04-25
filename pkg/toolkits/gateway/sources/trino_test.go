package sources

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSQLLiteral_RejectsHostileValues covers the security/correctness
// guards on the literal renderer: NUL bytes (Trino-undefined behavior),
// NaN/Inf (parse errors at runtime), and unsupported types.
func TestSQLLiteral_RejectsHostileValues(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"nul-byte", "abc\x00def", "NUL"},
		{"nan", math.NaN(), "NaN"},
		{"pos-inf", math.Inf(1), "+Inf"},
		{"neg-inf", math.Inf(-1), "+Inf or -Inf"}, // error mentions both
		{"unsupported-slice", []string{"a"}, "unsupported binding type"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sqlLiteral(tt.v)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSQLLiteral_AcceptsValidValues(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "NULL"},
		{"hi", "'hi'"},
		{"o'reilly", "'o''reilly'"}, // single-quote doubling
		{true, "TRUE"},
		{false, "FALSE"},
		{int(42), "42"},
		{int64(-7), "-7"},
		{float64(3.14), "3.14"},
		{float32(-2.5), "-2.5"},
	}
	for _, tt := range cases {
		got, err := sqlLiteral(tt.v)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestTrinoSource_NameAndOperations(t *testing.T) {
	s := NewTrinoSource(nil)
	assert.Equal(t, "trino", s.Name())
	assert.Equal(t, []string{"query"}, s.Operations())
}

func TestTrinoSource_ExecuteRejectsUnknownOp(t *testing.T) {
	s := NewTrinoSource(nil)
	_, err := s.Execute(context.Background(), "drop_table", nil)
	assert.ErrorContains(t, err, "not supported")
}

func TestTrinoSource_ExecuteRequiresConnection(t *testing.T) {
	s := NewTrinoSource(nil)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"sql_template": "SELECT 1",
	})
	assert.ErrorContains(t, err, "connection")
}

func TestTrinoSource_ExecuteRequiresSQLTemplate(t *testing.T) {
	s := NewTrinoSource(nil)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection": "prod",
	})
	assert.ErrorContains(t, err, "sql_template")
}

func TestTrinoSource_ExecuteRendersBindings(t *testing.T) {
	var seenSQL string
	exec := func(_ context.Context, _ string, sql string) ([]map[string]any, error) {
		seenSQL = sql
		return []map[string]any{{"x": 1}}, nil
	}
	s := NewTrinoSource(exec)
	rows, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT * FROM customers WHERE email = :email AND active = :active",
		"email":        "x@x.com",
		"active":       true,
	})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"x": 1}}, rows)
	assert.Contains(t, seenSQL, "email = 'x@x.com'")
	assert.Contains(t, seenSQL, "active = TRUE")
}

func TestTrinoSource_EscapesSingleQuotes(t *testing.T) {
	var seenSQL string
	exec := func(_ context.Context, _ string, sql string) ([]map[string]any, error) {
		seenSQL = sql
		return nil, nil
	}
	s := NewTrinoSource(exec)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT * FROM t WHERE name = :name",
		"name":         "O'Hara",
	})
	require.NoError(t, err)
	// Single quote doubled per ANSI SQL.
	assert.Contains(t, seenSQL, "'O''Hara'")
}

func TestTrinoSource_RendersTypes(t *testing.T) {
	var seenSQL string
	exec := func(_ context.Context, _ string, sql string) ([]map[string]any, error) {
		seenSQL = sql
		return nil, nil
	}
	s := NewTrinoSource(exec)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT :i, :i64, :f, :null, :tval, :fval",
		"i":            int(7),
		"i64":          int64(8),
		"f":            float64(3.14),
		"null":         nil,
		"tval":         true,
		"fval":         false,
	})
	require.NoError(t, err)
	for _, want := range []string{"7", "8", "3.14", "NULL", "TRUE", "FALSE"} {
		assert.Contains(t, seenSQL, want)
	}
}

func TestTrinoSource_RejectsUnsupportedBindingType(t *testing.T) {
	s := NewTrinoSource(func(context.Context, string, string) ([]map[string]any, error) {
		return nil, nil
	})
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT :ch",
		"ch":           []string{"a", "b"},
	})
	assert.ErrorContains(t, err, "binding")
	assert.ErrorContains(t, err, "unsupported")
}

func TestTrinoSource_DetectsUnresolvedPlaceholder(t *testing.T) {
	s := NewTrinoSource(func(context.Context, string, string) ([]map[string]any, error) {
		return nil, nil
	})
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT :missing",
	})
	assert.ErrorContains(t, err, "unresolved placeholder")
}

func TestTrinoSource_AllowsColonInTimestampLiteral(t *testing.T) {
	// A timestamp literal contains : but it's followed by a digit, not an
	// identifier character, so it shouldn't trip the unresolved-placeholder
	// detector.
	exec := func(context.Context, string, string) ([]map[string]any, error) { return nil, nil }
	s := NewTrinoSource(exec)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT TIMESTAMP '2024-01-01 00:00:00'",
	})
	assert.NoError(t, err)
}

func TestTrinoSource_PropagatesExecError(t *testing.T) {
	exec := func(context.Context, string, string) ([]map[string]any, error) {
		return nil, errors.New("connection refused")
	}
	s := NewTrinoSource(exec)
	_, err := s.Execute(context.Background(), "query", map[string]any{
		"connection":   "prod",
		"sql_template": "SELECT 1",
	})
	assert.ErrorContains(t, err, "connection refused")
	assert.True(t, strings.HasPrefix(err.Error(), "trino:"))
}
