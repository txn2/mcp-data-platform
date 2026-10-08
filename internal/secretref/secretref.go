// Package secretref is the reference to a stored secret a caller writes into
// a request, `{{secret:<name>}}`, and the redaction of a secret's value from
// whatever comes back (#2051).
//
// A placeholder is filled at the last moment, as the request is built for
// sending, so everything that records the call (the audit row, the call
// record, a script's recording, the arguments a cut response hands back)
// holds the placeholder and never the value. What the upstream sends back is
// then put through a Redactor holding the values the request carried, so an
// upstream that echoes one cannot hand it to the caller.
//
// The package knows nothing about where secrets are stored or who may use
// them: a Lookup answers for one name, and refuses by name.
package secretref

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// NamePattern is the grammar of a secret's name: lower case, digits, and
// . _ - after the first character. It is narrow so a name reads the same in
// a placeholder, a URL and a log line.
const NamePattern = `[a-z0-9][a-z0-9_.-]{0,62}`

// nameRE is NamePattern anchored.
var nameRE = regexp.MustCompile(`^` + NamePattern + `$`)

// ValidName reports whether name is one a secret can be stored under.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// opener is how every placeholder starts. A string holding it that is not a
// whole, well-formed placeholder is refused rather than sent as written.
const opener = "{{secret:"

// placeholderRE matches one placeholder.
var placeholderRE = regexp.MustCompile(`\{\{secret:(` + NamePattern + `)\}\}`)

// escapedPlaceholderRE matches a placeholder as path_params substitution
// left it: braces percent-escaped.
var escapedPlaceholderRE = regexp.MustCompile(`(?i)%7B%7Bsecret:(` + NamePattern + `)%7D%7D`)

// Lookup returns a secret's value, or an error that names the secret and
// says why it may not be used here.
type Lookup func(name string) (string, error)

// ErrMalformed is wrapped by the refusal of text that opens a placeholder
// and does not complete one.
var ErrMalformed = errors.New("malformed secret placeholder")

// Fill replaces each placeholder in s with its secret's value as escape
// writes it. A placeholder whose lookup fails fails the fill, and so does
// text that opens a placeholder without completing one: neither is ever
// sent as written.
func Fill(s string, lookup Lookup, escape func(string) string) (string, error) {
	if !strings.Contains(s, opener) {
		return s, nil
	}
	var failed error
	out := placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		if failed != nil {
			return m
		}
		value, err := lookup(placeholderRE.FindStringSubmatch(m)[1])
		if err != nil {
			failed = err
			return m
		}
		return escape(value)
	})
	if failed != nil {
		return "", failed
	}
	if strings.Contains(placeholderRE.ReplaceAllString(s, ""), opener) {
		return "", fmt.Errorf("write {{secret:<name>}}, where a name is lower case letters, digits, '.', '_' and '-': %w", ErrMalformed)
	}
	return out, nil
}

// FillPath fills the placeholders of a request path, in either form a path
// carries one: as the caller wrote it, or with its braces escaped by
// path_params substitution. The value is path-escaped, so it cannot add a
// segment.
func FillPath(path string, lookup Lookup) (string, error) {
	var failed error
	out := escapedPlaceholderRE.ReplaceAllStringFunc(path, func(m string) string {
		value, err := lookup(escapedPlaceholderRE.FindStringSubmatch(m)[1])
		if err != nil && failed == nil {
			failed = err
		}
		return url.PathEscape(value)
	})
	if failed != nil {
		return "", failed
	}
	return Fill(out, lookup, url.PathEscape)
}

// Raw writes a value as it is: for a body object, query values and header
// values, which are encoded on their way out.
func Raw(s string) string { return s }

// JSONString writes a value as the inside of a JSON string, for a body sent
// as JSON text.
func JSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// FillValue fills every string in a JSON-shaped value (maps, lists and
// strings, nested), returning a copy: the value the caller passed is the
// one recorded, and keeps its placeholders. Map keys are filled too.
func FillValue(v any, lookup Lookup) (any, error) {
	switch v := v.(type) {
	case string:
		return Fill(v, lookup, Raw)
	case map[string]any:
		return fillMap(v, lookup)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			filled, err := FillValue(item, lookup)
			if err != nil {
				return nil, err
			}
			out[i] = filled
		}
		return out, nil
	default:
		return v, nil
	}
}

// fillMap is FillValue of a map, keys and values both.
func fillMap(m map[string]any, lookup Lookup) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, item := range m {
		key, err := Fill(k, lookup, Raw)
		if err != nil {
			return nil, err
		}
		if out[key], err = FillValue(item, lookup); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FillStrings fills the values of a string map, returning a copy.
func FillStrings(m map[string]string, lookup Lookup) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		filled, err := Fill(v, lookup, Raw)
		if err != nil {
			return nil, err
		}
		out[k] = filled
	}
	return out, nil
}
