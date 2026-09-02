package knowledge

import (
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// urnOnlyDataset is the record DataHub returns for a dataset URN it has never
// ingested, as measured against a live catalog: the URN echoed back, a name and
// a type and a platform read out of the URN's key aspect, and a schema that is
// present but has no fields because the upstream read parses one out of every
// response. Nothing here is evidence of an entity.
func urnOnlyDataset() *semantic.Dataset {
	return &semantic.Dataset{
		TableContext: semantic.TableContext{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.gone,PROD)"},
		Name:         "warehouse.public.gone",
		Type:         "DATASET",
		Platform:     "trino",
		Schema:       &semantic.DatasetSchema{},
	}
}

// TestDatasetExistsRejectsTheURNOnlyRecord pins the shape of the stub itself:
// none of the fields the catalog synthesizes from the reference may count as
// evidence, or every dangling citation resolves as a hit (#1605).
func TestDatasetExistsRejectsTheURNOnlyRecord(t *testing.T) {
	if datasetExists(urnOnlyDataset()) {
		t.Error("a record carrying only what its URN supplies was read as an existing dataset")
	}
	// The name, type and platform of a real dataset are the same fields the stub
	// carries, so a rule that counted them would be no rule at all.
	stub := urnOnlyDataset()
	stub.Name, stub.Type, stub.Platform = "renamed", "TABLE", "hive"
	if datasetExists(stub) {
		t.Error("a URN-derived name, type or platform was counted as evidence the dataset exists")
	}
}

// TestDatasetExistsAcceptsAnyOwnAspect walks the inventory one aspect at a
// time. Each case is a dataset that carries exactly one thing the reference did
// not supply, and each must resolve: an entity documented only by its owners,
// or only by a tag, is still an entity.
func TestDatasetExistsAcceptsAnyOwnAspect(t *testing.T) {
	now := time.Now()
	score := 0.9
	for name, carry := range map[string]func(*semantic.Dataset){
		"description": func(d *semantic.Dataset) { d.Description = "one row per order" },
		"owners":      func(d *semantic.Dataset) { d.Owners = []semantic.Owner{{URN: "urn:li:corpuser:ada"}} },
		"tags":        func(d *semantic.Dataset) { d.Tags = []string{"pii"} },
		"tag refs":    func(d *semantic.Dataset) { d.TagRefs = []semantic.EntityRef{{URN: "urn:li:tag:pii"}} },
		"glossary terms": func(d *semantic.Dataset) {
			d.GlossaryTerms = []semantic.GlossaryTerm{{URN: "urn:li:glossaryTerm:Revenue"}}
		},
		"domain":            func(d *semantic.Dataset) { d.Domain = &semantic.Domain{URN: "urn:li:domain:retail"} },
		"deprecation":       func(d *semantic.Dataset) { d.Deprecation = &semantic.Deprecation{Deprecated: true} },
		"quality score":     func(d *semantic.Dataset) { d.QualityScore = &score },
		"custom properties": func(d *semantic.Dataset) { d.CustomProperties = map[string]string{"scd": "type 2"} },
		"last modified":     func(d *semantic.Dataset) { d.LastModified = &now },
		"structured properties": func(d *semantic.Dataset) {
			d.StructuredProperties = []semantic.StructuredProperty{{QualifiedName: "acme.tier"}}
		},
		"active incidents": func(d *semantic.Dataset) { d.ActiveIncidents = 1 },
		"incidents":        func(d *semantic.Dataset) { d.Incidents = []semantic.Incident{{URN: "urn:li:incident:1"}} },
		"data contract":    func(d *semantic.Dataset) { d.DataContract = &semantic.DataContractStatus{} },
		"sub types":        func(d *semantic.Dataset) { d.SubTypes = []string{"table"} },
		"created":          func(d *semantic.Dataset) { d.Created = &now },
		"schema fields": func(d *semantic.Dataset) {
			d.Schema = &semantic.DatasetSchema{Fields: []semantic.SchemaField{{FieldPath: "id"}}}
		},
		"queries":           func(d *semantic.Dataset) { d.Queries = []semantic.SavedQuery{{Name: "daily"}} },
		"query count":       func(d *semantic.Dataset) { d.TotalQueries = 3 },
		"related documents": func(d *semantic.Dataset) { d.RelatedDocuments = []semantic.DocumentResult{{URN: "urn:li:document:1"}} },
	} {
		t.Run(name, func(t *testing.T) {
			ds := urnOnlyDataset()
			carry(ds)
			if !datasetExists(ds) {
				t.Errorf("a dataset documented by its %s was reported as not existing", name)
			}
		})
	}
}

// TestDatasetExistsWillNotCallAPartialReadAbsent covers the one case where the
// rule must abstain: a read that could not serve part of the record has not
// seen enough to conclude the entity is missing.
func TestDatasetExistsWillNotCallAPartialReadAbsent(t *testing.T) {
	ds := urnOnlyDataset()
	ds.Unavailable = []string{"schema"}
	if !datasetExists(ds) {
		t.Error("a partial catalog read was treated as proof the dataset does not exist")
	}
}

// TestDatasetExistsIgnoresAFieldlessSchema is the trap this rule fell into
// first: the upstream read parses a schema out of every response, so a non-nil
// schema is present on a stub and only its fields are evidence.
func TestDatasetExistsIgnoresAFieldlessSchema(t *testing.T) {
	ds := urnOnlyDataset()
	ds.Schema = &semantic.DatasetSchema{Version: 2}
	if datasetExists(ds) {
		t.Error("a schema with no fields was counted as evidence the dataset exists")
	}
}

// TestDataProductExists pins the product arm. Its name does count, because the
// upstream read takes a product's name from its properties aspect and nowhere
// else, so a product that does not exist arrives unnamed.
func TestDataProductExists(t *testing.T) {
	stub := &semantic.DataProduct{URN: "urn:li:dataProduct:gone"}
	if dataProductExists(stub) {
		t.Error("a product carrying only its URN was read as an existing product")
	}
	for name, carry := range map[string]func(*semantic.DataProduct){
		"name":              func(p *semantic.DataProduct) { p.Name = "Retail 360" },
		"description":       func(p *semantic.DataProduct) { p.Description = "everything about a retail day" },
		"domain":            func(p *semantic.DataProduct) { p.Domain = &semantic.Domain{URN: "urn:li:domain:retail"} },
		"owners":            func(p *semantic.DataProduct) { p.Owners = []semantic.Owner{{URN: "urn:li:corpuser:ada"}} },
		"assets":            func(p *semantic.DataProduct) { p.Assets = []semantic.EntityRef{{URN: "urn:li:dataset:x"}} },
		"custom properties": func(p *semantic.DataProduct) { p.CustomProperties = map[string]string{"tier": "gold"} },
	} {
		t.Run(name, func(t *testing.T) {
			p := &semantic.DataProduct{URN: "urn:li:dataProduct:retail-360"}
			carry(p)
			if !dataProductExists(p) {
				t.Errorf("a product documented by its %s was reported as not existing", name)
			}
		})
	}
}

// TestGlossaryTermExists pins the term arm, including the field it must not
// read: a term's name is its URN's id segment on a stub and its properties name
// on a real term, so the two are indistinguishable and the name is no evidence.
func TestGlossaryTermExists(t *testing.T) {
	stub := &semantic.GlossaryTerm{URN: "urn:li:glossaryTerm:Gone", Name: "Gone"}
	if glossaryTermExists(stub) {
		t.Error("a term carrying only its URN was read as an existing term")
	}
	for name, carry := range map[string]func(*semantic.GlossaryTerm){
		"description":       func(g *semantic.GlossaryTerm) { g.Description = "revenue net of refunds" },
		"parent node":       func(g *semantic.GlossaryTerm) { g.ParentNode = "urn:li:glossaryNode:Finance" },
		"owners":            func(g *semantic.GlossaryTerm) { g.Owners = []semantic.Owner{{URN: "urn:li:corpuser:ada"}} },
		"custom properties": func(g *semantic.GlossaryTerm) { g.CustomProperties = map[string]string{"source": "finance"} },
	} {
		t.Run(name, func(t *testing.T) {
			term := &semantic.GlossaryTerm{URN: "urn:li:glossaryTerm:NetRevenue", Name: "Net Revenue"}
			carry(term)
			if !glossaryTermExists(term) {
				t.Errorf("a term documented by its %s was reported as not existing", name)
			}
		})
	}
}

// TestTableContextExists pins the layer the search entity arm and the
// context-only fetch fallback read: the same rule over a dataset's business
// context alone, where a URN is all a stub carries.
func TestTableContextExists(t *testing.T) {
	if tableContextExists(&semantic.TableContext{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.gone,PROD)"}) {
		t.Error("a context carrying only its URN was read as an existing entity")
	}
	if !tableContextExists(&semantic.TableContext{Tags: []string{"pii"}}) {
		t.Error("an entity documented by a tag alone was reported as not existing")
	}
}
