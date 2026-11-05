// Package apichange keeps an API catalog's specs current and says what
// changed (#2469): a spec's upstream checked on a schedule or on request, the
// difference from the copy in use as a report of operations added, removed,
// changed and newly deprecated (#2483), an uploaded or pasted spec compared
// with an upstream it is never replaced by (#2484), which scripts, prompts and
// endpoints use each operation (#2485), and what upstreams say about an
// operation in their answers: Deprecation, Sunset, and a removal (#2486).
//
// Everything read from an upstream is untrusted text: a spec's operation ids,
// summaries and version, and a header's value, are cleaned and bounded before
// they are stored or sent.
package apichange

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/untrusted"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// The parts of an operation a change report names as changed.
const (
	PartParameters  = "parameters"
	PartRequestBody = "request_body"
	PartResponses   = "responses"
)

// maxListed bounds each list a report carries; a spec that changed more says
// so with Truncated.
const maxListed = 500

// maxText bounds an operation id, path or summary read from a spec.
const maxText = 200

// Op is one operation as a report names it. ID is the spec's operationId, or
// "METHOD path" when the spec gives none, which is also how a script and a
// warning address it. UsedBy is filled when the report is written.
type Op struct {
	ID      string   `json:"id"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Summary string   `json:"summary,omitempty"`
	UsedBy  []Caller `json:"used_by,omitempty"`
}

// Changed is an operation present in both versions whose contract moved.
type Changed struct {
	Op
	Parts []string `json:"parts"`
}

// Report is what one version of a spec changed from the one before it.
type Report struct {
	FromVersion string    `json:"from_version,omitempty"`
	ToVersion   string    `json:"to_version,omitempty"`
	Added       []Op      `json:"added"`
	Removed     []Op      `json:"removed"`
	Changed     []Changed `json:"changed"`
	Deprecated  []Op      `json:"deprecated"`
	// Truncated is true when a list was cut at its bound.
	Truncated bool `json:"truncated,omitempty"`
}

// Empty reports whether the two versions differ in nothing a report names:
// no operation and no info.version. A formatting change is empty.
func (r Report) Empty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0 &&
		len(r.Deprecated) == 0 && r.FromVersion == r.ToVersion
}

// Counts is the report in one line: "3 added, 1 removed, 2 changed, 1
// deprecated", with the version change when there is one.
func (r Report) Counts() string {
	counts := fmt.Sprintf("%d added, %d removed, %d changed, %d deprecated",
		len(r.Added), len(r.Removed), len(r.Changed), len(r.Deprecated))
	if r.FromVersion == r.ToVersion {
		return counts
	}
	return fmt.Sprintf("%s; version %s to %s", counts, orNone(r.FromVersion), orNone(r.ToVersion))
}

func orNone(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

// operation is one operation of a parsed spec with its fingerprints.
type operation struct {
	op         Op
	deprecated bool
	parts      map[string]string
}

// Compare reports what next changed from prev. Both are OpenAPI documents,
// the effective content a spec serves; either failing to parse is an error.
func Compare(prev, next string) (Report, error) {
	before, beforeVersion, err := operationsOf(prev)
	if err != nil {
		return Report{}, fmt.Errorf("reading the copy in use: %w", err)
	}
	after, afterVersion, err := operationsOf(next)
	if err != nil {
		return Report{}, fmt.Errorf("reading the fetched spec: %w", err)
	}
	r := Report{
		FromVersion: beforeVersion, ToVersion: afterVersion,
		Added: []Op{}, Removed: []Op{}, Changed: []Changed{}, Deprecated: []Op{},
	}
	for _, id := range sortedKeys(after) {
		r.compareOne(before, after[id])
	}
	for _, id := range sortedKeys(before) {
		if _, ok := after[id]; !ok {
			r.Removed = append(r.Removed, before[id].op)
		}
	}
	r.truncate()
	return r, nil
}

// compareOne files a, an operation of the fetched spec, against the
// operation of the same id in before.
func (r *Report) compareOne(before map[string]operation, a operation) {
	b, ok := before[a.op.ID]
	if !ok {
		r.Added = append(r.Added, a.op)
		return
	}
	if parts := changedParts(b.parts, a.parts); len(parts) > 0 {
		r.Changed = append(r.Changed, Changed{Op: a.op, Parts: parts})
	}
	if a.deprecated && !b.deprecated {
		r.Deprecated = append(r.Deprecated, a.op)
	}
}

// truncate cuts every list at maxListed.
func (r *Report) truncate() {
	cut := func(n int) int {
		if n > maxListed {
			r.Truncated = true
			return maxListed
		}
		return n
	}
	r.Added = r.Added[:cut(len(r.Added))]
	r.Removed = r.Removed[:cut(len(r.Removed))]
	r.Changed = r.Changed[:cut(len(r.Changed))]
	r.Deprecated = r.Deprecated[:cut(len(r.Deprecated))]
}

func changedParts(before, after map[string]string) []string {
	var parts []string
	for _, p := range []string{PartParameters, PartRequestBody, PartResponses} {
		if before[p] != after[p] {
			parts = append(parts, p)
		}
	}
	return parts
}

func sortedKeys(m map[string]operation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// operationsOf parses raw and returns its operations keyed by Op.ID, and its
// info.version.
func operationsOf(raw string) (ops map[string]operation, version string, err error) {
	doc, err := apicatalog.ParseSpec(raw)
	if err != nil {
		return nil, "", err //nolint:wrapcheck // the parser names the defect
	}
	if doc.Info != nil {
		version = clean(doc.Info.Version)
	}
	ops = map[string]operation{}
	if doc.Paths == nil {
		return ops, version, nil
	}
	for path, item := range doc.Paths.Map() {
		if item == nil {
			continue
		}
		for method, op := range item.Operations() {
			o := describe(strings.ToUpper(method), path, item, op)
			ops[o.op.ID] = o
		}
	}
	return ops, version, nil
}

// describe is one operation with the fingerprints of its contract.
func describe(method, path string, item *openapi3.PathItem, op *openapi3.Operation) operation {
	id := clean(op.OperationID)
	if id == "" {
		id = method + " " + clean(path)
	}
	return operation{
		op:         Op{ID: id, Method: method, Path: clean(path), Summary: clean(op.Summary)},
		deprecated: op.Deprecated,
		parts: map[string]string{
			PartParameters:  paramsPrint(item.Parameters, op.Parameters),
			PartRequestBody: bodyPrint(op.RequestBody),
			PartResponses:   responsesPrint(op.Responses),
		},
	}
}

// clean is a spec's text as a report may carry it: one line, no invisible or
// control runes, bounded.
func clean(s string) string {
	s = untrusted.Line(s)
	if len(s) > maxText {
		s = strings.ToValidUTF8(s[:maxText], "")
	}
	return s
}

// paramsPrint fingerprints the parameters an operation takes, its own over
// the path item's: each one's location, name, whether it is required, and its
// schema.
func paramsPrint(pathLevel, opLevel openapi3.Parameters) string {
	byKey := map[string]string{}
	for _, list := range []openapi3.Parameters{pathLevel, opLevel} {
		for _, ref := range list {
			if ref == nil || ref.Value == nil {
				continue
			}
			p := ref.Value
			byKey[p.In+":"+p.Name] = strconv.FormatBool(p.Required) + ":" + schemaPrint(p.Schema, map[*openapi3.Schema]bool{})
		}
	}
	return joinSorted(byKey)
}

// bodyPrint fingerprints a request body: whether it is required and each
// media type's schema.
func bodyPrint(ref *openapi3.RequestBodyRef) string {
	if ref == nil || ref.Value == nil {
		return ""
	}
	return strconv.FormatBool(ref.Value.Required) + "|" + contentPrint(ref.Value.Content)
}

// responsesPrint fingerprints each response status's media types and schemas.
func responsesPrint(resp *openapi3.Responses) string {
	if resp == nil {
		return ""
	}
	byStatus := map[string]string{}
	for status, ref := range resp.Map() {
		if ref == nil || ref.Value == nil {
			byStatus[status] = ""
			continue
		}
		byStatus[status] = contentPrint(ref.Value.Content)
	}
	return joinSorted(byStatus)
}

func contentPrint(content openapi3.Content) string {
	byType := map[string]string{}
	for mt, media := range content {
		if media == nil {
			byType[mt] = ""
			continue
		}
		byType[mt] = schemaPrint(media.Schema, map[*openapi3.Schema]bool{})
	}
	return joinSorted(byType)
}

// schemaPrint fingerprints a schema through its references: the parts that
// decide what a caller sends or reads (type, format, enum, required,
// nullability, properties, items, and the compositions). A schema met again
// on the way down prints as a cycle marker, so a recursive schema ends.
func schemaPrint(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) string {
	if ref == nil || ref.Value == nil {
		return ""
	}
	s := ref.Value
	if seen[s] {
		return "<cycle>"
	}
	seen[s] = true
	defer delete(seen, s)

	parts := []string{"t=" + sortedJoin(typeNames(s.Type)), "f=" + s.Format}
	if len(s.Enum) > 0 {
		enum, _ := json.Marshal(s.Enum)
		parts = append(parts, "e="+string(enum))
	}
	if s.Nullable {
		parts = append(parts, "n")
	}
	if len(s.Required) > 0 {
		parts = append(parts, "r="+sortedJoin(s.Required))
	}
	if len(s.Properties) > 0 {
		props := map[string]string{}
		for name, p := range s.Properties {
			props[name] = schemaPrint(p, seen)
		}
		parts = append(parts, "p="+joinSorted(props))
	}
	if s.Items != nil {
		parts = append(parts, "i="+schemaPrint(s.Items, seen))
	}
	parts = append(parts, compositionsPrint(s, seen)...)
	return "{" + strings.Join(parts, ";") + "}"
}

// compositionsPrint fingerprints allOf, oneOf and anyOf, each in its own
// order-free form.
func compositionsPrint(s *openapi3.Schema, seen map[*openapi3.Schema]bool) []string {
	var out []string
	for _, c := range []struct {
		label string
		list  openapi3.SchemaRefs
	}{{"all", s.AllOf}, {"one", s.OneOf}, {"any", s.AnyOf}} {
		if len(c.list) == 0 {
			continue
		}
		prints := make([]string, 0, len(c.list))
		for _, r := range c.list {
			prints = append(prints, schemaPrint(r, seen))
		}
		out = append(out, c.label+"="+sortedJoin(prints))
	}
	return out
}

func typeNames(t *openapi3.Types) []string {
	if t == nil {
		return nil
	}
	return *t
}

// sortedJoin is list sorted and joined by commas, leaving list as it was.
func sortedJoin(list []string) string {
	c := slices.Clone(list)
	slices.Sort(c)
	return strings.Join(c, ",")
}

// joinSorted renders a map in key order, so equal maps print equal.
func joinSorted(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, cmp.Compare[string])
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+m[k]+";")
	}
	return strings.Join(pairs, "")
}
