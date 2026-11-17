package site

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContract(t *testing.T) {
	c, err := ParseContract(`{"application": "store", "contract": ">=2  <4"}`)
	require.NoError(t, err)
	assert.Equal(t, &Contract{Application: "store", Range: ">=2 <4"}, c)
	assert.Contains(t, c.Render(), `"contract": ">=2 <4"`)
	none, err := ParseContract("  ")
	require.NoError(t, err)
	assert.Nil(t, none)
	for _, bad := range []string{
		`not json`, `{"application": ""}`, `{"application": "s", "contract": ">=x"}`,
		`{"application": "s", "contract": "-1"}`, `{"application": "s", "extra": 1}`,
	} {
		_, err := ParseContract(bad)
		assert.Error(t, err, bad)
	}
	_, err = ParseContract(`{"application": "` + string(make([]byte, 5000)) + `"}`)
	require.Error(t, err)
	assert.Empty(t, (*Contract)(nil).Render())
}

func TestContractAccepts(t *testing.T) {
	r := &Contract{Application: "store", Range: ">=2 <4"}
	for v, want := range map[int]bool{0: true, 1: false, 2: true, 3: true, 4: false} {
		assert.Equal(t, want, r.Accepts(v), "contract %d", v)
	}
	assert.True(t, (&Contract{Application: "s", Range: "3"}).Accepts(3))
	assert.False(t, (&Contract{Application: "s", Range: "=3"}).Accepts(2))
	assert.True(t, (&Contract{Application: "s", Range: "<=3 >1"}).Accepts(2))
	assert.True(t, (*Contract)(nil).Accepts(9), "no declaration is compatible with everything")
	assert.True(t, (&Contract{Application: "s"}).Accepts(9))
	assert.False(t, (&Contract{Application: "s", Range: "junk"}).Accepts(1))
	assert.True(t, r.For("STORE"))
	assert.False(t, r.For("other"))
	assert.False(t, (*Contract)(nil).For("store"))
}
