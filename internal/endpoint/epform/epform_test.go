package epform

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

var key = []byte("0123456789abcdef0123456789abcdef")

// contact is a spec with one field of every type.
func contact() Spec {
	s := Spec{
		Fields: []Field{
			{Name: "name", Type: TypeText, Required: true, MaxLength: 20},
			{Name: "message", Type: TypeLongText},
			{Name: "email", Type: TypeEmail, Required: true},
			{Name: "age", Type: TypeNumber, Min: new(18.0), Max: new(120.0)},
			{Name: "start", Type: TypeDate},
			{Name: "plan", Type: TypeSingleChoice, Choices: []Choice{{Value: "basic"}, {Value: "pro"}}},
			{Name: "topics", Type: TypeMultipleChoice, Choices: []Choice{{Value: "a"}, {Value: "b"}}},
			{Name: "agree", Type: TypeCheckbox, Required: true},
			{Name: "customer", Type: TypeHidden},
		},
		Prefill: map[string]any{"customer": "c-42"},
	}
	s.normalize()
	return s
}

func TestDefines(t *testing.T) {
	parse := func(src string) bool {
		file, err := scriptdialect.Options.Parse("s", src, 0)
		require.NoError(t, err)
		return Defines(file)
	}
	assert.True(t, parse("def form_spec(req):\n    pass\n\ndef form_submit(req, values):\n    pass\n"))
	assert.False(t, parse("def form_spec(req):\n    pass\n"), "a spec handler alone is not a form")
	assert.False(t, parse("def form_spec(req):\n    pass\n\ndef form_submit(req):\n    pass\n"), "form_submit takes two parameters")
	assert.False(t, Defines(nil))
	assert.False(t, DefinesSource("def ("), "a source that does not parse is no form")
}

func TestSpecFrom_ReadsWhatFormSpecReturns(t *testing.T) {
	v, err := starlarkconv.ToStarlark(map[string]any{
		"fields": []any{
			map[string]any{"name": "plan", "type": "single_choice", "choices": []any{map[string]any{"value": "pro", "label": "Pro"}}},
			map[string]any{"name": "notes", "type": "long_text"},
		},
		"confirmation": "Thanks", "min_fill_seconds": 1,
	})
	require.NoError(t, err)
	s, err := SpecFrom(v)
	require.NoError(t, err)
	assert.Equal(t, DefaultMinFillSeconds, s.MinFillSeconds, "a minimum below the platform's is raised to it")
	assert.Equal(t, DefaultLongTextLength, s.Fields[1].MaxLength)
	assert.Equal(t, DefaultTextLength, s.Fields[0].MaxLength)
	assert.Equal(t, map[string]any{}, s.Prefill)

	_, err = SpecFrom(starlark.String("x"))
	require.ErrorContains(t, err, "returned string")
	bad, _ := starlarkconv.ToStarlark(map[string]any{"fields": []any{}, "color": "red"})
	_, err = SpecFrom(bad)
	require.ErrorContains(t, err, "does not read")
}

