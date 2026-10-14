package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// AspectName names one kind of curation written on a catalog entity.
type AspectName string

// The aspects. Each is stored, versioned and rolled back on its own, so writing
// a dataset's tags never touches its description.
const (
	AspectDescription          AspectName = "description"
	AspectTags                 AspectName = "tags"
	AspectGlossaryTerms        AspectName = "glossary_terms"
	AspectOwners               AspectName = "owners"
	AspectDomain               AspectName = "domain"
	AspectCustomProperties     AspectName = "custom_properties"
	AspectStructuredProperties AspectName = "structured_properties"
	AspectDocumentationLinks   AspectName = "documentation_links"
	AspectCuratedQueries       AspectName = "curated_queries"
	AspectIncidents            AspectName = "incidents"
	AspectContextDocuments     AspectName = "context_documents"
)

// ErrAspectNotAllowed is returned for an aspect the entity's kind does not carry.
var ErrAspectNotAllowed = errors.New("aspect not allowed on this entity")

// ErrInvalidAspect is returned for an aspect value that does not decode into
// the aspect's shape.
var ErrInvalidAspect = errors.New("invalid aspect value")

// Aspects is the curation on one entity. An aspect never written is its zero
// value and is omitted from JSON.
//
// GlossaryTerms and Domain hold references (mcp:glossary_term:<id>,
// mcp:domain:<id>); Tags holds tag names.
type Aspects struct {
	Description          string            `json:"description,omitempty"`
	Tags                 []string          `json:"tags,omitempty"`
	GlossaryTerms        []string          `json:"glossary_terms,omitempty"`
	Owners               []Owner           `json:"owners,omitempty"`
	Domain               string            `json:"domain,omitempty"`
	CustomProperties     map[string]string `json:"custom_properties,omitempty"`
	StructuredProperties map[string][]any  `json:"structured_properties,omitempty"`
	DocumentationLinks   []Link            `json:"documentation_links,omitempty"`
	CuratedQueries       []CuratedQuery    `json:"curated_queries,omitempty"`
	Incidents            []Incident        `json:"incidents,omitempty"`
	ContextDocuments     []ContextDocument `json:"context_documents,omitempty"`
}

// sharedAspects are carried by a dataset and a column alike.
var sharedAspects = []AspectName{
	AspectDescription, AspectTags, AspectGlossaryTerms, AspectOwners, AspectDomain,
	AspectCustomProperties, AspectStructuredProperties, AspectDocumentationLinks,
}

// datasetOnlyAspects are carried by a dataset and by nothing else.
var datasetOnlyAspects = []AspectName{AspectCuratedQueries, AspectIncidents, AspectContextDocuments}

// definitionAspects are what a glossary term, glossary node, domain, data
// product or tag carries: what it means, who answers for it, and where to read
// more.
var definitionAspects = []AspectName{
	AspectDescription, AspectOwners, AspectCustomProperties, AspectDocumentationLinks,
}

// AspectsFor returns the aspects an entity of kind carries, in a stable order.
func AspectsFor(kind Kind) []AspectName {
	switch kind {
	case KindDataset:
		return append(append([]AspectName{}, sharedAspects...), datasetOnlyAspects...)
	case KindColumn:
		return append([]AspectName{}, sharedAspects...)
	case KindGlossaryTerm, KindGlossaryNode, KindTag, KindDomain, KindDataProduct:
		return append([]AspectName{}, definitionAspects...)
	default:
		return nil
	}
}

// Allows reports whether an entity of kind carries the aspect.
func Allows(kind Kind, name AspectName) bool {
	return slices.Contains(AspectsFor(kind), name)
}

// aspectFields maps each aspect's name to where its value lives in Aspects.
// It is the one mapping between the two.
var aspectFields = map[AspectName]func(*Aspects) any{
	AspectDescription:          func(a *Aspects) any { return &a.Description },
	AspectTags:                 func(a *Aspects) any { return &a.Tags },
	AspectGlossaryTerms:        func(a *Aspects) any { return &a.GlossaryTerms },
	AspectOwners:               func(a *Aspects) any { return &a.Owners },
	AspectDomain:               func(a *Aspects) any { return &a.Domain },
	AspectCustomProperties:     func(a *Aspects) any { return &a.CustomProperties },
	AspectStructuredProperties: func(a *Aspects) any { return &a.StructuredProperties },
	AspectDocumentationLinks:   func(a *Aspects) any { return &a.DocumentationLinks },
	AspectCuratedQueries:       func(a *Aspects) any { return &a.CuratedQueries },
	AspectIncidents:            func(a *Aspects) any { return &a.Incidents },
	AspectContextDocuments:     func(a *Aspects) any { return &a.ContextDocuments },
}

// field returns a pointer to the Aspects field that holds name, or nil for a
// name that is not an aspect.
func (a *Aspects) field(name AspectName) any {
	f, ok := aspectFields[name]
	if !ok {
		return nil
	}
	return f(a)
}

// Set decodes a stored or written value into the named aspect.
func (a *Aspects) Set(name AspectName, value json.RawMessage) error {
	dst := a.field(name)
	if dst == nil {
		return fmt.Errorf("unknown aspect %q: %w", name, ErrInvalidAspect)
	}
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decoding %s: %w: %w", name, err, ErrInvalidAspect)
	}
	return nil
}

