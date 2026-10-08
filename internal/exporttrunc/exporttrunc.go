// Package exporttrunc is what every export tool does when a bound cut the
// result it was writing (#2057): trino_export at a row limit, api_export and
// graphql_export at a page walk's page bound. The tools share one judgment so
// they cannot disagree about when a cut file is refused, what the response
// says, and how the asset it lands in is marked.
//
// A cut the caller asked for (its own limit or max_pages) writes the file and
// flags it. A cut the caller did not ask for (the deployment's cap, or a walk's
// built-in default bound) refuses and writes nothing, unless the caller passes
// on_truncation "warn". Either default is overridden by on_truncation.
package exporttrunc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Tag is the reserved tag an asset carries while its current version is a
// truncated export. The portal version store adds and removes it with the
// version, so a complete version written later, by any path, clears it.
const Tag = "_sys-truncated"

// Where the bound that cut a result came from.
const (
	// SourceRequest is a bound the caller set: trino_export's limit, a page
	// walk's max_pages.
	SourceRequest = "request"
	// SourceDeployment is a bound the caller did not set: the deployment's
	// row cap, or a page walk's built-in default page bound.
	SourceDeployment = "deployment"
)

// What a bound counts.
const (
	UnitRows  = "rows"
	UnitPages = "pages"
)

// on_truncation values.
const (
	// PolicyFail refuses a cut result and writes nothing.
	PolicyFail = "fail"
	// PolicyWarn writes a cut result and flags it.
	PolicyWarn = "warn"
)

// The metadata keys a truncated version and its asset carry. The portal
// version store strips them from an asset whose new version carries no
// metadata, so they only ever describe the content the asset holds now.
const (
	MetaTruncated    = "truncated"
	MetaLimitApplied = "limit_applied"
	MetaLimitSource  = "limit_source"
	MetaLimitUnit    = "limit_unit"
)

// Options is what a caller passes on an export call to decide what a cut or
// an unexpected count does. Each export tool's input embeds it.
type Options struct {
	// OnTruncation is "fail" or "warn"; empty takes the default for the
	// bound's source.
	OnTruncation string `json:"on_truncation,omitempty"`
	// ExpectRows is the exact count the export must write.
	ExpectRows *int `json:"expect_rows,omitempty"`
	// ExpectMinRows is the fewest the export may write.
	ExpectMinRows *int `json:"expect_min_rows,omitempty"`
}

// Validate refuses an on_truncation value outside the enum, a negative
// expectation, and both expectations at once.
func (o Options) Validate() error {
	switch o.OnTruncation {
	case "", PolicyFail, PolicyWarn:
	default:
		return fmt.Errorf("on_truncation must be %q or %q, not %q", PolicyFail, PolicyWarn, o.OnTruncation)
	}
	if o.ExpectRows != nil && o.ExpectMinRows != nil {
		return errors.New("set expect_rows or expect_min_rows, not both")
	}
	if (o.ExpectRows != nil && *o.ExpectRows < 0) || (o.ExpectMinRows != nil && *o.ExpectMinRows < 0) {
		return errors.New("expect_rows and expect_min_rows may not be negative")
	}
	return nil
}

// Expects reports whether the caller set an expectation on the count.
func (o Options) Expects() bool {
	return o.ExpectRows != nil || o.ExpectMinRows != nil
}

// Limit is the bound a result was read under.
type Limit struct {
	// Applied is the bound's value.
	Applied int
	// Source is SourceRequest or SourceDeployment.
	Source string
	// Unit is what Applied counts: UnitRows or UnitPages.
	Unit string
	// Key names what set the bound, for the sentence a cut is reported in:
	// the config key of a deployment cap ("portal.export.max_rows") or the
	// parameter the caller set ("limit").
	Key string
	// Remedy is what the caller can do instead, appended to a refusal. Each
	// tool has its own.
	Remedy string
	// Name is what the bound is called in a sentence. Empty takes the
	// source's: "the requested limit" or "the deployment cap".
	Name string
}

