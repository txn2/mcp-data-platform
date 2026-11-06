package gates

// scripts/acceptance-copy-check.py fails when an acceptance criterion asserts
// text the diff removed from production Go (#2519). #2498 changed the endpoint
// binding refusal "only an administrator can bind a script to an endpoint",
// and test/acceptance/issue_2171_test.go still asserted
// !strings.Contains(refusal, "only an administrator"); the E2E Nightly after
// the merge failed on it and refused the v1.153.2 tag. What the check must
// never do is fail on a fragment as common as the one a criterion written
// later reads, on text that moved, on a criterion asserting text is absent,
// or on a criterion the diff itself updates.

import (
	"strings"
	"testing"
)

const acceptanceCopyScriptRelPath = "scripts/acceptance-copy-check.py"

const (
	bindingSource = `package epbinding

import "errors"

// ErrNotAllowed refuses a binding by someone other than an administrator.
var ErrNotAllowed = errors.New("only an administrator can bind a script to an endpoint")
`
	ownerSource = `package scripthttp

import "net/http"

func refuse(w http.ResponseWriter) {
	http.Error(w, "only an administrator can change a script's owner", http.StatusForbidden)
}
`
	issue2171Criterion = `package acceptance

import (
	"strings"
	"testing"
)

func TestIssue2171_ANonAdministratorIsRefused(t *testing.T) {
	refusal := bind(t)
	if !strings.Contains(refusal, "only an administrator") {
		t.Fatalf("want refused, got %q", refusal)
	}
}
`
	absentCriterion = `package acceptance

import (
	"strings"
	"testing"
)

func TestIssue2428_NoPostgresTool(t *testing.T) {
	if raw := list(t); strings.Contains(raw, "postgres_query") {
		t.Errorf("a postgres tool is listed: %s", raw)
	}
}
`
	toolSource = `package tools

var names = []string{"postgres_query", "trino_query"}
`
)

func TestAcceptanceCopyCheck(t *testing.T) {
	t.Parallel()
	const fail = "FAIL acceptance-copy-check"
	// The criterion line rewritten in a commit of its own, still asserting
	// the fragment.
	rewrittenCriterion := strings.Replace(issue2171Criterion, `if !strings.Contains(`, `if refusal == "" || !strings.Contains(`, 1)
	nameRefusal := strings.Replace(bindingSource, `errors.New("only an administrator can bind a script to an endpoint")`,
		`errors.New("the script is not a form: an administrator binds an endpoint that is not a form")`, 1)
	cases := []struct {
		name string
		// later is committed on main after the base, before the change branch.
		later map[string]string
		files map[string]string
		env   []string
		exp   expect
	}{
		{
			name:  "a refusal rewritten while the criterion written with it still asserts it fails and names the line",
			files: map[string]string{"internal/endpoint/epbinding/epbinding.go": nameRefusal},
			exp: expect{want: []string{
				fail,
				`test/acceptance/issue_2171_test.go:10: "only an administrator" matches "only an administrator can bind a script to an endpoint", which internal/endpoint/epbinding/epbinding.go no longer has`,
			}},
		},
		{
			name:  "a fragment another refusal still says, read by a criterion written in another commit, is not reported",
			later: map[string]string{"test/acceptance/issue_2171_test.go": rewrittenCriterion},
			files: map[string]string{"internal/endpoint/epbinding/epbinding.go": nameRefusal},
			exp:   expect{pass: true, want: []string{"no acceptance criterion asserts it"}, mustNot: []string{fail}},
		},
		{
			name:  "a fragment no production string holds any longer fails whoever wrote the criterion",
			later: map[string]string{"test/acceptance/issue_2171_test.go": rewrittenCriterion},
			files: map[string]string{
				"internal/endpoint/epbinding/epbinding.go":      nameRefusal,
				"internal/httpserver/scripthttp/portalowner.go": strings.Replace(ownerSource, "only an administrator can change", "an administrator changes", 1),
			},
			exp: expect{want: []string{fail, "test/acceptance/issue_2171_test.go:10:"}},
		},
		{
			name: "text moved to another file is not removed",
			files: map[string]string{
				"internal/endpoint/epbinding/epbinding.go": nameRefusal,
				"internal/endpoint/epcmd/epcmd.go":         "package epcmd\n\nconst refusal = \"only an administrator can bind a script to an endpoint\"\n",
			},
			exp: expect{pass: true, mustNot: []string{fail}},
		},
		{
			name:  "a criterion asserting a text is absent is not broken by removing it",
			files: map[string]string{"pkg/tools/names.go": "package tools\n\nvar names = []string{\"trino_query\"}\n"},
			exp:   expect{pass: true, want: []string{"no acceptance criterion asserts it"}, mustNot: []string{fail}},
		},
		{
			name: "a criterion the diff updates is the author's update",
			files: map[string]string{
				"internal/endpoint/epbinding/epbinding.go": nameRefusal,
				"test/acceptance/issue_2171_test.go": strings.Replace(issue2171Criterion, `"only an administrator")`,
					`"only an administrator") && !strings.Contains(refusal, "is not a form")`, 1),
			},
			exp: expect{pass: true, mustNot: []string{fail}},
		},
		{
			name: "a generated file, an import, a comment and a test file are not production text",
			files: map[string]string{
				"internal/apidocs/docs.go":                       "// Package apidocs Code generated by swaggo/swag. DO NOT EDIT\npackage apidocs\n",
				"internal/httpserver/scripthttp/portalowner.go":  strings.Replace(ownerSource, `import "net/http"`, `import http "net/http"`, 1),
				"internal/endpoint/epbinding/epbinding_test.go":  "package epbinding\n",
				"internal/endpoint/epbinding/epbinding_extra.go": "package epbinding\n\n// only an administrator can bind a script to an endpoint\n",
			},
			exp: expect{pass: true, want: []string{"no text removed from production Go against main"}, mustNot: []string{fail}},
		},
		{
			name: "a base branch that cannot be resolved fails rather than skipping",
			env:  []string{"BASE_BRANCH=no-such-branch"},
			exp:  expect{want: []string{"FAIL acceptance-copy-check: cannot resolve base branch 'no-such-branch'"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newGitRepo(t, acceptanceCopyScriptRelPath, map[string]string{
				"internal/endpoint/epbinding/epbinding.go":      bindingSource,
				"internal/httpserver/scripthttp/portalowner.go": ownerSource,
				"internal/apidocs/docs.go":                      "// Package apidocs Code generated by swaggo/swag. DO NOT EDIT\npackage apidocs\n\nconst doc = `{\"only an administrator can bind a script to an endpoint\": 1}`\n",
				"pkg/tools/names.go":                            toolSource,
				"test/acceptance/issue_2171_test.go":            issue2171Criterion,
				"test/acceptance/issue_2428_test.go":            absentCriterion,
			})
			if tc.later != nil {
				runIn(t, root, "git", "checkout", "-q", "main")
				for rel, body := range tc.later {
					writeFile(t, root, rel, body)
				}
				runIn(t, root, "git", "add", "-A")
				runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", "later")
				runIn(t, root, "git", "checkout", "-q", "-B", "change")
			}
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := runScript(t, root, []string{acceptanceCopyScriptRelPath}, tc.env)
			assertOutput(t, out, passed, tc.exp)
		})
	}
}
