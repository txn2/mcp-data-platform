package whadmin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

func formInput() whsource.Source {
	return whsource.Source{
		Name: "form-contact", Enabled: true, Connection: "scratch", CreatedBy: "admin@example.com",
		Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_1", Endpoint: "contact"}},
	}
}

func TestCreate_RefusesAFormSource(t *testing.T) {
	r := newRig()
	_, err := r.svc.Create(ctx, formInput())
	require.ErrorIs(t, err, ErrFormSource)
	assert.Empty(t, r.sources.rows, "nothing is made for an administrator's form source")
}

func TestCreateForm(t *testing.T) {
	r := newRig()
	_, err := r.svc.CreateForm(ctx, input())
	require.ErrorIs(t, err, whsource.ErrInvalid, "CreateForm makes only a form's source")

	src, err := r.svc.CreateForm(ctx, formInput())
	require.NoError(t, err)
	assert.True(t, src.IsForm())
	assert.Contains(t, r.sources.rows, "form-contact")
	assert.Equal(t, 1, r.changed)
}

func TestCreateForm_RefusesAnUnknownDecryptPersona(t *testing.T) {
	r := newRig()
	r.svc.deps.PersonaExists = func(name string) bool { return name == "support" }
	in := formInput()
	in.Config.Form.DecryptPersonas = []string{"nobody"}
	_, err := r.svc.CreateForm(ctx, in)
	require.ErrorIs(t, err, whsource.ErrInvalid)
	assert.Contains(t, err.Error(), "nobody")
}

func TestSetDecryptPersonas(t *testing.T) {
	r := newRig()
	r.svc.deps.PersonaExists = func(name string) bool { return name == "support" }
	_, err := r.svc.CreateForm(ctx, formInput())
	require.NoError(t, err)

	src, err := r.svc.SetDecryptPersonas(ctx, "form-contact", []string{"support"})
	require.NoError(t, err)
	assert.Equal(t, []string{"support"}, src.Config.Form.DecryptPersonas)
	assert.Equal(t, []string{"support"}, r.sources.rows["form-contact"].Config.Form.DecryptPersonas)

	_, err = r.svc.SetDecryptPersonas(ctx, "form-contact", []string{"nobody"})
	require.ErrorIs(t, err, whsource.ErrInvalid)
	_, err = r.svc.SetDecryptPersonas(ctx, "missing", nil)
	require.ErrorIs(t, err, whsource.ErrNotFound)

	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err)
	_, err = r.svc.SetDecryptPersonas(ctx, "esp", nil)
	require.ErrorIs(t, err, whsource.ErrInvalid, "a posted source has no decrypt personas")
}

func TestUpdate_KeepsAFormsBinding(t *testing.T) {
	r := newRig()
	_, err := r.svc.CreateForm(ctx, formInput())
	require.NoError(t, err)
	days := 30
	src, err := r.svc.Update(ctx, "form-contact", Update{Config: whsource.Config{
		CompactedRetentionDays: &days, Form: &whsource.Form{ScriptID: "another", Endpoint: "elsewhere"},
	}})
	require.NoError(t, err)
	assert.Equal(t, "scr_1", src.Config.Form.ScriptID, "an administrator's update does not rebind the form")
	assert.Equal(t, 30, *src.Config.CompactedRetentionDays)
	assert.True(t, src.IsForm())
}

func TestUpdate_RefusesMakingASourceAForms(t *testing.T) {
	r := newRig()
	_, err := r.svc.Create(ctx, input())
	require.NoError(t, err)
	for name, cfg := range map[string]whsource.Config{
		"the input": {Input: whsource.InputForm},
		"a form":    {Form: &whsource.Form{ScriptID: "s", Endpoint: "e"}},
	} {
		_, err = r.svc.Update(ctx, "esp", Update{Auth: input().Auth, Config: cfg})
		require.ErrorIs(t, err, whsource.ErrInvalid, name)
		assert.Contains(t, err.Error(), "only for a form's source", name)
	}
}
