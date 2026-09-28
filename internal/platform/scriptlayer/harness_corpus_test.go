package scriptlayer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptfmt"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// docBlocks returns the fenced Python blocks of a markdown document that are
// whole scripts. A block fenced ` + "```python fragment" + ` shows one call out of
// context and is not a whole script, so it is not held to the gates.
func docBlocks(markdown string) []string {
	var blocks []string
	cur := make([]string, 0, 64)
	in := false
	for line := range strings.SplitSeq(markdown, "\n") {
		switch {
		case !in && line == "```python":
			in, cur = true, cur[:0]
		case in && line == "```":
			in = false
			blocks = append(blocks, strings.Join(cur, "\n")+"\n")
		case in:
			cur = append(cur, line)
		}
	}
	return blocks
}

// corpus is every script the platform shows an author: the built-in examples
// and the whole scripts in docs/scripts/.
func corpus(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, ex := range scriptexamples.All {
		out[ex.Name] = ex.Source
	}
	paths, err := filepath.Glob("../../../docs/scripts/*.md")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // a fixed documentation glob
		require.NoError(t, err)
		for i, block := range docBlocks(string(raw)) {
			out[fmt.Sprintf("%s#%d", filepath.Base(p), i+1)] = block
		}
	}
	require.Greater(t, len(out), len(scriptexamples.All), "the documentation's whole scripts are in the corpus")
	return out
}

// TestScriptCorpusPassesTheGates is #1937 and #1938's corpus check: every
// script the platform shows an author is already in the canonical format,
// passes every lint rule as a script created today, and formats to a tree the
// dialect parses the same way, stable on a second pass.
func TestScriptCorpusPassesTheGates(t *testing.T) {
	for name, src := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			require.True(t, scriptrun.Validate(src).OK, "%+v", scriptrun.Validate(src).Findings)
			res := scriptlint.Check(src, scriptlint.Save{})
			assert.Empty(t, res.Findings, "%+v", res.Findings)
			assert.Equal(t, src, res.Source, "the published script is already in the canonical format")
			assertFormatKeepsTheProgram(t, src)
		})
	}
}

// TestFormatKeepsTheProgram runs the formatter over source it has to change and
// checks what it made: the output parses under the dialect, has the syntax
// tree the input had (positions, comments and quote style aside), keeps every
// comment, and a second pass changes nothing.
func TestFormatKeepsTheProgram(t *testing.T) {
	src := "# lead\nX=[1,2,\n  3]  # trailing\ndef main( ):\n  '''Doc.'''\n  rows=platform.query('SELECT 1',connection='a',params={'a':1,'b':[1,2]})\n  if rows and (X or not rows): print(len(rows))\n  platform.result({'n':len(rows)})\n"
	out := scriptfmt.Format(src)
	assert.NotEqual(t, src, out)
	assert.Contains(t, out, `connection = "a"`)
	assert.Contains(t, out, "# trailing")
	assertFormatKeepsTheProgram(t, src)

	assert.Equal(t, "def main(:\n", scriptfmt.Format("def main(:\n"), "source the printer cannot read is kept as sent")
}

func assertFormatKeepsTheProgram(t *testing.T, src string) {
	t.Helper()
	out := scriptfmt.Format(src)
	before, err := scriptdialect.Options.Parse("s", src, syntax.RetainComments)
	require.NoError(t, err)
	after, err := scriptdialect.Options.Parse("s", out, syntax.RetainComments)
	require.NoError(t, err, "the formatted source parses under the dialect")
	assert.Equal(t, shape(before), shape(after), "the formatted source is the same program")
	assert.Equal(t, comments(before), comments(after), "every comment is kept")
	assert.Equal(t, out, scriptfmt.Format(out), "formatting twice changes nothing")
}

// shape renders a syntax tree without positions, comments or quote style: the
// node kinds, names, operators and literal values in walk order.
func shape(f *syntax.File) string {
	var b strings.Builder
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Ident:
			fmt.Fprintf(&b, "id:%s ", n.Name)
		case *syntax.Literal:
			fmt.Fprintf(&b, "lit:%v ", n.Value)
		case *syntax.BinaryExpr:
			fmt.Fprintf(&b, "bin:%s ", n.Op)
		case *syntax.UnaryExpr:
			fmt.Fprintf(&b, "un:%s ", n.Op)
		case *syntax.AssignStmt:
			fmt.Fprintf(&b, "assign:%s ", n.Op)
		case *syntax.ParenExpr:
			// Parentheses the printer adds or drops do not change the program.
		default:
			fmt.Fprintf(&b, "%T ", n)
		}
		return true
	})
	return b.String()
}

// comments is the text of every comment in a file, in order.
func comments(f *syntax.File) []string {
	out := []string{}
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			return false
		}
		if cm := n.Comments(); cm != nil {
			for _, group := range [][]syntax.Comment{cm.Before, cm.Suffix, cm.After} {
				for _, c := range group {
					out = append(out, c.Text)
				}
			}
		}
		return true
	})
	return out
}
