// Package epform is the form contract of an endpoint (#2176): a bound script
// that defines form_spec(req) and form_submit(req, values) is a form. A GET
// is answered with the spec form_spec returns, checked here and signed into a
// form token; a submission is checked against the spec its token carries
// (declared fields only, types, required fields, limits, the honeypot and the
// minimum fill time) before form_submit runs, and form_submit may add its own
// per-field errors or accept.
package epform

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// The functions a form script defines.
const (
	// SpecName is called with the request on a GET and returns the spec.
	SpecName = scriptdialect.FormSpecName
	// SubmitName is called with the request and the checked values on a
	// submission, and returns None to accept or {"errors": {...}}.
	SubmitName = scriptdialect.FormSubmitName
)

// The fields a submission carries that are the platform's, not the form's.
const (
	// TokenField carries the form token the GET issued.
	TokenField = "form_token"
	// HoneypotField is rendered hidden from people; a value in it is a
	// program filling every field it finds, and the submission is refused.
	HoneypotField = "website"
)

// Field types.
const (
	TypeText           = "text"
	TypeLongText       = "long_text"
	TypeEmail          = "email"
	TypeNumber         = "number"
	TypeDate           = "date"
	TypeSingleChoice   = "single_choice"
	TypeMultipleChoice = "multiple_choice"
	TypeCheckbox       = "checkbox"
	TypeHidden         = "hidden"
)

// Types is every field type, in the order they are documented.
var Types = []string{
	TypeText, TypeLongText, TypeEmail, TypeNumber, TypeDate, TypeSingleChoice, TypeMultipleChoice, TypeCheckbox, TypeHidden,
}