func TestValidate_RefusesEachBadSpec(t *testing.T) {
	require.NoError(t, contact().Validate())
	cases := map[string]struct {
		edit func(*Spec)
		want string
	}{
		"no fields":         {func(s *Spec) { s.Fields = nil }, "at least one field"},
		"too many":          {func(s *Spec) { s.Fields = make([]Field, maxFields+1) }, "at most"},
		"twice":             {func(s *Spec) { s.Fields[1].Name = "name" }, "declared twice"},
		"bad name":          {func(s *Spec) { s.Fields[0].Name = "Name" }, "lowercase"},
		"token name":        {func(s *Spec) { s.Fields[0].Name = TokenField }, "the platform's"},
		"honeypot name":     {func(s *Spec) { s.Fields[0].Name = HoneypotField }, "the platform's"},
		"bad type":          {func(s *Spec) { s.Fields[0].Type = "color" }, "not one of"},
		"long label":        {func(s *Spec) { s.Fields[0].Label = strings.Repeat("l", maxLabelLen+1) }, "label past"},
		"length":            {func(s *Spec) { s.Fields[0].MaxLength = MaxLength + 1 }, "max_length"},
		"min past max":      {func(s *Spec) { s.Fields[3].Min = new(200.0) }, "min past max"},
		"no choices":        {func(s *Spec) { s.Fields[5].Choices = nil }, "no choices"},
		"choices on text":   {func(s *Spec) { s.Fields[0].Choices = []Choice{{Value: "x"}} }, "lists choices"},
		"repeated choice":   {func(s *Spec) { s.Fields[5].Choices = []Choice{{Value: "a"}, {Value: "a"}} }, "repeated choice"},
		"empty choice":      {func(s *Spec) { s.Fields[5].Choices = []Choice{{Value: ""}} }, "empty or repeated"},
		"prefill unknown":   {func(s *Spec) { s.Prefill["nope"] = 1 }, "not a declared field"},
		"redirect":          {func(s *Spec) { s.Redirect = "javascript:alert(1)" }, "redirect"},
		"protocol relative": {func(s *Spec) { s.Redirect = "//evil.example.com" }, "redirect"},
		"confirmation":      {func(s *Spec) { s.Confirmation = strings.Repeat("c", maxMessageLen+1) }, "confirmation"},
		"slow":              {func(s *Spec) { s.MinFillSeconds = maxMinFillSeconds + 1 }, "min_fill_seconds"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := contact()
			tc.edit(&s)
			require.ErrorContains(t, s.Validate(), tc.want)
		})
	}
	ok := contact()
	ok.Redirect = "/thanks"
	require.NoError(t, ok.Validate())
}

// #2176: a token opens only on the endpoint that issued it, unaltered and
// within its lifetime.
func TestToken_OpensOnlyWhereItWasIssuedAndUnaltered(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok, err := Sign(key, "contact", contact(), now)
	require.NoError(t, err)
	spec, issued, err := Open(key, "contact", tok, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), issued.Unix())
	assert.Equal(t, contact().Fields, spec.Fields)

	_, _, err = Open(key, "signup", tok, now)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "another form")

	body, sig, _ := strings.Cut(tok, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	altered := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), "c-42", "c-43", 1))) + "." + sig
	_, _, err = Open(key, "contact", altered, now)
	require.ErrorContains(t, err, "was changed")
	_, _, err = Open([]byte("another key, another deployment"), "contact", tok, now)
	require.ErrorContains(t, err, "was changed")
	_, _, err = Open(key, "contact", "", now)
	require.ErrorContains(t, err, "carries no form_token")
	_, _, err = Open(key, "contact", "nodot", now)
	require.ErrorIs(t, err, ErrRefused)
	_, _, err = Open(key, "contact", tok, now.Add(TokenTTL+time.Second))
	require.ErrorContains(t, err, "load it again")

	notJSON := base64.RawURLEncoding.EncodeToString([]byte("x"))
	_, _, err = Open(key, "contact", notJSON+"."+base64.RawURLEncoding.EncodeToString(mac(key, notJSON)), now)
	require.ErrorContains(t, err, "cannot be read")

	big := contact()
	big.Confirmation = strings.Repeat("x", maxTokenLen)
	_, err = Sign(key, "contact", big, now)
	require.ErrorContains(t, err, "too large")
	assert.Nil(t, Key(nil))
	assert.Len(t, Key(key), 32)
}

