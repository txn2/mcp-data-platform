package namesort

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompare(t *testing.T) {
	names := []string{"conn-10", "Beta", "conn-2", "alpha", "Conn-11", "conn-02", "zeta", "Alpha", "10x", "2x", "a", "", "conn-", "café"}
	slices.SortFunc(names, Compare)
	assert.Equal(t, []string{"", "2x", "10x", "a", "Alpha", "alpha", "Beta", "café", "conn-", "conn-2", "conn-02", "conn-10", "Conn-11", "zeta"}, names)
}

func TestCompare_IsTotalAndAntisymmetric(t *testing.T) {
	names := []string{"a1", "A1", "a01", "a1b", "a", "b", "9", "09", "x99y", "x100y"}
	for _, a := range names {
		assert.Equal(t, 0, Compare(a, a), a)
		for _, b := range names {
			if a != b {
				assert.NotEqual(t, 0, Compare(a, b), "%q vs %q", a, b)
				assert.Equal(t, -Compare(a, b), Compare(b, a), "%q vs %q", a, b)
			}
		}
	}
}
