package flowdoc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

func TestDocSentence_NotADocstring(t *testing.T) {
	assert.Equal(t, "", DocSentence(&syntax.ExprStmt{X: &syntax.Ident{Name: "x"}}, 120))
	assert.Equal(t, "", DocSentence(&syntax.ReturnStmt{}, 120))
	assert.Equal(t, "", DocSentence(&syntax.ExprStmt{X: &syntax.Literal{Value: int64(3)}}, 120))
	assert.Equal(t, "Pulls the orders.", DocSentence(&syntax.ExprStmt{X: &syntax.Literal{Value: "Pulls the orders. Then more."}}, 120))
	assert.Equal(t, "Pulls…", DocSentence(&syntax.ExprStmt{X: &syntax.Literal{Value: "Pulls the orders."}}, 6))
}

func TestCaption(t *testing.T) {
	f, err := (&syntax.FileOptions{Set: true}).Parse("m", `
# Pulls the day's orders. More detail here.
def above():
    pass

def top():
    # Sends the summary.
    pass

def doc():
    """Builds the report. Then more."""
    pass

# ----
def ruled():
    pass

def empty(): pass
`, syntax.RetainComments)
	require.NoError(t, err)
	got := map[string]string{}
	for _, s := range f.Stmts {
		d, ok := s.(*syntax.DefStmt)
		require.True(t, ok)
		got[d.Name.Name] = Caption(d, 120)
	}
	assert.Equal(t, "Pulls the day's orders.", got["above"])
	assert.Equal(t, "Sends the summary.", got["top"])
	assert.Equal(t, "Builds the report.", got["doc"])
	assert.Equal(t, "", got["ruled"])
	assert.Equal(t, "", got["empty"])
	assert.Equal(t, "", Caption(&syntax.DefStmt{Name: &syntax.Ident{Name: "x"}}, 120))
}
