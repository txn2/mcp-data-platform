package gates

// scripts/builtin-pages-check.py warns when a diff changes the source a
// built-in knowledge page describes and leaves the page alone (#2200). Every
// deployment serves those pages as the platform's own word on its next start,
// and nothing compiles their prose.

import "testing"

const builtinPagesScriptRelPath = "scripts/builtin-pages-check.py"

const pagesCovers = "# comment\n\nmaps.md internal/maps/ internal/httpserver/maphttp/serve.go\n"

func TestBuiltinPagesCheck(t *testing.T) {
	t.Parallel()
	const warning = "WARNING builtin-pages-check"
	cases := []struct {
		name  string
		files map[string]string
		env   []string
		expect
	}{
		{
			name:  "a change under a covered directory with the page untouched warns and names both",
			files: map[string]string{"internal/maps/fetch.go": "package maps\n\n// changed\n"},
			pass:  true, want: []string{warning, "internal/platform/knowledgebuiltin/pages/maps.md", "    internal/maps/fetch.go"},
		},
		{
			name:  "a covered file warns",
			files: map[string]string{"internal/httpserver/maphttp/serve.go": "package maphttp\n\n// changed\n"},
			pass:  true, want: []string{warning, "internal/httpserver/maphttp/serve.go"},
		},
		{
			name: "the page changing with its source is quiet",
			files: map[string]string{
				"internal/maps/fetch.go":                           "package maps\n\n// changed\n",
				"internal/platform/knowledgebuiltin/pages/maps.md": "# Maps\n\nupdated\n",
			},
			pass: true, want: []string{"OK builtin-pages-check"}, mustNot: []string{warning},
		},
		{
			name: "a test file, a sibling of a covered file and an uncovered path are not changes",
			files: map[string]string{
				"internal/maps/fetch_test.go":          "package maps\n",
				"internal/httpserver/maphttp/other.go": "package maphttp\n",
				"internal/mapsextra/x.go":              "package mapsextra\n",
			},
			pass: true, want: []string{"OK builtin-pages-check"}, mustNot: []string{warning},
		},
		{
			name:  "a base branch that cannot be resolved fails rather than skipping",
			files: map[string]string{"internal/maps/fetch.go": "package maps\n\n// changed\n"},
			env:   []string{"BASE_BRANCH=no-such-branch"},
			want:  []string{"FAIL builtin-pages-check: cannot resolve base branch 'no-such-branch'"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newGitRepo(t, builtinPagesScriptRelPath, map[string]string{
				"internal/platform/knowledgebuiltin/covers.txt":    pagesCovers,
				"internal/platform/knowledgebuiltin/pages/maps.md": "# Maps\n",
				"internal/maps/fetch.go":                           "package maps\n",
				"internal/httpserver/maphttp/serve.go":             "package maphttp\n",
			})
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := runScript(t, root, []string{builtinPagesScriptRelPath}, tc.env)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}