// NormalizeAspect checks a value written to an aspect of an entity of kind and
// returns its canonical JSON, the form the store keeps. A JSON null clears the
// aspect and is returned as nil.
func NormalizeAspect(kind Kind, name AspectName, value json.RawMessage) (json.RawMessage, error) {
	if !Allows(kind, name) {
		return nil, fmt.Errorf("aspect %s on a %s: %w", name, kind, ErrAspectNotAllowed)
	}
	if v := bytes.TrimSpace(value); len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return nil, nil
	}
	var a Aspects
	if err := a.Set(name, value); err != nil {
		return nil, err
	}
	if check, ok := aspectChecks[name]; ok {
		if err := check(&a); err != nil {
			return nil, fmt.Errorf("checking %s: %w: %w", name, err, ErrInvalidAspect)
		}
	}
	out, err := json.Marshal(a.field(name))
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", name, err)
	}
	return out, nil
}

// aspectChecks are the rules a well-formed value must meet beyond its shape:
// the references an aspect holds name the right kind, and the fields an entry
// is identified by are present. An aspect with no entry needs only its shape.
var aspectChecks = map[AspectName]func(*Aspects) error{
	AspectGlossaryTerms: func(a *Aspects) error { return checkRefs(a.GlossaryTerms, KindGlossaryTerm) },
	AspectDomain:        func(a *Aspects) error { return checkRefs([]string{a.Domain}, KindDomain) },
	AspectTags:          func(a *Aspects) error { return nonEmpty("tag", a.Tags) },
	AspectOwners: func(a *Aspects) error {
		return each(a.Owners, "an owner needs an owner", func(o Owner) bool { return blank(o.Owner) })
	},
	AspectDocumentationLinks: func(a *Aspects) error {
		return each(a.DocumentationLinks, "a link needs a url", func(l Link) bool { return blank(l.URL) })
	},
	AspectCuratedQueries: func(a *Aspects) error {
		return each(a.CuratedQueries, "a curated query needs a name and sql",
			func(q CuratedQuery) bool { return blank(q.Name) || blank(q.SQL) })
	},
	AspectIncidents: func(a *Aspects) error { return checkIncidents(a.Incidents) },
	AspectContextDocuments: func(a *Aspects) error {
		return each(a.ContextDocuments, "a context document needs an id and a title",
			func(d ContextDocument) bool { return blank(d.ID) || blank(d.Title) })
	},
}

// each refuses the list with msg when any entry is missing what bad reports.
func each[T any](entries []T, msg string, bad func(T) bool) error {
	if slices.ContainsFunc(entries, bad) {
		return errors.New(msg)
	}
	return nil
}

// blank reports whether s holds nothing but whitespace.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// checkRefs requires every value to be a reference of the given kind.
func checkRefs(values []string, kind Kind) error {
	for _, v := range values {
		r, err := ParseRef(v)
		if err != nil {
			return err
		}
		if r.Kind != kind {
			return fmt.Errorf("reference %q is a %s, want a %s", v, r.Kind, kind)
		}
	}
	return nil
}

// nonEmpty refuses a blank entry in a list of names.
func nonEmpty(what string, values []string) error {
	if slices.ContainsFunc(values, blank) {
		return fmt.Errorf("empty %s", what)
	}
	return nil
}

// checkIncidents requires an id, a title and a known state on each incident.
func checkIncidents(incidents []Incident) error {
	for _, in := range incidents {
		if blank(in.ID) || blank(in.Title) {
			return errors.New("an incident needs an id and a title")
		}
		if in.State != IncidentActive && in.State != IncidentResolved {
			return fmt.Errorf("incident state %q, want %s or %s", in.State, IncidentActive, IncidentResolved)
		}
	}
	return nil
}

// WithText returns a copy of the curation with every free-text value people
// and agents write passed through fn: descriptions, owners, property values,
// link and query descriptions, incident text and context documents. Names
// and references (tags, glossary terms, the domain, a query's SQL) are kept
// as they are. A reader that hands curation to a model scrubs it this way.
func (a Aspects) WithText(fn func(string) string) Aspects {
	out := a
	out.Description = fn(a.Description)
	out.Owners = mapSlice(a.Owners, func(o Owner) Owner { o.Owner = fn(o.Owner); return o })
	if a.CustomProperties != nil {
		out.CustomProperties = make(map[string]string, len(a.CustomProperties))
		for k, v := range a.CustomProperties {
			out.CustomProperties[k] = fn(v)
		}
	}
	if a.StructuredProperties != nil {
		out.StructuredProperties = make(map[string][]any, len(a.StructuredProperties))
		for k, vs := range a.StructuredProperties {
			out.StructuredProperties[k] = mapSlice(vs, func(v any) any {
				if s, ok := v.(string); ok {
					return fn(s)
				}
				return v
			})
		}
	}
	out.DocumentationLinks = mapSlice(a.DocumentationLinks, func(l Link) Link { l.Description = fn(l.Description); return l })
	out.CuratedQueries = mapSlice(a.CuratedQueries, func(q CuratedQuery) CuratedQuery {
		q.Name, q.Description = fn(q.Name), fn(q.Description)
		return q
	})
	out.Incidents = mapSlice(a.Incidents, func(in Incident) Incident {
		in.Title, in.Description, in.Resolution = fn(in.Title), fn(in.Description), fn(in.Resolution)
		return in
	})
	out.ContextDocuments = mapSlice(a.ContextDocuments, func(d ContextDocument) ContextDocument {
		d.Title, d.Content, d.Category = fn(d.Title), fn(d.Content), fn(d.Category)
		return d
	})
	return out
}

// mapSlice applies fn to each element of a copy of s; nil stays nil.
func mapSlice[T any](s []T, fn func(T) T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	for i, v := range s {
		out[i] = fn(v)
	}
	return out
}
