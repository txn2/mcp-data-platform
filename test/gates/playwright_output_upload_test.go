package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryPlaywrightOutputIsUploaded pins #2694: each Playwright config the
// acceptance suite runs writes its screenshots and error context to a folder
// the acceptance job uploads, so a browser failure in CI can be read from the
// run (#2691 could not).
func TestEveryPlaywrightOutputIsUploaded(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/acceptance-run.yml")
	for _, want := range []string{"build/*-results/", "ui/test-results/"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("acceptance-run.yml does not upload %s", want)
		}
	}
	configs, err := filepath.Glob(filepath.Join("..", "..", "ui", "e2e", "*", "playwright.config.ts"))
	require(t, err)
	if len(configs) == 0 {
		t.Fatal("found no Playwright configs under ui/e2e")
	}
	named := regexp.MustCompile(`outputDir:\s*path\.join\(__dirname,\s*"([^"]+)"\)`)
	for _, c := range configs {
		raw, err := os.ReadFile(c) // #nosec G304 -- a config this repository holds
		require(t, err)
		m := named.FindStringSubmatch(string(raw))
		if m == nil {
			continue // Playwright's default, ui/test-results, or a folder built in code
		}
		dir := filepath.Base(m[1])
		if !strings.HasPrefix(m[1], "../../../build/") || !strings.HasSuffix(dir, "-results") {
			t.Errorf("%s writes its output to %s, which the acceptance job does not upload: use build/<suite>-results", c, m[1])
		}
	}
}
