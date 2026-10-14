package catalogimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/urnbuild"
)

// Options are the deployment's URN settings the import reads DataHub's
// dataset names by (semantic.urn_mapping).
type Options struct {
	// Platform is the DataHub platform the deployment's Trino datasets were
	// recorded under when it is not "trino".
	Platform string
	// CatalogMapping maps a query-engine catalog name to the name DataHub
	// knows it by; the import reads it backwards.
	CatalogMapping map[string]string
}

// Importer carries a DataHub's curation into the platform catalog.
type Importer struct {
	catalog Catalog
	mapping Mapping
	opts    Options
	now     func() time.Time
}

// New builds an importer.
func New(c Catalog, m Mapping, opts Options) *Importer {
	return &Importer{catalog: c, mapping: m, opts: opts, now: time.Now}
}

// run is one import under way: the mapping as it grows, and the report.
type run struct {
	im      *Importer
	ctx     context.Context
	mapped  map[string]string
	report  *Report
	records map[string]*DatasetRecord
}

// Run imports src into the catalog, then rewrites the DataHub URNs platform
// tables hold into the catalog references the import recorded. A nil rw
// imports only.
func (im *Importer) Run(ctx context.Context, src Source, rw Rewrite) (*Report, error) {
	r, err := im.importAll(ctx, src)
	if err != nil {
		return nil, err
	}
	if rw != nil {
		if err := rw.Rewrite(ctx, r.resolveRef, r.report); err != nil {
			return r.report, fmt.Errorf("rewriting stored references: %w", err)
		}
	}
	return r.report, nil
}

// importAll reads src and writes what it holds into the catalog.
func (im *Importer) importAll(ctx context.Context, src Source) (*run, error) {
	mapped, err := im.mapping.Map(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the import's mapping: %w", err)
	}
	r := &run{im: im, ctx: ctx, mapped: mapped, report: newReport(), records: map[string]*DatasetRecord{}}
	defs, err := src.Definitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading DataHub's definitions: %w", err)
	}
	if err := r.definitions(defs); err != nil {
		return nil, err
	}
	heads, err := src.DatasetHeads(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing DataHub's datasets: %w", err)
	}
	if err := r.datasets(src, heads); err != nil {
		return nil, err
	}
	return r, nil
}

// resolveRef is the catalog reference a stored DataHub URN becomes: what the
// import recorded for it; for a tag, the tag of that name; for a dataset the
// import did not list, the catalog dataset with its path; for a field, that
// column of its dataset.
func (r *run) resolveRef(urn string) string {
	if ref := r.mapped[urn]; ref != "" {
		return ref
	}
	switch {
	case strings.HasPrefix(urn, "urn:li:tag:"):
		return catalog.KeyRef(catalog.KindTag, urnKey(urn)).String()
	case strings.HasPrefix(urn, "urn:li:dataset:"):
		if refs, _ := r.resolve(urn); len(refs) > 0 {
			return refs[0]
		}
	case strings.HasPrefix(urn, "urn:li:schemaField:"):
		return r.resolveField(urn)
	}
	return ""
}

// resolveField is the column reference a DataHub schema field URN becomes:
// urn:li:schemaField:(<dataset urn>,<field path>).
func (r *run) resolveField(urn string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(urn, "urn:li:schemaField:("), ")")
	i := strings.LastIndex(body, ",")
	if i < 0 {
		return ""
	}
	dataset := r.resolveRef(body[:i])
	field, ok := topLevelField(body[i+1:])
	if dataset == "" || !ok {
		return ""
	}
	ref, err := catalog.ParseRef(dataset)
	if err != nil {
		return ""
	}
	return catalog.ColumnRef(ref, field).String()
}

// record keeps what a URN became, for this run and the next.
func (r *run) record(urn, ref string) error {
	r.mapped[urn] = ref
	if err := r.im.mapping.Record(r.ctx, urn, ref); err != nil {
		return fmt.Errorf("recording %s: %w", urn, err)
	}
	return nil
}

// definitions imports every definition, glossary nodes before what sits
// under them. Definitions whose parents form a cycle are imported without one.
func (r *run) definitions(defs []Definition) error {
	pending := defs
	for len(pending) > 0 {
		later, err := r.importReady(pending)
		if err != nil {
			return err
		}
		if len(later) == len(pending) {
			for _, d := range later {
				d.Parent = ""
				if err := r.definition(d); err != nil {
					return err
				}
			}
			return nil
		}
		pending = later
	}
	return nil
}

