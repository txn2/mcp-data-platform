package scriptrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// binding is one name/value pair a test binds into a params dict.
type binding struct {
	name  string
	value any
}

// params builds a Starlark params dict, in the order given, so a test can
// express a binding the way a script would.
func params(t *testing.T, bindings ...binding) *starlark.Dict {
	t.Helper()
	d := starlark.NewDict(len(bindings))
	for _, b := range bindings {
		v, err := toStarlark(b.value)
		require.NoError(t, err)
		require.NoError(t, d.SetKey(starlark.String(b.name), v))
	}
	return d
}

// bind is shorthand for one binding.
func bind(name string, value any) binding { return binding{name: name, value: value} }

func TestBindSQL_Literals(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		args []binding
		want string
	}{
		{"string quoted", "WHERE r = :r", []binding{bind("r", "west")}, "WHERE r = 'west'"},
		{"quote doubled", "WHERE r = :r", []binding{bind("r", "o'brien")}, "WHERE r = 'o''brien'"},
		{"injection is a value", "WHERE r = :r", []binding{bind("r", "x' OR '1'='1")}, "WHERE r = 'x'' OR ''1''=''1'"},
		{"int", "LIMIT :n", []binding{bind("n", int64(5))}, "LIMIT 5"},
		{"float", "WHERE f > :f", []binding{bind("f", 1.5)}, "WHERE f > 1.5"},
		{"bool", "WHERE b = :b", []binding{bind("b", true)}, "WHERE b = TRUE"},
		{"false", "WHERE b = :b", []binding{bind("b", false)}, "WHERE b = FALSE"},
		{"none", "WHERE x IS :x", []binding{bind("x", nil)}, "WHERE x IS NULL"},
		{"list", "WHERE r IN :rs", []binding{bind("rs", []any{"a", "b"})}, "WHERE r IN ('a', 'b')"},
		{"list of ints", "WHERE n IN :ns", []binding{bind("ns", []any{int64(1), int64(2)})}, "WHERE n IN (1, 2)"},
		{"repeated placeholder", "WHERE a = :v OR b = :v", []binding{bind("v", "x")}, "WHERE a = 'x' OR b = 'x'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bindSQL(tc.sql, params(t, tc.args...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestBindSQL_SkipsNonCode is the correctness half: text that merely looks like
// a placeholder must survive untouched, or binding would silently rewrite a
// literal or a comment.
func TestBindSQL_SkipsNonCode(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		args []binding
		want string
	}{
		{"string literal", "SELECT ':r' , :r", []binding{bind("r", "x")}, "SELECT ':r' , 'x'"},
		{"escaped quote in literal", "SELECT 'it''s :r', :r", []binding{bind("r", "x")}, "SELECT 'it''s :r', 'x'"},
		{"quoted identifier", `SELECT ":r", :r`, []binding{bind("r", "x")}, `SELECT ":r", 'x'`},
		{"line comment", "-- :r\nSELECT :r", []binding{bind("r", "x")}, "-- :r\nSELECT 'x'"},
		{"block comment", "/* :r */ SELECT :r", []binding{bind("r", "x")}, "/* :r */ SELECT 'x'"},
		{"cast is not a placeholder", "SELECT x::varchar, :r", []binding{bind("r", "x")}, "SELECT x::varchar, 'x'"},
		{"bare colon", "SELECT 1 : 2", nil, "SELECT 1 : 2"},
		{"unterminated literal swallows the rest", "SELECT ':r", nil, "SELECT ':r"},
		{"unterminated comment swallows the rest", "SELECT /* :r", nil, "SELECT /* :r"},
		{"unterminated line comment", "SELECT 1 -- :r", nil, "SELECT 1 -- :r"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bindSQL(tc.sql, params(t, tc.args...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBindSQL_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		args    []binding
		wantErr string
	}{
		{"unbound placeholder", "WHERE r = :r", nil, "has no bound value"},
		{"unused value", "SELECT 1", []binding{bind("r", "x")}, "no :r placeholder"},
		{"empty list", "WHERE r IN :rs", []binding{bind("rs", []any{})}, "empty list"},
		{"nested list", "WHERE r IN :rs", []binding{bind("rs", []any{[]any{"a"}})}, "may not contain lists"},
		{"nul byte", "WHERE r = :r", []binding{bind("r", "a\x00b")}, "NUL byte"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bindSQL(tc.sql, params(t, tc.args...))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("oversize list", func(t *testing.T) {
		big := make([]any, maxBindListLen+1)
		for i := range big {
			big[i] = int64(i)
		}
		_, err := bindSQL("WHERE n IN :ns", params(t, bind("ns", big)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bind limit")
	})

	t.Run("non-string key", func(t *testing.T) {
		d := starlark.NewDict(1)
		require.NoError(t, d.SetKey(starlark.MakeInt(1), starlark.String("x")))
		_, err := bindSQL("SELECT 1", d)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keys must be strings")
	})

	t.Run("unbindable value", func(t *testing.T) {
		d := starlark.NewDict(1)
		require.NoError(t, d.SetKey(starlark.String("t"), starlark.Tuple{starlark.String("a")}))
		_, err := bindSQL("WHERE x = :t", d)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot leave the script")
	})
}

func TestBindSQL_NilParams(t *testing.T) {
	got, err := bindSQL("SELECT 1", nil)
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1", got)
}
