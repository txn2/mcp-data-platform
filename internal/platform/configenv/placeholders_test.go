package configenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CheckNoPlaceholders walks slices as well as maps, so one entry of a list is
// named rather than the list.
func TestCheckNoPlaceholders_NamesTheOffendingPath(t *testing.T) {
	err := CheckNoPlaceholders(map[string]any{
		"hosts": []any{"a.example.com", "${FALLBACK_HOST}"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hosts[1]")
}

// A config with nothing to report returns no error, including one holding
// values that merely contain a dollar sign.
func TestCheckNoPlaceholders_Clean(t *testing.T) {
	require.NoError(t, CheckNoPlaceholders(map[string]any{
		"host": "trino.example.com", "password": "a$dollar$sign", "port": 443, "ssl": true,
	}))
}

// A value that merely contains a dollar sign is not a placeholder, and a
// password legitimately containing "${" would be refused — which is the one
// false positive this check can produce. It is preferred to the alternative:
// a stored placeholder is unusable, while such a password can be changed.
func TestCheckNoPlaceholders_DollarSignAlone(t *testing.T) {
	if err := CheckNoPlaceholders(map[string]any{"password": "pa$$word"}); err != nil {
		t.Errorf("a password with dollar signs was refused: %v", err)
	}
	if err := CheckNoPlaceholders(map[string]any{"password": "literal ${BRACE} inside"}); err == nil {
		t.Error("a value carrying ${...} was accepted")
	}
}
