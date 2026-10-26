package epsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

func formSrc() *whsource.Source {
	return &whsource.Source{
		Name: "form-apply", Connection: "scratch",
		Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_1", Endpoint: "apply", DecryptPersonas: []string{"credit"}}},
	}
}

type fakeAdmin struct {
	list     []whsource.Source
	listErr  error
	created  []whsource.Source
	createEr error
	personas map[string][]string
}

func (f *fakeAdmin) List(context.Context) ([]whsource.Source, error) { return f.list, f.listErr }

func (f *fakeAdmin) CreateForm(_ context.Context, src whsource.Source) (whsource.Source, error) {
	if f.createEr != nil {
		return whsource.Source{}, f.createEr
	}
	f.created = append(f.created, src)
	return src, nil
}

func (f *fakeAdmin) SetDecryptPersonas(_ context.Context, name string, p []string) (whsource.Source, error) {
	if f.personas == nil {
		f.personas = map[string][]string{}
	}
	f.personas[name] = p
	for _, s := range f.list {
		if s.Name == name {
			form := *s.Config.Form
			form.DecryptPersonas = p
			s.Config.Form = &form
			return s, nil
		}
	}
	return whsource.Source{}, whsource.ErrNotFound
}

func TestEnsure_MakesTheSourceOnTheOnlyConnection(t *testing.T) {
	a := &fakeAdmin{}
	m := &Maker{Admin: a, Connections: func() []string { return []string{"scratch"} }}
	res, err := m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "contact_us", Actor: "admin@example.com"})
	require.NoError(t, err)
	assert.Equal(t, SourceResult{
		Source: "form-contact-us", Table: "webhook_form_contact_us", Connection: "scratch", DecryptPersonas: []string{}, Created: true,
	}, res)
	require.Len(t, a.created, 1)
	assert.Equal(t, "admin@example.com", a.created[0].CreatedBy)
	assert.True(t, a.created[0].Enabled)
	assert.Equal(t, "scr_1", a.created[0].Config.Form.ScriptID)
}

func TestEnsure_Connections(t *testing.T) {
	a := &fakeAdmin{}
	m := &Maker{Admin: a, Connections: func() []string { return []string{"lake", "scratch"} }}
	_, err := m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "contact"})
	require.ErrorIs(t, err, whsource.ErrInvalid)
	assert.Contains(t, err.Error(), "lake, scratch")

	res, err := m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "contact", Connection: "lake"})
	require.NoError(t, err)
	assert.Equal(t, "lake", res.Connection)

	m.Connections = nil
	_, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_2", Endpoint: "other"})
	require.ErrorContains(t, err, "has none")
}

func TestEnsure_KeepsAnExistingSource(t *testing.T) {
	a := &fakeAdmin{list: []whsource.Source{*formSrc()}}
	m := &Maker{Admin: a}
	res, err := m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "renamed"})
	require.NoError(t, err)
	assert.Equal(t, "form-apply", res.Source, "a renamed endpoint keeps storing where it did")
	assert.False(t, res.Created)
	assert.Equal(t, []string{"credit"}, res.DecryptPersonas)
	assert.Empty(t, a.created)

	res, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "apply", DecryptPersonas: []string{}})
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.DecryptPersonas, "[] removes them")

	_, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "scr_1", Endpoint: "apply", Connection: "lake"})
	require.ErrorIs(t, err, whsource.ErrInvalid)
	assert.Contains(t, err.Error(), "does not change")
}

func TestEnsure_Refusals(t *testing.T) {
	m := &Maker{Admin: &fakeAdmin{listErr: errors.New("db down")}}
	_, err := m.Ensure(t.Context(), SourceRequest{ScriptID: "s", Endpoint: "e", Connection: "c"})
	require.ErrorContains(t, err, "db down")

	m = &Maker{Admin: &fakeAdmin{createEr: whsource.ErrExists}}
	_, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "s", Endpoint: "e", Connection: "c"})
	require.ErrorContains(t, err, "is not this form's")

	m = &Maker{Admin: &fakeAdmin{createEr: errors.New("trino down")}}
	_, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "s", Endpoint: "e", Connection: "c"})
	require.ErrorContains(t, err, "trino down")

	_, err = m.Ensure(t.Context(), SourceRequest{ScriptID: "s", Endpoint: strings.Repeat("a", 60), Connection: "c"})
	require.ErrorContains(t, err, "longer than 63")
}

func TestFindAndConnectionChoices(t *testing.T) {
	m := &Maker{Admin: &fakeAdmin{list: []whsource.Source{*formSrc()}}}
	res, found, err := m.Find(t.Context(), "scr_1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "form-apply", res.Source)
	_, found, err = m.Find(t.Context(), "scr_2")
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = (&Maker{Admin: &fakeAdmin{listErr: errors.New("db down")}}).Find(t.Context(), "s")
	require.Error(t, err)

	assert.Equal(t, []string{}, m.ConnectionChoices(), "none is [], never null")
	m.Connections = func() []string { return []string{"scratch"} }
	assert.Equal(t, []string{"scratch"}, m.ConnectionChoices())
}
