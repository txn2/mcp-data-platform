package epform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"
)

// dateLayout is the one form a date field takes, the one an HTML date input
// submits.
const dateLayout = "2006-01-02"

// The number forms a number field reads and writes: a 64-bit float, or a
// base-10 64-bit integer when it is a whole number.
const (
	floatBits = 64
	intBase   = 10
	intBits   = 64
)

// maxParts bounds the parts of a multipart submission.
const maxParts = 1000

// Read reads a submission body into each field's values and the files it
// carries: a urlencoded body and a JSON object alike, where a JSON number,
// boolean or array of them reads as the strings a urlencoded body would
// carry, and a multipart/form-data body, the one form a submission with files
// takes (#2333).
func Read(contentType string, body []byte) (map[string][]string, []Upload, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "application/x-www-form-urlencoded":
		v, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, nil, fmt.Errorf("the submission is not a form: %w", err)
		}
		return v, nil, nil
	case "application/json":
		v, err := jsonValues(body)
		return v, nil, err
	case "multipart/form-data":
		return multipartValues(params["boundary"], body)
	}
	return nil, nil, fmt.Errorf("a submission is application/x-www-form-urlencoded, application/json or multipart/form-data, not %q", mediaType)
}

// multipartValues reads a multipart body: each part with a file name is a
// file, each other part a value.
func multipartValues(boundary string, body []byte) (map[string][]string, []Upload, error) {
	if boundary == "" {
		return nil, nil, errNoBoundary
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	values := map[string][]string{}
	var files []Upload
	for n := 0; ; n++ {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return values, files, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the submission is not a multipart form: %w", err)
		}
		if n >= maxParts {
			return nil, nil, fmt.Errorf("the submission is not a multipart form: it has more than %d parts", maxParts)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, nil, fmt.Errorf("the submission is not a multipart form: %w", err)
		}
		if name := part.FileName(); name != "" {
			files = append(files, Upload{Field: part.FormName(), Name: name, Data: data})
			continue
		}
		values[part.FormName()] = append(values[part.FormName()], string(data))
	}
}

func jsonValues(body []byte) (map[string][]string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("the submission is not a JSON object: %w", err)
	}
	out := make(map[string][]string, len(obj))
	for name, v := range obj {
		values, err := jsonStrings(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		out[name] = values
	}
	return out, nil
}

func jsonStrings(v any) ([]string, error) {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, err := jsonScalar(item)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}
	if v == nil {
		return []string{}, nil
	}
	s, err := jsonScalar(v)
	if err != nil {
		return nil, err
	}
	return []string{s}, nil
}

func jsonScalar(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	}
	return "", errors.New("a value is a string, a number, a boolean or a list of them")
}

// Check is a submission checked against spec, issued when its token was
// signed: the values form_submit is handed, by field, or the per-field
// errors, or ErrRefused when the honeypot was filled or the form came back
// sooner than a person fills it in. Every declared field is in values: an
// empty one is "" (a list for a multiple choice or a file field, False for a
// checkbox). A file field's value describes each of its files, in the order
// they were sent.
func Check(spec Spec, issued time.Time, submitted map[string][]string, files []Upload, now time.Time) (values map[string]any, errs map[string]string, err error) {
	if first(submitted[HoneypotField]) != "" {
		return nil, nil, refused("a field people do not see was filled in")
	}
	if now.Sub(issued) < time.Duration(spec.MinFillSeconds)*time.Second {
		return nil, nil, refused("the form came back sooner than a person fills it in; wait a moment and send it again")
	}
	errs = spec.undeclared(submitted)
	byField := spec.uploadsByField(files, errs)
	values = make(map[string]any, len(spec.Fields))
	for _, f := range spec.Fields {
		if _, done := errs[f.Name]; done {
			continue
		}
		v, msg := f.checkValue(spec.Prefill[f.Name], submitted[f.Name], byField[f.Name])
		if msg != "" {
			errs[f.Name] = msg
			continue
		}
		values[f.Name] = v
	}
	if len(errs) > 0 {
		return nil, errs, nil
	}
	return values, nil, nil
}

// undeclared names each submitted value the spec does not declare, other
// than the platform's own fields.
func (s Spec) undeclared(submitted map[string][]string) map[string]string {
	errs := map[string]string{}
	for name := range submitted {
		if _, ok := s.field(name); !ok && name != TokenField && name != HoneypotField {
			errs[name] = "this form has no such field"
		}
	}
	return errs
}

