package whsource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func formSource() Source {
	return Source{
		Name: "form-contact-us", Connection: "scratch",
		Config: Config{Input: InputForm, Form: &Form{ScriptID: "scr_1", Endpoint: "contact_us", DecryptPersonas: []string{"support"}}},
	}
}

func TestForm_ValidatesAndNames(t *testing.T) {
	s := formSource().WithDefaults()
	require.NoError(t, Validate(s))
	assert.True(t, s.IsForm())
	assert.False(t, s.IsIMAP())
	assert.Equal(t, "form-contact-us", FormSourceName("contact_us"))
	assert.Equal(t, "webhook_form_contact_us", s.TableName())
}

func TestForm_Refusals(t *testing.T) {
	cases := map[string]struct {
		edit func(*Source)
		want string
	}{
		"no form block":      {func(s *Source) { s.Config.Form = nil }, "names the script and endpoint"},
		"no script":          {func(s *Source) { s.Config.Form.ScriptID = " " }, "names the script and endpoint"},
		"a secret":           {func(s *Source) { s.Auth.Secret = "x" }, "takes no auth"},
		"an auth mode":       {func(s *Source) { s.Auth.Mode = AuthHMAC }, "takes no auth"},
		"another name":       {func(s *Source) { s.Name = "contact" }, `is named "form-contact-us"`},
		"an empty persona":   {func(s *Source) { s.Config.Form.DecryptPersonas = []string{""} }, "empty persona"},
		"an event id path":   {func(s *Source) { s.Config.EventIDPath = "$.values.ssn" }, "are the platform's"},
		"a key path":         {func(s *Source) { s.Config.KeyPath = "$.values.email" }, "are the platform's"},
		"a split":            {func(s *Source) { s.Config.Split = "$.items" }, "are the platform's"},
		"an event type path": {func(s *Source) { s.Config.EventTypePath = "$.x" }, "are the platform's"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := formSource()
			f := *s.Config.Form
			s.Config.Form = &f
			tc.edit(&s)
			err := Validate(s.WithDefaults())
			require.ErrorIs(t, err, ErrInvalid)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestForm_BlockRefusedOnOtherInputs(t *testing.T) {
	s := Source{
		Name: "esp", Connection: "scratch", Auth: Auth{Mode: AuthHeaderToken, Header: "X-Token", Secret: "s"},
		Config: Config{Form: &Form{ScriptID: "scr_1", Endpoint: "e"}},
	}
	err := Validate(s.WithDefaults())
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "only for a form's source")
}