// importReady imports the pending definitions whose parent is imported or not
// pending, and returns the rest.
func (r *run) importReady(pending []Definition) ([]Definition, error) {
	var later []Definition
	for _, d := range pending {
		if d.Parent != "" && r.mapped[d.Parent] == "" && hasDefinition(pending, d.Parent) {
			later = append(later, d)
			continue
		}
		if err := r.definition(d); err != nil {
			return nil, err
		}
	}
	return later, nil
}

// hasDefinition reports whether urn is among defs.
func hasDefinition(defs []Definition, urn string) bool {
	for _, d := range defs {
		if d.URN == urn {
			return true
		}
	}
	return false
}

// definition imports one definition and its curation.
func (r *run) definition(d Definition) error {
	slug, err := r.slugFor(d)
	if err != nil {
		return err
	}
	def, err := r.im.catalog.PutDefinition(r.ctx, catalog.DefinitionInput{
		Kind: d.Kind, Slug: slug, Name: d.Name, Parent: r.mapped[d.Parent],
	})
	if err != nil {
		return fmt.Errorf("importing %s: %w", d.URN, err)
	}
	if err := r.record(d.URN, def.Reference); err != nil {
		return err
	}
	r.report.Definitions[string(d.Kind)]++
	ref, err := catalog.ParseRef(def.Reference)
	if err != nil {
		return fmt.Errorf("importing %s: %w", d.URN, err)
	}
	return r.write(d.URN, ref, d.Kind, def.Aspects, d.Aspects)
}

// slugFor is the key a definition is stored under: the one it was stored
// under by an earlier run, its name's slug, or, when another DataHub
// definition already took that slug, the slug of its name and its DataHub key.
// A tag is keyed by its name.
func (r *run) slugFor(d Definition) (string, error) {
	if d.Kind == catalog.KindTag {
		return urnKey(d.URN), nil
	}
	if ref := r.mapped[d.URN]; ref != "" {
		parsed, err := catalog.ParseRef(ref)
		if err == nil {
			if def, err := r.im.catalog.GetDefinition(r.ctx, parsed); err == nil {
				return def.Slug, nil
			}
		}
	}
	slug := catalog.Slug(d.Name)
	existing, err := r.im.catalog.GetDefinition(r.ctx, catalog.KeyRef(d.Kind, slug))
	if errors.Is(err, catalog.ErrNotFound) {
		return slug, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s %s: %w", d.Kind, slug, err)
	}
	for _, ref := range r.mapped {
		if ref == existing.Reference {
			return catalog.Slug(d.Name + " " + urnKey(d.URN)), nil
		}
	}
	return slug, nil
}

// datasets imports every dataset that a catalog dataset has the path of.
func (r *run) datasets(src Source, heads []DatasetHead) error {
	r.report.Datasets.Read = len(heads)
	groups, order := r.group(heads)
	var edges []catalog.Edge
	for _, ref := range order {
		group := groups[ref]
		merged, fromRemoved, ok := r.merge(src, group)
		if !ok {
			continue
		}
		parsed, err := catalog.ParseRef(ref)
		if err != nil {
			return fmt.Errorf("importing onto %s: %w", ref, err)
		}
		if err := r.dataset(parsed, group, merged); err != nil {
			return err
		}
		r.report.Datasets.Imported++
		if fromRemoved {
			r.report.Datasets.FromRemoved++
		}
		edges = append(edges, r.upstreamEdges(ref, merged.Upstreams)...)
	}
	if len(edges) > 0 {
		if err := r.im.catalog.AddEdges(r.ctx, edges); err != nil {
			return fmt.Errorf("recording lineage: %w", err)
		}
	}
	r.report.LineageEdges = len(edges)
	return nil
}

// group finds the catalog datasets each DataHub dataset lands on, and lists
// the DataHub records of each catalog dataset with the live ones first.
func (r *run) group(heads []DatasetHead) (groups map[string][]DatasetHead, order []string) {
	groups = map[string][]DatasetHead{}
	for _, h := range heads {
		refs, reason := r.resolve(h.URN)
		if len(refs) == 0 {
			r.report.Datasets.Skipped++
			r.report.skip(h.URN, reason, "")
			continue
		}
		if err := r.record(h.URN, refs[0]); err != nil {
			r.report.skip(h.URN, err.Error(), "")
			continue
		}
		for _, ref := range refs {
			if _, seen := groups[ref]; !seen {
				order = append(order, ref)
			}
			if h.Removed {
				groups[ref] = append(groups[ref], h)
			} else {
				groups[ref] = append([]DatasetHead{h}, groups[ref]...)
			}
		}
	}
	return groups, order
}

