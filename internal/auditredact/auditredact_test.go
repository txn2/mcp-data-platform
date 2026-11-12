package auditredact

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type replacer string

func (r replacer) String(s string) string {
	return strings.ReplaceAll(s, string(r), "[REDACTED:cookie]")
}
func (r replacer) Empty() bool { return r == "" }

func TestWithAndFrom(t *testing.T) {
	assert.Nil(t, From(context.Background()))
	assert.Nil(t, From(With(context.Background(), replacer(""))), "an empty redactor is none")
	red := From(With(context.Background(), replacer("sess-1")))
	if assert.NotNil(t, red) {
		assert.Equal(t, "id [REDACTED:cookie]", red.String("id sess-1"))
	}
}
