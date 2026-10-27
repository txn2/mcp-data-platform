package assetrefs_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

type fakeForms map[string]assetrefs.Form

func (f fakeForms) Form(_ context.Context, endpoint string) (assetrefs.Form, error) {
	if endpoint == "broken" {
		return assetrefs.Form{}, errors.New("db down")
	}
	form, ok := f[endpoint]
	if !ok {
		return assetrefs.Form{}, assetrefs.ErrNoForm
	}
	return form, nil
}

func formDeclarer(store *fakeRefs) *assetrefs.Declarer {
	d := declarer(store)
	d.BindForms(fakeForms{
		"contact": {Form: true},
		"apply":   {Form: true, Authenticated: true, OwnerEmail: author},
		"intake":  {Form: true, Authenticated: true, OwnerEmail: "someone@example.com"},
		"lookup":  {},
	})
	return d
}

// #2177: an asset may declare several forms, each by mcp:form:<endpoint>.
func TestResolveDeclaresForms(t *testing.T) {
	d := formDeclarer(newFakeRefs())
	declared, err := d.Resolve(t.Context(), []string{"mcp:form:contact", "mcp:form:apply", logoURI}, analystAuthor(), "")
	require.NoError(t, err)
	require.Len(t, declared, 3)
	assert.Equal(t, assetrefs.Declared{Kind: assetrefs.TargetForm, TargetID: "contact", URI: "mcp:form:contact"}, declared[0])
	assert.Equal(t, assetrefs.TargetForm, declared[1].Kind)
}

func TestResolveFormRefusals(t *testing.T) {
	d := formDeclarer(newFakeRefs())
	for uri, want := range map[string]string{
		"mcp:form:missing": "no form answers",
		"mcp:form:lookup":  "no form answers",
		"mcp:form:intake":  "only its owner or an administrator",
		"mcp:form:":        "or a form by mcp:form:<endpoint>",
	} {
		_, err := d.Resolve(t.Context(), []string{uri}, analystAuthor(), "")
		require.ErrorIs(t, err, assetrefs.ErrRefused, uri)
		assert.Contains(t, err.Error(), want, uri)
	}
	_, err := d.Resolve(t.Context(), []string{"mcp:form:broken"}, analystAuthor(), "")
	require.Error(t, err)
	assert.NotErrorIs(t, err, assetrefs.ErrRefused, "a store fault is not the author's")

	admin := assetrefs.Author{Claims: resource.BuildClaims("u9", "root@example.com", "", nil, true)}
	_, err = d.Resolve(t.Context(), []string{"mcp:form:intake"}, admin, "")
	require.NoError(t, err, "an administrator embeds any form")

	unbound := declarer(newFakeRefs())
	_, err = unbound.Resolve(t.Context(), []string{"mcp:form:contact"}, analystAuthor(), "")
	require.ErrorContains(t, err, "no endpoints")
	var nilDeclarer *assetrefs.Declarer
	nilDeclarer.BindForms(fakeForms{})
}

// #2177, #2331: a form reference is served as the form's page, handed what
// the reader's copy carries, and the asset's other references are unchanged.
func TestRewriteForServesAFormsPage(t *testing.T) {
	refs := []assetrefs.Ref{
		{TargetKind: assetrefs.TargetForm, TargetID: "contact", URI: "mcp:form:contact", RefToken: "t1"},
		{TargetKind: assetrefs.TargetForm, TargetID: "apply", URI: "mcp:form:apply", RefToken: "t2"},
		{TargetKind: assetrefs.TargetResource, TargetID: "res-logo", URI: logoURI, RefToken: "t3"},
	}
	html := `<iframe src="mcp:form:contact"></iframe><iframe src="mcp:form:apply"></iframe><img src="` + logoURI + `">` +
		`<a href="mcp:form:contact" download>x</a>`
	query := func(endpoint string) url.Values {
		if endpoint == "apply" {
			return url.Values{"form_viewer": {"tok/1+"}}
		}
		return nil
	}
	out := string(assetrefs.FormQuery(query).Rewrite([]byte(html), "text/html", "https://data.example.com", "a1", refs))
	assert.Contains(t, out, `src="https://data.example.com/forms/contact"`)
	assert.Contains(t, out, `src="https://data.example.com/forms/apply?form_viewer=`+url.QueryEscape("tok/1+")+`"`)
	assert.Contains(t, out, `src="https://data.example.com/portal/refs/a1/t3"`)
	assert.NotContains(t, out, "download=1", "a form is never offered as a download")

	plain := string(assetrefs.Rewrite([]byte(html), "text/html", "", "a1", refs))
	assert.Contains(t, plain, `src="/forms/apply"`, "a read with no query hands the page nothing")
	assert.False(t, strings.Contains(plain, "form_viewer"))
}

func TestFormURI(t *testing.T) {
	assert.Equal(t, "mcp:form:contact", assetrefs.FormURI("contact"))
	name, ok := assetrefs.ParseFormURI("mcp:form:contact")
	assert.True(t, ok)
	assert.Equal(t, "contact", name)
	_, ok = assetrefs.ParseFormURI("mcp:form:")
	assert.False(t, ok)
	assert.True(t, assetrefs.TargetForm.Valid())
}