// uploadsByField groups files by the field they were sent in, naming in errs
// each file sent to a field that is not declared or takes no files.
func (s Spec) uploadsByField(files []Upload, errs map[string]string) map[string][]Upload {
	byField := map[string][]Upload{}
	for _, u := range files {
		f, ok := s.field(u.Field)
		switch {
		case !ok:
			errs[u.Field] = "this form has no such field"
		case f.Type != TypeFile:
			errs[u.Field] = "this field takes no files"
		}
		byField[u.Field] = append(byField[u.Field], u)
	}
	return byField
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// checkValue is one field's value from what was submitted for it, or the
// message that refuses it.
func (f Field) checkValue(prefill any, values []string, files []Upload) (value any, refusal string) {
	if f.Type == TypeFile {
		return f.checkFiles(values, files)
	}
	return f.check(prefill, values)
}

// check is one field's value, or the message that refuses it.
func (f Field) check(prefill any, values []string) (value any, refusal string) {
	switch f.Type {
	case TypeMultipleChoice:
		return f.checkMany(values)
	case TypeCheckbox:
		return f.checkBox(values)
	}
	if len(values) > 1 {
		return nil, "this field takes one value"
	}
	v := first(values)
	if v == "" {
		if f.Required {
			return nil, "this field is required"
		}
		return "", ""
	}
	return f.checkOne(prefill, v)
}

func (f Field) checkOne(prefill any, v string) (value any, refusal string) {
	if f.Type == TypeNumber {
		return f.checkNumber(v)
	}
	if msg := f.checkShape(prefill, v); msg != "" {
		return nil, msg
	}
	if utf8.RuneCountInString(v) > f.MaxLength {
		return nil, fmt.Sprintf("this is longer than %d characters", f.MaxLength)
	}
	return v, ""
}

// checkShape is the message refusing a value that is not its type's, empty
// when it is.
func (f Field) checkShape(prefill any, v string) string {
	switch f.Type {
	case TypeDate:
		if _, err := time.Parse(dateLayout, v); err != nil {
			return "this is not a date (YYYY-MM-DD)"
		}
	case TypeSingleChoice:
		if !f.offers(v) {
			return "this is not one of the choices"
		}
	case TypeHidden:
		if fmt.Sprint(prefill) != v {
			return "this field was changed"
		}
	case TypeEmail:
		if addr, err := mail.ParseAddress(v); err != nil || addr.Address != v {
			return "this is not an email address"
		}
	}
	return ""
}

func (f Field) checkNumber(v string) (value any, refusal string) {
	n, err := strconv.ParseFloat(v, floatBits)
	switch {
	case err != nil:
		return nil, "this is not a number"
	case f.Min != nil && n < *f.Min:
		return nil, "this is less than " + strconv.FormatFloat(*f.Min, 'f', -1, floatBits)
	case f.Max != nil && n > *f.Max:
		return nil, "this is more than " + strconv.FormatFloat(*f.Max, 'f', -1, floatBits)
	}
	if i, err := strconv.ParseInt(v, intBase, intBits); err == nil {
		return i, ""
	}
	return n, ""
}

func (f Field) checkMany(values []string) (value any, refusal string) {
	chosen := []string{}
	for _, v := range values {
		if v == "" {
			continue
		}
		if !f.offers(v) {
			return nil, fmt.Sprintf("%q is not one of the choices", v)
		}
		if !slices.Contains(chosen, v) {
			chosen = append(chosen, v)
		}
	}
	if f.Required && len(chosen) == 0 {
		return nil, "choose at least one"
	}
	out := make([]any, len(chosen))
	for i, v := range chosen {
		out[i] = v
	}
	return out, ""
}

func (f Field) checkBox(values []string) (value any, refusal string) {
	if len(values) > 1 {
		return nil, "this field takes one value"
	}
	switch first(values) {
	case "", "false", "off", "0":
		if f.Required {
			return nil, "this box must be ticked"
		}
		return false, ""
	case "true", "on", "1", "yes":
		return true, ""
	}
	return nil, "this is not a ticked or unticked box"
}

// offers reports whether a choice field offers the value.
func (f Field) offers(v string) bool {
	return slices.ContainsFunc(f.Choices, func(c Choice) bool { return c.Value == v })
}
