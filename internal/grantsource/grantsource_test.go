package grantsource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fixed []string

func (f fixed) WritableFolders(context.Context, Grantee) ([]string, error) { return f, nil }

func TestInstallAndUnderSQL(t *testing.T) {
	assert.Nil(t, Installed())
	Install(fixed{"reports"})
	got, err := Installed().WritableFolders(context.Background(), Grantee{})
	assert.NoError(t, err)
	assert.Equal(t, []string{"reports"}, got)
	Install(nil)
	assert.Nil(t, Installed())
	assert.Equal(t, "(p = f OR left(p, length(f) + 1) = f || '/')", UnderSQL("p", "f"))
}