// resolve finds the catalog datasets a DataHub dataset URN has the path of:
// on a Trino platform, the dataset whose catalog, schema and table match, with
// DataHub's catalog name mapped back to the engine's and a leading platform
// instance dropped.
func (r *run) resolve(urn string) (refs []string, reason string) {
	parsed, err := urnbuild.ParseDatasetURN(urn)
	if err != nil {
		return nil, ReasonUnsupported
	}
	platform := strings.TrimPrefix(parsed.Platform, "urn:li:dataPlatform:")
	if platform == "tableau" {
		return nil, ReasonTableau
	}
	if platform != "trino" && (r.im.opts.Platform == "" || platform != r.im.opts.Platform) {
		return nil, ReasonPlatform
	}
	for _, path := range r.im.candidatePaths(parsed.Name) {
		found, err := r.im.catalog.DatasetsByPath(r.ctx, "trino", path)
		if err == nil && len(found) > 0 {
			return found, ""
		}
	}
	return nil, ReasonNotCataloged
}

// tableParts is how many parts a Trino table's path has: catalog, schema,
// table.
const tableParts = 3

// jsonNull is an aspect's JSON when an Aspects value lacks it.
const jsonNull = "null"

// candidatePaths are the catalog paths a DataHub dataset name may stand for.
func (im *Importer) candidatePaths(name string) []string {
	parts := strings.Split(name, ".")
	paths := []string{name}
	if len(parts) > tableParts {
		parts = parts[len(parts)-tableParts:]
		paths = append(paths, strings.Join(parts, "."))
	}
	for engine, dh := range im.opts.CatalogMapping {
		if len(parts) == tableParts && parts[0] == dh {
			paths = append(paths, engine+"."+parts[1]+"."+parts[2])
		}
	}
	return paths
}

// merge reads a catalog dataset's DataHub records and merges them: the live
// record's curation, with any aspect it lacks taken from a removed record. It
// reports whether a removed record supplied anything, and false when none of
// the records could be read.
func (r *run) merge(src Source, group []DatasetHead) (merged *DatasetRecord, fromRemoved, ok bool) {
	for _, h := range group {
		rec, err := r.read(src, h.URN)
		if err != nil {
			r.report.skip(h.URN, ReasonUnreadable+": "+err.Error(), "")
			continue
		}
		if merged == nil {
			merged = copyRecord(rec)
			fromRemoved = rec.Removed && hasCuration(rec)
			continue
		}
		if fillRecord(merged, rec) && rec.Removed {
			fromRemoved = true
		}
	}
	return merged, fromRemoved, merged != nil
}

// copyRecord copies a record so merging into it leaves the one read intact.
func copyRecord(rec *DatasetRecord) *DatasetRecord {
	copied := *rec
	copied.Columns = maps.Clone(rec.Columns)
	if copied.Columns == nil {
		copied.Columns = map[string]catalog.Aspects{}
	}
	return &copied
}

// fillRecord fills merged with what rec has and merged lacks, and reports
// whether it filled anything.
func fillRecord(merged, rec *DatasetRecord) bool {
	filled := fill(&merged.Aspects, rec.Aspects)
	for name, a := range rec.Columns {
		current := merged.Columns[name]
		if fill(&current, a) {
			filled = true
		}
		merged.Columns[name] = current
	}
	merged.Upstreams = union(merged.Upstreams, rec.Upstreams)
	return filled
}

// read reads a record once per run, however many catalog datasets it lands on.
func (r *run) read(src Source, urn string) (*DatasetRecord, error) {
	if rec, ok := r.records[urn]; ok {
		return rec, nil
	}
	rec, err := src.Dataset(r.ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", urn, err)
	}
	r.records[urn] = rec
	return rec, nil
}

// hasCuration reports whether a record carries any curation.
func hasCuration(rec *DatasetRecord) bool {
	if !isZero(rec.Aspects) {
		return true
	}
	for _, a := range rec.Columns {
		if !isZero(a) {
			return true
		}
	}
	return false
}

// isZero reports whether a carries no aspect.
func isZero(a catalog.Aspects) bool {
	b, _ := json.Marshal(a)
	return string(b) == "{}"
}

// fill copies into dst every aspect src has and dst lacks, and reports
// whether it copied any.
func fill(dst *catalog.Aspects, src catalog.Aspects) bool {
	copied := false
	for _, name := range catalog.AspectsFor(catalog.KindDataset) {
		if aspectJSON(dst, name) == jsonNull && aspectJSON(&src, name) != jsonNull {
			raw := json.RawMessage(aspectJSON(&src, name))
			if err := dst.Set(name, raw); err == nil {
				copied = true
			}
		}
	}
	return copied
}

