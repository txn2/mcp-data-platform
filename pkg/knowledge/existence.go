package knowledge

import "github.com/txn2/mcp-data-platform/pkg/semantic"

// A catalog read cannot settle existence by the absence of an error. DataHub
// hydrates a stub from an entity's key aspect for a URN it has never ingested:
// it echoes the URN back, synthesizes the fields the URN itself encodes, and
// returns every real aspect as null, with no GraphQL error. The upstream
// client's not-found test (an empty URN in the response) therefore never fires,
// and a dangling citation resolved as a hit whose every field the caller had
// supplied in the reference (#1605, filed upstream as txn2/mcp-datahub#204).
//
// Existence needs a positive signal instead, and the signal is the record
// carrying something the reference did not supply. carriesOwnContent is that
// one rule, shared by every arm that resolves a catalog record by URN: the
// dataset, data product and glossary term arms of fetch, and the entity arm of
// search. Each passes the inventory of aspects its own record type holds only
// when the entity exists. The tag and domain arms need no rule: they resolve by
// enumerating the vocabulary and matching the URN, which is positive by
// construction.
//
// An inventory must never list a field derived from the URN, since those are
// identical on a hit and a miss, and must list every aspect a real entity can be
// documented by, so a sparsely documented one still resolves on whatever it does
// carry.
//
// This is a heuristic because the exact signal is not reachable from here:
// DataHub exposes `exists` on a dataset and a glossary term but not on a data
// product, and only inside the concrete type's fragment, so reading it means
// changing the upstream queries. Once #204 lands the catalog reports the miss
// itself and this rule stops firing, staying as the fallback for DataHub
// versions that omit `exists`.
func carriesOwnContent(aspects ...bool) bool {
	for _, present := range aspects {
		if present {
			return true
		}
	}
	return false
}

// datasetExists reports whether a dataset record is the catalog's own rather
// than a stub built from the reference. Name, Type and Platform are excluded
// deliberately: all three are read out of the URN's key aspect and are present
// on a miss.
//
// A partial read is never called absent. When the catalog could not serve part
// of the record it names the part in Unavailable, and that part is empty for
// the same reason a stub's is, so the read has not seen enough to conclude
// anything.
//
// The declared schema counts by its fields rather than by its presence. The
// upstream read parses a schema out of every response, so a dataset that has no
// schema and a URN that has no dataset both arrive carrying a non-nil,
// field-less one (mcp-datahub GetSchema): the pointer is always there and only
// its contents are evidence.
func datasetExists(ds *semantic.Dataset) bool {
	if len(ds.Unavailable) > 0 {
		return true
	}
	return tableContextExists(&ds.TableContext) || carriesOwnContent(
		len(ds.SubTypes) > 0,
		ds.Created != nil,
		ds.Schema != nil && len(ds.Schema.Fields) > 0,
		len(ds.Queries) > 0,
		ds.TotalQueries > 0,
		len(ds.RelatedDocuments) > 0,
	)
}

// tableContextExists is the same rule over a dataset's business context alone.
// It is its own function because the context is what the entity arm of search
// reads (searchByEntity) and what a fetch falls back to when no full-record read
// is wired, and a stub reaches both by the same route it reaches the full read.
func tableContextExists(tc *semantic.TableContext) bool {
	return carriesOwnContent(
		tc.Description != "",
		len(tc.Owners) > 0,
		len(tc.Tags) > 0,
		len(tc.TagRefs) > 0,
		len(tc.GlossaryTerms) > 0,
		tc.Domain != nil,
		tc.Deprecation != nil,
		tc.QualityScore != nil,
		len(tc.CustomProperties) > 0,
		tc.LastModified != nil,
		len(tc.StructuredProperties) > 0,
		tc.ActiveIncidents > 0,
		len(tc.Incidents) > 0,
		tc.DataContract != nil,
	)
}

// dataProductExists reports whether a data product record is the catalog's own.
// Name counts here, unlike on the other two arms: the upstream read takes a
// product's name from its properties aspect and nowhere else, so a product that
// does not exist arrives unnamed.
func dataProductExists(p *semantic.DataProduct) bool {
	return carriesOwnContent(
		p.Name != "",
		p.Description != "",
		p.Domain != nil,
		len(p.Owners) > 0,
		len(p.Assets) > 0,
		len(p.CustomProperties) > 0,
	)
}

// glossaryTermExists reports whether a term record is the vocabulary's own.
// Name is excluded because both cases arrive in the same field: the upstream
// read takes the term's properties name when there is one and the key-derived
// name otherwise, so a stub and a real term are indistinguishable by name here.
//
// This leaves one residual gap, the narrowest the rule can: a term that exists,
// sits at the glossary root, and has no definition, no steward and no properties
// reports as not found. That term is also the one a reader learns nothing from,
// so absent is the safer of the two wrong answers. txn2/mcp-datahub#204 closes
// it properly.
func glossaryTermExists(t *semantic.GlossaryTerm) bool {
	return carriesOwnContent(
		t.Description != "",
		t.ParentNode != "",
		len(t.Owners) > 0,
		len(t.CustomProperties) > 0,
	)
}