// Report is the fields every export output carries beside its count when a
// bound applied to it.
type Report struct {
	// Truncated reports that the bound cut the result: the source had more
	// past it.
	Truncated    bool   `json:"truncated"`
	LimitApplied int    `json:"limit_applied,omitempty"`
	LimitSource  string `json:"limit_source,omitempty"`
	LimitUnit    string `json:"limit_unit,omitempty"`
	// ArbitrarySubset reports that a cut result came from a statement with no
	// top-level ORDER BY, so which rows it kept is the engine's choice and can
	// differ on the next run.
	ArbitrarySubset bool `json:"arbitrary_subset,omitempty"`
	// ExpectMismatch, under on_truncation "warn", is the expectation the
	// written count missed. A miss without "warn" refuses instead.
	ExpectMismatch string `json:"expect_mismatch,omitempty"`
}

// Judgment is what Judge needs to know about one export.
type Judgment struct {
	Limit Limit
	// Truncated is the source's own signal that the bound cut the result.
	Truncated bool
	// Written is the count the export would write: rows, or items merged.
	Written int
	// WrittenUnit names Written in a sentence ("rows", "items").
	WrittenUnit string
	Options     Options
	// Unordered marks a result whose order the statement does not fix (a
	// query with no top-level ORDER BY). Only a row export knows this; a page
	// walk leaves it false.
	Unordered bool
}

// Outcome is what an export does with its result.
type Outcome struct {
	Report Report
	// Refusal, when set, is the error the call returns instead of writing.
	Refusal string
	// Note is what the success message says beyond the count. Empty for a
	// complete result nothing else is said about.
	Note string
}

// Judge decides whether an export writes its result, and what it reports.
func Judge(j Judgment) Outcome {
	lim := j.Limit
	out := Outcome{Report: Report{
		Truncated: j.Truncated, LimitApplied: lim.Applied, LimitSource: lim.Source, LimitUnit: lim.Unit,
	}}
	policy := j.Options.OnTruncation
	var notes []string
	if j.Truncated {
		if policy == "" && lim.Source == SourceDeployment {
			policy = PolicyFail
		}
		if policy == PolicyFail {
			out.Refusal = refusal(lim)
			return out
		}
		notes = append(notes, cutSentence(lim))
	}
	// A limit that did not cut returned every row, which is no subset at all.
	if j.Unordered && j.Truncated {
		out.Report.ArbitrarySubset = true
		notes = append(notes, "The statement has no top-level ORDER BY, so which rows were kept is arbitrary and can differ on the next run.")
	}
	if miss := expectMiss(j.Options, j.Written, j.WrittenUnit); miss != "" {
		if j.Options.OnTruncation != PolicyWarn {
			out.Refusal = miss + "; nothing was written. Set on_truncation to \"warn\" to write it anyway, flagged."
			return out
		}
		out.Report.ExpectMismatch = miss
		notes = append(notes, "Count check failed: "+miss+".")
	}
	out.Note = strings.Join(notes, " ")
	return out
}

// Metadata is what a truncated version and its asset record, or nil for a
// result the bound did not cut.
func (r Report) Metadata() map[string]any {
	if !r.Truncated {
		return nil
	}
	return map[string]any{
		MetaTruncated:    true,
		MetaLimitApplied: r.LimitApplied,
		MetaLimitSource:  r.LimitSource,
		MetaLimitUnit:    r.LimitUnit,
	}
}

// FromMetadata reads a Report back from a version's or an asset's metadata,
// for an answer that names an export written earlier (an idempotency hit). It
// is nil when the metadata records no cut.
func FromMetadata(m map[string]any) *Report {
	if t, _ := m[MetaTruncated].(bool); !t {
		return nil
	}
	r := &Report{Truncated: true, LimitApplied: intValue(m[MetaLimitApplied])}
	r.LimitSource, _ = m[MetaLimitSource].(string)
	r.LimitUnit, _ = m[MetaLimitUnit].(string)
	return r
}

// Carry is the truncation keys of an older version's metadata, for a revert
// that makes that version's content current again, or nil when it records no
// cut.
func Carry(m map[string]any) map[string]any {
	r := FromMetadata(m)
	if r == nil {
		return nil
	}
	return r.Metadata()
}

// WithTag returns tags with Tag added when the report records a cut, and
// unchanged otherwise.
func WithTag(tags []string, r Report) []string {
	if !r.Truncated || slices.Contains(tags, Tag) {
		return tags
	}
	return append(tags, Tag)
}

// cutSentence is how a written, flagged cut is reported.
func cutSentence(l Limit) string {
	return fmt.Sprintf("Truncated at %s of %d %s (%s); %s. This file is incomplete.",
		l.name(), l.Applied, l.Unit, l.Key, evidence(l.Unit))
}

