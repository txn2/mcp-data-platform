package gates

// scripts/state-readers-check.py lists the API contract changes in a diff and
// warns when nothing under ui/src changed with them (#1709). #1676 changed no
// json tag; it changed the refresh route's description in swagger.json, and
// the Schema card that reads the route was left behind (#1689). What the check
// must never do is stay quiet on either shape.

import (
	"strings"
	"testing"
)

const readersScriptRelPath = "scripts/state-readers-check.py"

const (
	adminResp = "package admin\n\n// toolResp is what the tool route answers with.\ntype toolResp struct {\n\tName string `json:\"name\"`\n}\n"
	swagger   = `{
  "paths": {"/admin/tools": {"get": {"description": "Lists tools.", "responses": {}}}},
  "definitions": {"admin.toolResp": {"type": "object", "properties": {"name": {"type": "string"}}}}
}
`
)

// withField returns adminResp with body in place of the struct's one field.
func withField(body string) string {
	return strings.Replace(adminResp, "\tName string `json:\"name\"`\n", body, 1)
}

func TestStateReadersCheck(t *testing.T) {
	t.Parallel()
	const warning = "WARNING state-readers-check"
	cases := []struct {
		name  string
		files map[string]string
		env   []string
		expect
	}{
		{
			name:  "an added field with no ui change warns and names the field and its definition",
			files: map[string]string{"pkg/admin/resp.go": withField("\tName string `json:\"name\"`\n\tHint bool `json:\"hint,omitempty\"`\n")},
			expect: expect{pass: true, want: []string{
				warning,
				`pkg/admin: toolResp.Hint added (json:"hint,omitempty"); swagger definition admin.toolResp`,
				"Readers of this state",
			}},
		},
		{
			name:   "a renamed tag warns",
			files:  map[string]string{"pkg/admin/resp.go": withField("\tName string `json:\"title\"`\n")},
			expect: expect{pass: true, want: []string{warning, `pkg/admin: toolResp.Name tag json:"name" -> json:"title"`}},
		},
		{
			name: "a route description change in swagger.json warns, the shape #1676 had",
			files: map[string]string{"internal/apidocs/swagger.json": strings.Replace(swagger,
				"Lists tools.", "Lists tools, and reports a refused read beside them.", 1)},
			expect: expect{pass: true, want: []string{warning, "internal/apidocs/swagger.json: GET /admin/tools changed: description"}},
		},
		{
			name:   "a swagger definition that gains a property names the property",
			files:  map[string]string{"internal/apidocs/swagger.json": strings.Replace(swagger, `"name": {"type": "string"}`, `"name": {"type": "string"}, "hint": {"type": "boolean"}`, 1)},
			expect: expect{pass: true, want: []string{warning, "definition admin.toolResp changed: hint added"}},
		},
		{
			name: "a contract change with a ui/src change lists the change without warning",
			files: map[string]string{
				"pkg/admin/resp.go":        withField("\tName string `json:\"title\"`\n"),
				"ui/src/pages/ToolRow.tsx": "export {};\n",
			},
			expect: expect{pass: true, want: []string{"1 API contract change(s) against main, and 1 file(s) under ui/src/ changed with them", "toolResp.Name"}, mustNot: []string{warning}},
		},
		{
			name: "a field moved to another file of the same package is not a change",
			files: map[string]string{
				"pkg/admin/resp.go":  "package admin\n",
				"pkg/admin/tools.go": adminResp,
			},
			expect: expect{pass: true, want: []string{"no API contract field changed"}, mustNot: []string{warning}},
		},
		{
			name: "a comment, a test file and a struct outside the contract directories are not changes",
			files: map[string]string{
				"pkg/admin/resp.go":          strings.Replace(adminResp, "is what the tool route answers with", "answers the tool route", 1),
				"pkg/admin/resp_test.go":     "package admin\n\ntype fixture struct {\n\tX int `json:\"x\"`\n}\n",
				"pkg/toolkits/trino/resp.go": "package trino\n\ntype out struct {\n\tX int `json:\"x\"`\n}\n",
			},
			expect: expect{pass: true, want: []string{"no API contract field changed"}, mustNot: []string{warning}},
		},
		{
			name:   "a base branch that cannot be resolved fails rather than skipping",
			files:  map[string]string{"pkg/admin/resp.go": withField("\tName string `json:\"title\"`\n")},
			env:    []string{"BASE_BRANCH=no-such-branch"},
			expect: expect{want: []string{"FAIL state-readers-check: cannot resolve base branch 'no-such-branch'"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newGitRepo(t, readersScriptRelPath, map[string]string{
				"pkg/admin/resp.go":             adminResp,
				"internal/apidocs/swagger.json": swagger,
			})
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := runScript(t, root, []string{readersScriptRelPath}, tc.env)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}