func TestValues_ReadsAFormAndJSONTheSame(t *testing.T) {
	form, _, err := Read("application/x-www-form-urlencoded; charset=utf-8", []byte("name=Ann&topics=a&topics=b&agree=on&age=30"))
	require.NoError(t, err)
	js, _, err := Read("application/json", []byte(`{"name":"Ann","topics":["a","b"],"agree":true,"age":30,"start":null}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"Ann"}, js["name"])
	assert.Equal(t, form["topics"], js["topics"])
	assert.Equal(t, []string{"30"}, js["age"])
	assert.Equal(t, []string{"true"}, js["agree"])
	assert.Equal(t, []string{}, js["start"])

	for name, tc := range map[string]struct{ ct, body string }{
		"not an object": {"application/json", `[1]`},
		"nested":        {"application/json", `{"a":{"b":1}}`},
		"nested list":   {"application/json", `{"a":[[1]]}`},
		"bad form":      {"application/x-www-form-urlencoded", "a=%zz"},
		"other type":    {"text/plain", "a=1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Read(tc.ct, []byte(tc.body))
			require.Error(t, err)
		})
	}
}

// #2176: a submission is checked against the spec its token carries; each
// field it gets wrong is named, and an undeclared field is refused.
func TestCheck_RefusesEachFieldItGetsWrong(t *testing.T) {
	issued := time.Unix(1_800_000_000, 0)
	later := issued.Add(time.Minute)
	good := map[string][]string{
		"name": {"Ann"}, "email": {"ann@example.com"}, "age": {"30"}, "start": {"2026-11-01"}, "plan": {"pro"},
		"topics": {"a", "b", "a"}, "agree": {"on"}, "customer": {"c-42"}, TokenField: {"t"}, HoneypotField: {""},
	}
	values, errs, err := Check(contact(), issued, good, nil, later)
	require.NoError(t, err)
	require.Empty(t, errs)
	assert.Equal(t, int64(30), values["age"])
	assert.Equal(t, []any{"a", "b"}, values["topics"], "de-duplicated in order")
	assert.Equal(t, true, values["agree"])
	assert.Equal(t, "", values["message"], "a field left empty is present and empty")

	bad := map[string][]string{
		"name": {strings.Repeat("n", 21)}, "email": {"Ann <ann@example.com>"}, "age": {"12"}, "start": {"01/11/2026"},
		"plan": {"gold"}, "topics": {"c"}, "agree": {""}, "customer": {"c-43"}, "color": {"red"}, "message": {"a", "b"},
	}
	_, errs, err = Check(contact(), issued, bad, nil, later)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"name":     "this is longer than 20 characters",
		"email":    "this is not an email address",
		"age":      "this is less than 18",
		"start":    "this is not a date (YYYY-MM-DD)",
		"plan":     "this is not one of the choices",
		"topics":   `"c" is not one of the choices`,
		"agree":    "this box must be ticked",
		"customer": "this field was changed",
		"color":    "this form has no such field",
		"message":  "this field takes one value",
	}, errs)

	_, errs, _ = Check(contact(), issued, map[string][]string{"age": {"x"}, "agree": {"maybe"}}, nil, later)
	assert.Equal(t, "this field is required", errs["name"])
	assert.Equal(t, "this is not a number", errs["age"])
	assert.Equal(t, "this is not a ticked or unticked box", errs["agree"])

	over := map[string][]string{"age": {"121.5"}, "agree": {"1", "1"}}
	_, errs, _ = Check(contact(), issued, over, nil, later)
	assert.Equal(t, "this is more than 120", errs["age"])
	assert.Equal(t, "this field takes one value", errs["agree"])

	required := contact()
	required.Fields[6].Required = true
	_, errs, _ = Check(required, issued, map[string][]string{}, nil, later)
	assert.Equal(t, "choose at least one", errs["topics"])

	fractional := map[string][]string{"name": {"Ann"}, "email": {"a@example.com"}, "agree": {"yes"}, "age": {"30.5"}, "customer": {"c-42"}}
	values, errs, _ = Check(contact(), issued, fractional, nil, later)
	require.Empty(t, errs)
	assert.Equal(t, 30.5, values["age"])
	assert.Equal(t, true, values["agree"], "yes ticks a box")
}

// #2176: the honeypot and the minimum fill time refuse a submission whole.
func TestCheck_TheHoneypotAndTheMinimumFillTime(t *testing.T) {
	issued := time.Unix(1_800_000_000, 0)
	_, _, err := Check(contact(), issued, map[string][]string{HoneypotField: {"https://spam.example.com"}}, nil, issued.Add(time.Minute))
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "people do not see")

	_, _, err = Check(contact(), issued, map[string][]string{}, nil, issued.Add(time.Second))
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "sooner than a person")

	slow := contact()
	slow.MinFillSeconds = 30
	_, _, err = Check(slow, issued, map[string][]string{}, nil, issued.Add(10*time.Second))
	require.ErrorContains(t, err, "sooner", "a spec may raise the minimum")
}
