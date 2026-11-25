package structure

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// literalAuditSource is an audit event or registration request that names a
// tool call's source as a literal rather than reading it from the call
// (#2819). A tool call arrives from an agent session (mcp) or a managed
// script's host (script), and only the call knows which. An HTTP handler's
// own surface (admin, portal) is fixed by its route and may be written so.
var literalAuditSource = regexp.MustCompile(`\bSource:\s*"(mcp|script)"`)

// TestAuditSourcesAreReadFromTheCall pins #2819: a tool that writes its own
// audit event takes the source from the call's context
// (mcpcontext.CallSource), never a literal. The table tool's adapter wrote
// "mcp" for every registration, so a managed script's registrations, 66,482 of
// one deployment's 91,671 agent rows, read as agent calls.
func TestAuditSourcesAreReadFromTheCall(t *testing.T) {
	var hits []string
	for _, dir := range []string{"../../pkg", "../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
				strings.HasSuffix(name, "_integration.go") {
				return nil
			}
			raw, err := os.ReadFile(path) //nolint:gosec // a path walked from this repository
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if literalAuditSource.MatchString(line) {
					hits = append(hits, path+":"+itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hits {
		t.Errorf("an audit source written as a literal; read it with mcpcontext.CallSource(ctx): %s", h)
	}
}

// itoa is strconv.Itoa without the import for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
