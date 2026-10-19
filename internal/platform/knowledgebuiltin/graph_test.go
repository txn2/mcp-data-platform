package knowledgebuiltin

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// citedSlugs is the built-in pages a body cites as the reference scan reads
// it: a citation inside a code span is not one, because the scan and the
// renderer both skip code (#2200).
func citedSlugs(body string) []string {
	var out []string
	for _, ref := range knowledgepage.ScanBodyRefs(body) {
		if ref.TargetType == knowledgepage.RefTargetKnowledgePage {
			out = append(out, ref.RefPageID)
		}
	}
	return out
}

// TestPages_FormAGraph is #2200's gate. On a fresh install none of the
// built-in pages referenced another: their citations sat in code spans, which
// the scan skips. Each page now cites, and is cited by, at least one page
// other than the index, every slug cited is shipped, and the index cites
// every page.
func TestPages_FormAGraph(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)

	shipped := map[string]bool{}
	for _, p := range pages {
		shipped[p.Slug] = true
	}
	outbound := map[string]int{}
	inbound := map[string]int{}
	for _, p := range pages {
		cited := citedSlugs(p.Body)
		for _, slug := range cited {
			assert.Truef(t, shipped[slug], "%s cites %q, which this release does not ship", p.Slug, slug)
			if p.Slug == indexSlug || slug == indexSlug || slug == p.Slug {
				continue
			}
			outbound[p.Slug]++
			inbound[slug]++
		}
		if p.Slug == indexSlug {
			for other := range shipped {
				if other != indexSlug {
					assert.Containsf(t, cited, other, "the index does not cite %s", other)
				}
			}
			continue
		}
		assert.Containsf(t, cited, indexSlug, "%s does not cite the index", p.Slug)
	}
	for slug := range shipped {
		if slug == indexSlug {
			continue
		}
		assert.Positivef(t, outbound[slug], "%s cites no other built-in page", slug)
		assert.Positivef(t, inbound[slug], "no other built-in page cites %s", slug)
	}
}

func TestPageMetas_RelatedNameOtherShippedPages(t *testing.T) {
	shipped := map[string]bool{}
	for _, m := range pageMetas {
		shipped[m.slug] = true
	}
	for _, m := range pageMetas {
		for _, slug := range m.related {
			assert.Truef(t, shipped[slug], "%s names %q as related, which this release does not ship", m.slug, slug)
			assert.NotEqualf(t, m.slug, slug, "%s names itself as related", m.slug)
			assert.NotEqualf(t, indexSlug, slug, "%s names the index, which every page cites already", m.slug)
		}
	}
}

// coversEntries reads covers.txt into page file -> covered paths.
func coversEntries(t *testing.T) map[string][]string {
	t.Helper()
	f, err := os.Open("covers.txt")
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck // read-only test fixture

	entries := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		_, dup := entries[fields[0]]
		require.Falsef(t, dup, "covers.txt lists %s twice", fields[0])
		entries[fields[0]] = fields[1:]
	}
	require.NoError(t, sc.Err())
	return entries
}

// covers.txt is what make builtin-pages-check reads to say a page may need
// another look. A page with no entry is never flagged, and a path that no
// longer exists flags nothing, so both fail here. The index is generated from
// pageMetas and describes no source of its own.
func TestCovers_EveryPageNamesPathsThatExist(t *testing.T) {
	entries := coversEntries(t)
	files := map[string]bool{}
	for _, m := range pageMetas {
		files[m.file] = true
		if m.slug == indexSlug {
			assert.NotContainsf(t, entries, m.file, "the index is generated and covers no source")
			continue
		}
		assert.NotEmptyf(t, entries[m.file], "%s lists no source it describes in covers.txt", m.file)
	}
	root := filepath.Join("..", "..", "..")
	for file, paths := range entries {
		assert.Truef(t, files[file], "covers.txt names %s, which is not a shipped page", file)
		for _, p := range paths {
			info, err := os.Stat(filepath.Join(root, p))
			if !assert.NoErrorf(t, err, "covers.txt: %s names %s, which does not exist", file, p) {
				continue
			}
			assert.Equalf(t, strings.HasSuffix(p, "/"), info.IsDir(),
				"covers.txt: %s names %s; a directory ends in / and a file does not", file, p)
		}
	}
}
