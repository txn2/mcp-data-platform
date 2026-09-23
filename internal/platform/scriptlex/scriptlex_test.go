package scriptlex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScan_EachCheck pins every check to the line it reports and its severity.
func TestScan_EachCheck(t *testing.T) {
	cases := map[string]struct {
		source   string
		message  string
		severity string
		line     int
	}{
		"import":       {"x = 1\nimport os\n", "`import` is not available", SeverityError, 2},
		"try":          {"try:\n    x = 1\n", "`try`/`except` does not exist", SeverityError, 1},
		"f-string":     {"s = \"\"\"\na\n\"\"\"\nx = f\"{s}\"\n", "f-strings are not supported", SeverityWarning, 4},
		"clock":        {"x = datetime.now()\n", "no clock", SeverityWarning, 1},
		"random":       {"x = random.choice([1])\n", "no randomness", SeverityWarning, 1},
		"filesystem":   {"x = open(p)\n", "no filesystem or network", SeverityWarning, 1},
		"aws key":      {"k = \"AKIAABCDEFGHIJKLMNOP\"\n", "AWS access key id", SeverityError, 1},
		"a credential": {"\npassword = \"hunter2222\"\n", "credential-shaped assignment", SeverityWarning, 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Scan(tc.source)
			require.Len(t, got, 1, "%+v", got)
			assert.Contains(t, got[0].Message, tc.message)
			assert.Equal(t, tc.severity, got[0].Severity)
			assert.Equal(t, tc.line, got[0].Line)
			assert.NotEmpty(t, got[0].Hint)
		})
	}
}

// TestScan_ReadsCodeNotStrings is #1853 at the package's own boundary: the
// author-facing checks never fire on string contents or comments.
func TestScan_ReadsCodeNotStrings(t *testing.T) {
	assert.Empty(t, Scan("q = {\"f\": r[\"f\"]}\nsql = \"SELECT datetime FROM t\" # open(x)\n"))
}

func TestLineOf_PastTheEndIsTheLastLine(t *testing.T) {
	assert.Equal(t, 2, lineOf("a\nb", 99))
	assert.Equal(t, 1, lineOf("a\nb", 0))
}
