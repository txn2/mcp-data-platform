// Package secretscan finds credentials in text the platform is about to keep
// or send somewhere (#2820, and #2800's skill promotion after it).
//
// The audit sanitizer is key-based: it knows a "password" parameter is one.
// Text an agent writes in its own words has no keys, so this reads the text
// itself, two ways:
//
//   - credential shapes: a PEM private key, a bearer token, a provider token
//     with its well-known prefix, a JSON Web Token, credentials written into a
//     URL, and a password-, secret- or token-style assignment;
//   - the deployment's own secret values (stored secrets and encrypted
//     connection fields), as written and in the encodings a value is carried
//     in: base64, hex, URL-escaped and JSON-escaped.
//
// A finding names the field and the kind of match, never the value, so a
// refusal built from it repeats nothing it found.
package secretscan

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// minKnownLength is the shortest stored value matched exactly: a shorter one
// ("admin", "true") would match ordinary prose.
const minKnownLength = 8

// Kinds of finding.
const (
	KindPrivateKey = "a private key"
	KindBearer     = "a bearer token"
	KindToken      = "an access token"
	KindJWT        = "a signed web token"
	KindURLCreds   = "credentials in a URL"
	KindAssignment = "a password or secret assignment"
	KindKnown      = "a secret this deployment stores"
)

// Field is one named piece of text to scan.
type Field struct {
	Name  string
	Value string
}

// Finding is one field holding a credential, and what kind.
type Finding struct {
	Field string
	Kind  string
}

// Known lists the deployment's secret values for one scan. The values are
// held only while the scan runs and are never logged or returned.
type Known func(ctx context.Context) ([]string, error)

// Detector finds credentials in fields.
type Detector struct {
	// Known is the deployment's own secret values; nil checks shapes alone.
	Known Known
}

// shapes are the credential forms recognized by their look.
var shapes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{KindPrivateKey, regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY-{5}`)},
	{KindBearer, regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{KindToken, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|(?:sk|rk)_live_[A-Za-z0-9]{16,}|glpat-[A-Za-z0-9_-]{20,})`)},
	{KindJWT, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{KindURLCreds, regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`)},
	{KindAssignment, regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|private[_-]?key)\s*[:=]\s*["']?[^\s"',;]{6,}`)},
}

// Scan reports every field holding a credential. An error reading the known
// values is returned, so a caller refuses rather than sends text it could not
// check.
func (d Detector) Scan(ctx context.Context, fields []Field) ([]Finding, error) {
	var known []string
	if d.Known != nil {
		values, err := d.Known(ctx)
		if err != nil {
			return nil, err
		}
		known = encodedForms(values)
	}
	var out []Finding
	for _, f := range fields {
		if kind := findIn(f.Value, known); kind != "" {
			out = append(out, Finding{Field: f.Name, Kind: kind})
		}
	}
	return out, nil
}

// findIn is the kind of the first credential text holds, "" for none.
func findIn(text string, known []string) string {
	if text == "" {
		return ""
	}
	for _, k := range known {
		if strings.Contains(text, k) {
			return KindKnown
		}
	}
	for _, s := range shapes {
		if s.re.MatchString(text) {
			return s.kind
		}
	}
	return ""
}

// encodedForms is every value long enough to match, in each form it may be
// carried in.
func encodedForms(values []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if len(s) >= minKnownLength && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, v := range values {
		if len(v) < minKnownLength {
			continue
		}
		add(v)
		add(base64.StdEncoding.EncodeToString([]byte(v)))
		add(base64.RawStdEncoding.EncodeToString([]byte(v)))
		add(base64.URLEncoding.EncodeToString([]byte(v)))
		add(base64.RawURLEncoding.EncodeToString([]byte(v)))
		add(hex.EncodeToString([]byte(v)))
		add(strings.ToUpper(hex.EncodeToString([]byte(v))))
		add(url.QueryEscape(v))
		add(url.PathEscape(v))
		if b, err := json.Marshal(v); err == nil {
			add(strings.Trim(string(b), `"`))
		}
	}
	return out
}