// Bounds of a spec.
const (
	// DefaultMinFillSeconds is how long after its token was issued a form may
	// be submitted, unless the spec asks for longer.
	DefaultMinFillSeconds = 2
	maxMinFillSeconds     = 3600
	maxFields             = 100
	maxChoices            = 500
	maxNameLen            = 64
	maxLabelLen           = 200
	maxHelpLen            = 1000
	maxMessageLen         = 2000
	maxRedirectLen        = 2048
	// DefaultTextLength and DefaultLongTextLength bound a text field that
	// names no max_length; MaxLength is the most any may name.
	DefaultTextLength     = 1000
	DefaultLongTextLength = 10000
	MaxLength             = 100000
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Choice is one option of a choice field.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one field of a form.
type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	// MaxLength bounds a text, long text or email field's characters.
	MaxLength int `json:"max_length,omitempty"`
	// Min and Max bound a number field.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Help is shown under the field.
	Help    string   `json:"help,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
}

// Spec is what form_spec returns: the fields, the values they start with,
// what a person is shown once the form is accepted, and where they are sent.
type Spec struct {
	Fields []Field `json:"fields"`
	// Prefill is each field's starting value; a hidden field's is the value a
	// submission must carry back unchanged.
	Prefill map[string]any `json:"prefill,omitempty"`
	// Confirmation is shown once the form is accepted.
	Confirmation string `json:"confirmation,omitempty"`
	// Redirect is where the browser is sent once the form is accepted.
	Redirect string `json:"redirect,omitempty"`
	// MinFillSeconds is how long after it was shown the form may be
	// submitted, at least DefaultMinFillSeconds.
	MinFillSeconds int `json:"min_fill_seconds,omitempty"`
}

// Defines reports whether file is a form script: it defines form_spec taking
// one parameter and form_submit taking two.
func Defines(file *syntax.File) bool {
	return definesFunc(file, SpecName, 1) && definesFunc(file, SubmitName, 2)
}

// DefinesSource is Defines over a source, false when it does not parse.
func DefinesSource(source string) bool {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	return err == nil && Defines(file)
}

func definesFunc(file *syntax.File, name string, params int) bool {
	if file == nil {
		return false
	}
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == name && len(d.Params) == params {
			return true
		}
	}
	return false
}

// SpecFrom reads what form_spec returned into a checked spec.
func SpecFrom(v starlark.Value) (Spec, error) {
	if _, ok := v.(*starlark.Dict); !ok {
		return Spec{}, fmt.Errorf("the spec handler %s(req) returned %s; it returns a dict with fields", SpecName, v.Type())
	}
	plain, err := starlarkconv.FromStarlark(v)
	if err != nil {
		return Spec{}, fmt.Errorf("reading what %s(req) returned: %w", SpecName, err)
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		return Spec{}, fmt.Errorf("reading what %s(req) returned: %w", SpecName, err)
	}
	var s Spec
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("the spec handler %s(req) returned a spec this platform does not read: %w", SpecName, err)
	}
	s.normalize()
	return s, s.Validate()
}

// normalize fills the defaults a spec leaves out.
func (s *Spec) normalize() {
	if s.MinFillSeconds < DefaultMinFillSeconds {
		s.MinFillSeconds = DefaultMinFillSeconds
	}
	if s.Prefill == nil {
		s.Prefill = map[string]any{}
	}
	for i := range s.Fields {
		f := &s.Fields[i]
		if f.MaxLength == 0 {
			f.MaxLength = defaultLength(f.Type)
		}
	}
}

func defaultLength(typ string) int {
	if typ == TypeLongText {
		return DefaultLongTextLength
	}
	return DefaultTextLength
}

// Validate reports the first thing wrong with the spec.
func (s Spec) Validate() error {
	if err := s.validateShape(); err != nil {
		return err
	}
	return s.validateFields()
}

// validateShape checks what the spec says beside its fields.
func (s Spec) validateShape() error {
	switch {
	case len(s.Fields) == 0:
		return errors.New("a form spec declares at least one field")
	case len(s.Fields) > maxFields:
		return fmt.Errorf("a form spec declares at most %d fields", maxFields)
	case len(s.Confirmation) > maxMessageLen:
		return fmt.Errorf("confirmation is longer than %d characters", maxMessageLen)
	case len(s.Redirect) > maxRedirectLen || (s.Redirect != "" && !validRedirect(s.Redirect)):
		return fmt.Errorf("redirect %q must be an https or http URL, or a path starting with /", s.Redirect)
	case s.MinFillSeconds > maxMinFillSeconds:
		return fmt.Errorf("min_fill_seconds must be at most %d", maxMinFillSeconds)
	}
	return nil
}

// validateFields checks each field, that no name is declared twice, and that
// the prefill names only declared fields.
func (s Spec) validateFields() error {
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if seen[f.Name] {
			return fmt.Errorf("field %q is declared twice", f.Name)
		}
		seen[f.Name] = true
		if err := f.validate(); err != nil {
			return err
		}
	}
	for name := range s.Prefill {
		if !seen[name] {
			return fmt.Errorf("prefill names %q, which is not a declared field", name)
		}
	}
	return nil
}

func validRedirect(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") ||
		(strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"))
}

func (f Field) validate() error {
	if err := f.validateName(); err != nil {
		return err
	}
	switch {
	case !slices.Contains(Types, f.Type):
		return fmt.Errorf("field %q has type %q, which is not one of %s", f.Name, f.Type, strings.Join(Types, ", "))
	case len(f.Label) > maxLabelLen || len(f.Help) > maxHelpLen:
		return fmt.Errorf("field %q has a label past %d or help past %d characters", f.Name, maxLabelLen, maxHelpLen)
	case f.MaxLength < 1 || f.MaxLength > MaxLength:
		return fmt.Errorf("field %q has max_length %d; it is from 1 to %d", f.Name, f.MaxLength, MaxLength)
	case f.Min != nil && f.Max != nil && *f.Min > *f.Max:
		return fmt.Errorf("field %q has min past max", f.Name)
	}
	return f.validateChoices()
}

func (f Field) validateName() error {
	switch {
	case !namePattern.MatchString(f.Name) || len(f.Name) > maxNameLen:
		return fmt.Errorf("field name %q must be lowercase letters, digits and underscores, starting with a letter", f.Name)
	case f.Name == TokenField || f.Name == HoneypotField:
		return fmt.Errorf("field name %q is the platform's", f.Name)
	}
	return nil
}

func (f Field) validateChoices() error {
	choice := f.Type == TypeSingleChoice || f.Type == TypeMultipleChoice
	switch {
	case choice && len(f.Choices) == 0:
		return fmt.Errorf("field %q is a choice with no choices", f.Name)
	case !choice && len(f.Choices) > 0:
		return fmt.Errorf("field %q is a %s and lists choices", f.Name, f.Type)
	case len(f.Choices) > maxChoices:
		return fmt.Errorf("field %q lists more than %d choices", f.Name, maxChoices)
	}
	seen := map[string]bool{}
	for _, c := range f.Choices {
		if c.Value == "" || seen[c.Value] {
			return fmt.Errorf("field %q has an empty or repeated choice value %q", f.Name, c.Value)
		}
		seen[c.Value] = true
	}
	return nil
}

// field is the declared field named name.
func (s Spec) field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// ErrRefused is a submission refused as a whole rather than field by field:
// its token is missing, altered, expired or another form's, the honeypot was
// filled, or it came back sooner than a person fills it in.
var ErrRefused = errors.New("the submission was refused")

// refusal is ErrRefused with the reason a person is told.
type refusal struct{ reason string }

func (r refusal) Error() string { return ErrRefused.Error() + ": " + r.reason }

func (refusal) Unwrap() error { return ErrRefused }

// refused is the refusal of a submission for reason.
func refused(reason string) error { return refusal{reason: reason} }