// aspectJSON is one aspect of a, as JSON, or jsonNull when a lacks it.
func aspectJSON(a *catalog.Aspects, name catalog.AspectName) string {
	whole, err := json.Marshal(a)
	if err != nil {
		return jsonNull
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(whole, &fields); err != nil {
		return jsonNull
	}
	if v, ok := fields[string(name)]; ok {
		return string(v)
	}
	return jsonNull
}

// dataset writes a merged record onto one catalog dataset and its columns.
func (r *run) dataset(ref catalog.Ref, group []DatasetHead, rec *DatasetRecord) error {
	current, err := r.im.catalog.GetDataset(r.ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ref, err)
	}
	urn := group[0].URN
	if err := r.write(urn, ref, catalog.KindDataset, current.Aspects, r.mapAspects(urn, rec.Aspects)); err != nil {
		return err
	}
	columns := map[string]catalog.Column{}
	for _, c := range current.Columns {
		columns[strings.ToLower(c.Name)] = c
	}
	for name, a := range rec.Columns {
		col, ok := columns[strings.ToLower(name)]
		if !ok {
			if !isZero(a) {
				r.report.skip(urn, ReasonNoColumn, name)
			}
			continue
		}
		if err := r.write(urn, catalog.ColumnRef(ref, col.Name), catalog.KindColumn, col.Aspects, r.mapAspects(urn, a)); err != nil {
			return err
		}
	}
	return nil
}

// mapAspects turns the DataHub URNs an aspect holds into catalog references,
// leaving out, and reporting, the ones the import has no entity for.
func (r *run) mapAspects(urn string, a catalog.Aspects) catalog.Aspects {
	terms := make([]string, 0, len(a.GlossaryTerms))
	for _, t := range a.GlossaryTerms {
		if ref := r.mapped[t]; ref != "" {
			terms = append(terms, ref)
			continue
		}
		r.report.skip(t, ReasonUnmapped, urn)
	}
	a.GlossaryTerms = nil
	if len(terms) > 0 {
		a.GlossaryTerms = terms
	}
	if a.Domain != "" {
		ref := r.mapped[a.Domain]
		if ref == "" {
			r.report.skip(a.Domain, ReasonUnmapped, urn)
		}
		a.Domain = ref
	}
	return a
}

// write writes the aspects of one entity the import has a value for, in one
// transaction. An aspect already holding the value is left alone, and so is
// one something other than the import has written.
func (r *run) write(urn string, ref catalog.Ref, kind catalog.Kind, current, imported catalog.Aspects) error {
	var edits []catalog.AspectEdit
	for _, name := range catalog.AspectsFor(kind) {
		value := aspectJSON(&imported, name)
		if value == jsonNull {
			continue
		}
		normalized, err := catalog.NormalizeAspect(kind, name, json.RawMessage(value))
		if err != nil {
			r.report.skip(urn, fmt.Sprintf("its %s could not be read: %v", name, err), ref.String())
			continue
		}
		if bytes.Equal(normalized, []byte(aspectJSON(&current, name))) {
			r.report.AspectsUnchanged++
			continue
		}
		kept, err := r.writtenByOthers(ref, name)
		if err != nil {
			return err
		}
		if kept {
			r.report.AspectsKept++
			r.report.skip(urn, ReasonKept, ref.String()+" "+string(name))
			continue
		}
		edits = append(edits, catalog.AspectEdit{Ref: ref, Aspect: name, Value: normalized})
	}
	if len(edits) == 0 {
		return nil
	}
	if _, err := r.im.catalog.WriteAspects(r.ctx, edits, Principal); err != nil {
		return fmt.Errorf("writing %s: %w", ref, err)
	}
	r.report.AspectsWritten += len(edits)
	return nil
}

// writtenByOthers reports whether anything other than the import has written
// an aspect.
func (r *run) writtenByOthers(ref catalog.Ref, name catalog.AspectName) (bool, error) {
	history, err := r.im.catalog.History(r.ctx, ref, name)
	if err != nil {
		return false, fmt.Errorf("reading the history of %s %s: %w", ref, name, err)
	}
	for _, c := range history {
		if c.WrittenBy != Principal {
			return true, nil
		}
	}
	return false, nil
}

// upstreamEdges are the lineage edges into ref from the upstreams the import
// has a catalog dataset for.
func (r *run) upstreamEdges(ref string, upstreams []string) []catalog.Edge {
	var out []catalog.Edge
	for _, up := range upstreams {
		source := r.mapped[up]
		if source == "" {
			if refs, _ := r.resolve(up); len(refs) > 0 {
				source = refs[0]
			}
		}
		if source == "" {
			r.report.skip(up, ReasonUnmapped, "upstream of "+ref)
			continue
		}
		out = append(out, catalog.Edge{
			Source: source, Target: ref, WriterKind: catalog.WriterImport,
			WriterRef: "datahub", WrittenBy: Principal, WrittenAt: r.im.now(),
		})
	}
	return out
}
