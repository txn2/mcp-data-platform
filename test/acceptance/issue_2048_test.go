//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// #2048, #2049, #2050: a managed script formats with Python's printf flags and
// format specs, hashes, and matches regular expressions, through
// manage_script run_draft on the running platform.
//
// Wire forms: manage_script's command, name and source are strings, and
// validate's source a string; each is sent once in the one form its schema
// admits.

// draftLog2048 runs source as a draft and returns its log.
func draftLog2048(t *testing.T, c *client, source string) string {
	t.Helper()
	name := "acceptance-2048-" + unique1579()
	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	log, _ := ran["log"].(string)
	return log
}

const format2048 = `
def main():
    """Prints the forms #2048 names."""
    print("%02X" % 10)
    print("%5.2f|" % 3.14159)
    print("%-4s|" % "a")
    print("%%%02X" % 32)
    print("{:02X}".format(10))
    print("{:>10}|".format("a"))
    print("{:.2f}".format(3.14159))
    print("{:,}".format(1234567))
`

func TestIssue2048_PercentAndFormatTakePythonsFlagsWidthsAndPrecisions(t *testing.T) {
	c := connect(t)
	log := draftLog2048(t, c, format2048)
	want := "0A\n 3.14|\na   |\n%20\n0A\n         a|\n3.14\n1,234,567\n"
	if log != want {
		t.Errorf("the draft printed %q, want %q", log, want)
	}
}

func TestIssue2048_ValidateRefusesALiteralFormatThatWouldStillFail(t *testing.T) {
	c := connect(t)
	out := c.call("manage_script", map[string]any{
		"command": "validate",
		"source":  "def main():\n    \"\"\"Prints.\"\"\"\n    print(\"%y\" % 1)\n",
	})
	if out["ok"] != false || !strings.Contains(stringOf(out["findings"]), "invalid-format-string") {
		t.Errorf("validate did not refuse the format: %v", out)
	}
	help := c.call("manage_script", map[string]any{"command": "help"})
	if text := stringOf(help); !strings.Contains(text, `"%02X" % 10 is "0A"`) {
		t.Errorf("help does not document the accepted forms")
	}
}

// stringOf renders any decoded JSON value as text for a containment check.
func stringOf(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			b.WriteString(v)
			b.WriteByte('\n')
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			for k, x := range v {
				b.WriteString(k)
				b.WriteByte('\n')
				walk(x)
			}
		}
	}
	walk(v)
	return b.String()
}