// refusal is the error a cut the caller did not accept is refused with: the
// bound, what set it, the evidence the source had more, and the way out.
func refusal(l Limit) string {
	s := fmt.Sprintf("the result was cut at %s of %d %s (%s): %s, so nothing was written.",
		l.name(), l.Applied, l.Unit, l.Key, evidence(l.Unit))
	if l.Remedy != "" {
		s += " " + l.Remedy
	}
	return s + fmt.Sprintf(" To write the first %d %s anyway, marked incomplete, set on_truncation to \"warn\".", l.Applied, l.Unit)
}

// name is what the bound is called in a sentence.
func (l Limit) name() string {
	switch {
	case l.Name != "":
		return l.Name
	case l.Source == SourceRequest:
		return "the requested limit"
	default:
		return "the deployment cap"
	}
}

// evidence is how the source showed it had more past the bound.
func evidence(unit string) string {
	if unit == UnitPages {
		return "the upstream reported another page"
	}
	return "the query returned more rows"
}

// expectMiss is the expectation the written count misses, or "".
func expectMiss(o Options, written int, unit string) string {
	switch {
	case o.ExpectRows != nil && written != *o.ExpectRows:
		return fmt.Sprintf("expected exactly %d %s, got %d", *o.ExpectRows, unit, written)
	case o.ExpectMinRows != nil && written < *o.ExpectMinRows:
		return fmt.Sprintf("expected at least %d %s, got %d", *o.ExpectMinRows, unit, written)
	default:
		return ""
	}
}

// intValue reads a JSON number, which decoded metadata carries as float64.
func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// thousandsGroup is how many digits a separator groups.
const thousandsGroup = 3

// Thousands renders n with comma separators ("100,000"), the way a tool
// description states a cap to a reader.
func Thousands(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	for i := len(s) - thousandsGroup; i > 0; i -= thousandsGroup {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

// WalkLimit is the bound a page walk ran under: the caller's max_pages, or the
// tool's default bound when the caller set none. remedy is the tool's own way
// out, appended to a refusal.
func WalkLimit(pages int, callerSet bool, remedy string) Limit {
	if callerSet {
		return Limit{Applied: pages, Source: SourceRequest, Unit: UnitPages, Key: "paginate.max_pages"}
	}
	return Limit{
		Applied: pages, Source: SourceDeployment, Unit: UnitPages, Key: "paginate.max_pages unset",
		Remedy: remedy, Name: "the default page bound",
	}
}

// WalkSchemaProperties is the on_truncation, expect_rows and expect_min_rows
// properties of an export whose bound is a page walk's (api_export,
// graphql_export), as JSON Schema property entries to splice into the tool's
// properties object, so the two describe one judgment in one set of words.
const WalkSchemaProperties = `
    "on_truncation": {
      "type": "string",
      "enum": ["fail", "warn"],
      "description": "What a page walk stopped at its page bound, with another page still to fetch, does. fail writes nothing and returns an error; warn writes the pages fetched and flags the response, the asset and its version as truncated. Default: warn when you set paginate.max_pages, fail when the default bound stopped the walk. It also decides whether a missed expect_rows or expect_min_rows fails (the default) or is flagged."
    },
    "expect_rows": {
      "type": "integer",
      "minimum": 0,
      "description": "The exact number of items the page walk must merge. Needs paginate. Any other count fails the call, or is flagged under on_truncation warn. A guard for a recurring export, where a sudden change in count usually means an upstream feed broke."
    },
    "expect_min_rows": {
      "type": "integer",
      "minimum": 0,
      "description": "The fewest items the page walk may merge. Needs paginate. Fewer fails the call, or is flagged under on_truncation warn. Mutually exclusive with expect_rows."
    }`

// ErrExpectNeedsWalk is the refusal for a count expectation on an export that
// is not a page walk: a single response has no item count to hold it to.
var ErrExpectNeedsWalk = errors.New("expect_rows and expect_min_rows count the items a page walk merges; set paginate, or drop them")

// ValidateWalk is Validate for an export whose count is a page walk's
// (api_export, graphql_export): walk says whether the call walks, and a count
// expectation on one that does not is refused.
func (o Options) ValidateWalk(walk bool) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if !walk && o.Expects() {
		return ErrExpectNeedsWalk
	}
	return nil
}

// Sentences joins a message's sentences, dropping the empty ones, so a part
// that said nothing leaves no doubled space.
func Sentences(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}
