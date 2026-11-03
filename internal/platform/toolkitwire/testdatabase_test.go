package toolkitwire

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTestDatabaseNeedsTheToolkit(t *testing.T) {
	_, _, err := testDatabase(context.Background(), nil, "shop", nil)
	assert.ErrorIs(t, err, errNoAppDBToolkit)
}
