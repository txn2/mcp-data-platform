//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// #2050: a managed script matches with the re module, through manage_script
// run_draft on the running platform: the ticket's title extraction, and sub,
// findall and split over a 1 MB string inside the step limit.
//
// Wire forms: manage_script's command, name and source are strings, each
// sent once in the one form its schema admits.

const re2050 = `
def main():
    """Extracts a title, then works over a 1 MB page."""
    page = "<html><head><TITLE>Quarterly Report</TITLE></head><body>" + ("a1 b22 " * 150000) + "</body></html>"
    print(re.search("(?i)<title[^>]*>([^<]*)</title>", page).group(1))
    print(len(page) > 1000000)
    print(len(re.findall("[0-9]+", page)))
    print(len(re.split(" ", page)))
    print(re.sub("[0-9]+", "#", page)[56:68])
    print(re.compile("(?P<k>[a-z]+)=(?P<v>[0-9]+)").search("x=1").groupdict())
`

func TestIssue2050_ReModuleMatchesInAScriptOverAMegabyte(t *testing.T) {
	c := connect(t)
	log := draftLog2048(t, c, re2050)
	want := strings.Join([]string{
		"Quarterly Report",
		"True",
		"300000",
		"300002",
		"a# b# a# b# ",
		`{"k": "x", "v": "1"}`,
	}, "\n") + "\n"
	if log != want {
		t.Errorf("the draft printed %q, want %q", log, want)
	}
}

func TestIssue2050_ValidateRefusesAPatternRE2CannotCompile(t *testing.T) {
	c := connect(t)
	out := c.call("manage_script", map[string]any{
		"command": "validate",
		"source":  "def main():\n    \"\"\"Matches.\"\"\"\n    print(re.search(\"a(?=b)\", \"ab\"))\n",
	})
	if out["ok"] != false || !strings.Contains(stringOf(out["findings"]), "invalid-pattern") {
		t.Errorf("validate did not refuse the lookahead: %v", out)
	}
}
