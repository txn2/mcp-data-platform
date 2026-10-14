package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// fieldCatalogChanges is the changeset new_value key holding the ids of the
// catalog changes an apply wrote. Rollback reverts exactly those (#1920).
const fieldCatalogChanges = "catalog_changes"

// qualityIssueTag is the tag flag_quality_issue sets; the detail, when given,
// is raised as an incident titled qualityIssueIncidentTitle (#722).
const qualityIssueTag = "QualityIssue"

// ErrTargetNotCataloged is returned for an apply target the platform catalog
// does not hold.
var ErrTargetNotCataloged = errors.New("the platform catalog holds no entity with this reference")

// curator writes apply_knowledge's changes to the platform catalog: it reads
// the entity's current curation, applies every change to a working copy, and
// writes each aspect that changed once, all in one transaction.
type curator struct {
	store catalog.Store
}

// resolveTarget reads an apply target as a catalog reference, refusing any
// other form with the forms it reads.
func (curator) resolveTarget(_ context.Context, target string) (catalog.Ref, error) {
	ref, err := catalog.ParseRef(strings.TrimSpace(target))
	if err != nil {
		return catalog.Ref{}, fmt.Errorf("entity_urn %q is not a catalog reference "+
			"(mcp:dataset:(kind,connection,path), mcp:column:..., mcp:glossary_term:<id>, mcp:tag:<name>, ...): %w", target, err)
	}
	return ref, nil
}

// entityState is the working copy of one entity's curation while an apply is
// planned: its kind, the aspects as read, and the aspects changes touched.
type entityState struct {
	ref     catalog.Ref
	aspects catalog.Aspects
	touched []catalog.AspectName
	// read is the entity's curation as read, which the write is checked
	// against so it never replaces a write made since.
	read []byte
}

// touch records that a change wrote name, once.
func (e *entityState) touch(name catalog.AspectName) {
	if !slices.Contains(e.touched, name) {
		e.touched = append(e.touched, name)
	}
}

// plan is an apply being assembled: the entities it reads, in the order first
// read, and the ids of what it created, for the changeset.
type plan struct {
	c        curator
	ctx      context.Context
	target   catalog.Ref
	entities []*entityState
	created  []string
}

// readAspects reads the current curation of the entity ref names.
func (c curator) readAspects(ctx context.Context, ref catalog.Ref) (catalog.Aspects, error) {
	switch ref.Kind {
	case catalog.KindDataset:
		d, err := c.store.GetDataset(ctx, ref)
		if err != nil {
			return catalog.Aspects{}, notCataloged(ref, err)
		}
		return d.Aspects, nil
	case catalog.KindColumn:
		d, err := c.store.GetDataset(ctx, ref.Dataset())
		if err != nil {
			return catalog.Aspects{}, notCataloged(ref, err)
		}
		for _, col := range d.Columns {
			if col.Name == ref.Column {
				return col.Aspects, nil
			}
		}
		return catalog.Aspects{}, fmt.Errorf("looking up %s: %w", ref, ErrTargetNotCataloged)
	default:
		d, err := c.store.GetDefinition(ctx, ref)
		if err != nil {
			return catalog.Aspects{}, notCataloged(ref, err)
		}
		return d.Aspects, nil
	}
}

// notCataloged names the reference a store read failed for, and says the
// catalog does not hold it when that is why.
func notCataloged(ref catalog.Ref, err error) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return fmt.Errorf("looking up %s: %w", ref, ErrTargetNotCataloged)
	}
	return fmt.Errorf("reading %s: %w", ref, err)
}

// entity returns the working copy of ref, reading it on first use.
func (p *plan) entity(ref catalog.Ref) (*entityState, error) {
	for _, e := range p.entities {
		if e.ref.String() == ref.String() {
			return e, nil
		}
	}
	a, err := p.c.readAspects(p.ctx, ref)
	if err != nil {
		return nil, err
	}
	read, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ref, err)
	}
	e := &entityState{ref: ref, aspects: a, read: read}
	p.entities = append(p.entities, e)
	return e, nil
}

// changeEntity is the entity one change writes: a column of the target when
// the change's target is "column:<name>" and the change type addresses one,
// otherwise the target itself.
func (p *plan) changeEntity(ch ApplyChange) (*entityState, error) {
	if columnAddressable[actionType(ch.ChangeType)] {
		if name, ok := parseColumnTarget(ch.Target); ok {
			if p.target.Kind != catalog.KindDataset {
				return nil, fmt.Errorf("change %s: a column target needs a dataset entity_urn, got a %s", ch.ChangeType, p.target.Kind)
			}
			return p.entity(catalog.ColumnRef(p.target, name))
		}
	}
	return p.entity(p.target)
}

// columnAddressable are the change types whose target may name a column of
// the dataset ("column:<name>") rather than carry a value of its own.
var columnAddressable = map[actionType]bool{
	actionUpdateDescription: true,
	actionAddTag:            true,
	actionRemoveTag:         true,
	actionAddGlossaryTerm:   true,
}

// changeFunc applies one change to the entity it writes.
type changeFunc func(p *plan, e *entityState, ch ApplyChange) error

// changeFuncs is every catalog change type's edit. add_prompt and delete_tag
// are not here: the first writes a prompt, the second every entity carrying
// the tag.
var changeFuncs = map[actionType]changeFunc{
	actionUpdateDescription:        setDescription,
	actionAddTag:                   addTag,
	actionRemoveTag:                removeTag,
	actionAddGlossaryTerm:          addGlossaryTerm,
	actionFlagQualityIssue:         flagQualityIssue,
	actionAddDocumentation:         addDocumentation,
	actionAddCuratedQuery:          addCuratedQuery,
	actionSetStructuredProperty:    setStructuredProperty,
	actionRemoveStructuredProperty: removeStructuredProperty,
	actionRaiseIncident:            raiseIncident,
	actionResolveIncident:          resolveIncident,
	actionSetCustomProperty:        setCustomProperty,
	actionRemoveCustomProperty:     removeCustomProperty,
	actionAddContextDocument:       addContextDocument,
	actionUpdateContextDocument:    updateContextDocument,
	actionRemoveContextDocument:    removeContextDocument,
}

// add applies one change to the plan.
func (p *plan) add(ch ApplyChange) error {
	fn, ok := changeFuncs[actionType(ch.ChangeType)]
	if !ok {
		return fmt.Errorf("unsupported change type: %s", ch.ChangeType)
	}
	e, err := p.changeEntity(ch)
	if err != nil {
		return err
	}
	if err := fn(p, e, ch); err != nil {
		return fmt.Errorf("change %s: %w", ch.ChangeType, err)
	}
	return nil
}

// edits returns each touched aspect's new value, in the order touched.
func (p *plan) edits() ([]catalog.AspectEdit, error) {
	var out []catalog.AspectEdit
	for _, e := range p.entities {
		var read catalog.Aspects
		if err := json.Unmarshal(e.read, &read); err != nil {
			return nil, fmt.Errorf("re-reading %s: %w", e.ref, err)
		}
		for _, name := range e.touched {
			value, err := aspectValue(&e.aspects, name)
			if err != nil {
				return nil, err
			}
			expect, err := aspectValue(&read, name)
			if err != nil {
				return nil, err
			}
			if string(expect) == "null" {
				expect = nil
			}
			out = append(out, catalog.AspectEdit{Ref: e.ref, Aspect: name, Value: value, Expect: expect, Checked: true})
		}
	}
	return out, nil
}

// aspectValue encodes one aspect of a working copy for writing. An emptied
// aspect is written as null, which clears it.
func aspectValue(a *catalog.Aspects, name catalog.AspectName) (json.RawMessage, error) {
	whole, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", name, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(whole, &fields); err != nil {
		return nil, fmt.Errorf("encoding %s: %w", name, err)
	}
	if v, ok := fields[string(name)]; ok {
		return v, nil
	}
	return json.RawMessage("null"), nil
}

func setDescription(_ *plan, e *entityState, ch ApplyChange) error {
	e.aspects.Description = ch.Detail
	e.touch(catalog.AspectDescription)
	return nil
}

// tagName reads a tag value in either form an agent sends: a bare name or
// mcp:tag:<name>. A urn: value is a DataHub URN, which names no tag of the
// platform catalog; it is refused rather than stored as a tag called that.
func tagName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if err := refuseURN(v, "a tag", "mcp:tag:<name> or the bare name"); err != nil {
		return "", err
	}
	if name, ok := strings.CutPrefix(v, "mcp:tag:"); ok {
		return name, nil
	}
	return v, nil
}

// refuseURN refuses a DataHub-style urn: value where the catalog reads a name
// or one of its own references, naming the form it does read.
func refuseURN(v, what, want string) error {
	if strings.HasPrefix(v, "urn:") {
		return fmt.Errorf("a DataHub URN (%q) does not name %s in the platform catalog; use %s", v, what, want)
	}
	return nil
}

func addTag(_ *plan, e *entityState, ch ApplyChange) error {
	name, err := tagName(ch.Detail)
	if err != nil {
		return err
	}
	return addTagName(e, name)
}

// addTagName adds a tag to an entity unless it already carries it.
func addTagName(e *entityState, name string) error {
	if name == "" {
		return errors.New("detail must name the tag")
	}
	if !slices.Contains(e.aspects.Tags, name) {
		e.aspects.Tags = append(e.aspects.Tags, name)
	}
	e.touch(catalog.AspectTags)
	return nil
}

func removeTag(_ *plan, e *entityState, ch ApplyChange) error {
	name, err := tagName(ch.Detail)
	if err != nil {
		return err
	}
	removeTagName(e, name)
	return nil
}

// removeTagName drops a tag from an entity.
func removeTagName(e *entityState, name string) {
	e.aspects.Tags = slices.DeleteFunc(e.aspects.Tags, func(t string) bool { return t == name })
	e.touch(catalog.AspectTags)
}

// addGlossaryTerm attaches a glossary term, creating the term when the catalog
// does not hold one by that name. Detail is an mcp:glossary_term: reference or
// the term's name.
func addGlossaryTerm(p *plan, e *entityState, ch ApplyChange) error {
	ref, err := p.glossaryTerm(ch.Detail)
	if err != nil {
		return err
	}
	if !slices.Contains(e.aspects.GlossaryTerms, ref) {
		e.aspects.GlossaryTerms = append(e.aspects.GlossaryTerms, ref)
	}
	e.touch(catalog.AspectGlossaryTerms)
	return nil
}

// glossaryTerm returns the reference of the term v names, creating it by name
// when it does not exist. A term named by mcp: reference must exist.
func (p *plan) glossaryTerm(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("detail must name the glossary term")
	}
	if strings.HasPrefix(v, "mcp:") {
		ref, err := catalog.ParseRef(v)
		if err != nil {
			return "", fmt.Errorf("glossary term: %w", err)
		}
		d, err := p.c.store.GetDefinition(p.ctx, ref)
		if err != nil {
			return "", notCataloged(ref, err)
		}
		return d.Reference, nil
	}
	name := v
	d, err := p.c.store.GetDefinition(p.ctx, catalog.KeyRef(catalog.KindGlossaryTerm, catalog.Slug(name)))
	if err == nil {
		return d.Reference, nil
	}
	if !errors.Is(err, catalog.ErrNotFound) {
		return "", fmt.Errorf("reading glossary term %q: %w", name, err)
	}
	d, err = p.c.store.PutDefinition(p.ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: name})
	if err != nil {
		return "", fmt.Errorf("creating glossary term %q: %w", name, err)
	}
	p.created = append(p.created, d.Reference)
	return d.Reference, nil
}

// flagQualityIssue sets the QualityIssue tag and, when the change carries a
// detail, raises an incident describing it unless an active one with the same
// description is already open.
func flagQualityIssue(p *plan, e *entityState, ch ApplyChange) error {
	if err := addTagName(e, qualityIssueTag); err != nil {
		return err
	}
	detail := strings.TrimSpace(ch.Detail)
	if detail == "" || e.ref.Kind != catalog.KindDataset {
		return nil
	}
	for _, in := range e.aspects.Incidents {
		if in.State == catalog.IncidentActive && in.Title == qualityIssueIncidentTitle && in.Description == ch.Detail {
			return nil
		}
	}
	p.openIncident(e, qualityIssueIncidentTitle, ch.Detail)
	return nil
}

// openIncident adds an active incident to a dataset and records its id.
func (p *plan) openIncident(e *entityState, title, description string) {
	id := uuid.NewString()
	e.aspects.Incidents = append(e.aspects.Incidents, catalog.Incident{
		ID: id, Title: title, Description: description, State: catalog.IncidentActive,
	})
	e.touch(catalog.AspectIncidents)
	p.created = append(p.created, id)
}

func raiseIncident(p *plan, e *entityState, ch ApplyChange) error {
	if strings.TrimSpace(ch.Target) == "" {
		return errors.New("target (the incident title) is required")
	}
	p.openIncident(e, ch.Target, ch.Detail)
	return nil
}

func resolveIncident(_ *plan, e *entityState, ch ApplyChange) error {
	for i := range e.aspects.Incidents {
		if e.aspects.Incidents[i].ID == ch.Target {
			e.aspects.Incidents[i].State = catalog.IncidentResolved
			e.aspects.Incidents[i].Resolution = ch.Detail
			e.touch(catalog.AspectIncidents)
			return nil
		}
	}
	return fmt.Errorf("no incident %q on %s", ch.Target, e.ref)
}

func addDocumentation(_ *plan, e *entityState, ch ApplyChange) error {
	if strings.TrimSpace(ch.Target) == "" {
		return errors.New("target (the link url) is required")
	}
	e.aspects.DocumentationLinks = slices.DeleteFunc(e.aspects.DocumentationLinks,
		func(l catalog.Link) bool { return l.URL == ch.Target })
	e.aspects.DocumentationLinks = append(e.aspects.DocumentationLinks, catalog.Link{URL: ch.Target, Description: ch.Detail})
	e.touch(catalog.AspectDocumentationLinks)
	return nil
}

// addCuratedQuery adds a curated query, replacing one of the same name.
func addCuratedQuery(_ *plan, e *entityState, ch ApplyChange) error {
	name := strings.TrimSpace(ch.Detail)
	if name == "" {
		return errors.New("detail (the query name) is required")
	}
	appendCuratedQuery(&e.aspects, catalog.CuratedQuery{Name: name, SQL: ch.QuerySQL, Description: ch.QueryDescription})
	e.touch(catalog.AspectCuratedQueries)
	return nil
}

// appendCuratedQuery adds q to a, replacing a query of the same name.
func appendCuratedQuery(a *catalog.Aspects, q catalog.CuratedQuery) {
	a.CuratedQueries = slices.DeleteFunc(a.CuratedQueries, func(x catalog.CuratedQuery) bool { return x.Name == q.Name })
	a.CuratedQueries = append(a.CuratedQueries, q)
}

// propertyKey reads a structured property name, refusing a DataHub
// structured-property URN in its place.
func propertyKey(target string) (string, error) {
	key := strings.TrimSpace(target)
	if err := refuseURN(key, "a structured property", "the property name"); err != nil {
		return "", err
	}
	return key, nil
}

func setStructuredProperty(_ *plan, e *entityState, ch ApplyChange) error {
	key, err := propertyKey(ch.Target)
	if err != nil {
		return err
	}
	if key == "" {
		return errors.New("target (the property name) is required")
	}
	values, err := parsePropertyValues(ch.Detail)
	if err != nil {
		return fmt.Errorf("parsing property values: %w", err)
	}
	if e.aspects.StructuredProperties == nil {
		e.aspects.StructuredProperties = map[string][]any{}
	}
	e.aspects.StructuredProperties[key] = values
	e.touch(catalog.AspectStructuredProperties)
	return nil
}

func removeStructuredProperty(_ *plan, e *entityState, ch ApplyChange) error {
	key, err := propertyKey(ch.Target)
	if err != nil {
		return err
	}
	delete(e.aspects.StructuredProperties, key)
	e.touch(catalog.AspectStructuredProperties)
	return nil
}

func setCustomProperty(_ *plan, e *entityState, ch ApplyChange) error {
	if e.aspects.CustomProperties == nil {
		e.aspects.CustomProperties = map[string]string{}
	}
	e.aspects.CustomProperties[ch.Target] = ch.Detail
	e.touch(catalog.AspectCustomProperties)
	return nil
}

func removeCustomProperty(_ *plan, e *entityState, ch ApplyChange) error {
	delete(e.aspects.CustomProperties, ch.Target)
	e.touch(catalog.AspectCustomProperties)
	return nil
}

// addContextDocument adds a document: target is its title, detail its
// content, query_description its category.
func addContextDocument(p *plan, e *entityState, ch ApplyChange) error {
	id := uuid.NewString()
	e.aspects.ContextDocuments = append(e.aspects.ContextDocuments, catalog.ContextDocument{
		ID: id, Title: ch.Target, Content: ch.Detail, Category: ch.QueryDescription,
	})
	e.touch(catalog.AspectContextDocuments)
	p.created = append(p.created, id)
	return nil
}

// updateContextDocument edits a document by id (target): detail replaces its
// content, query_sql its title and query_description its category, each only
// when given.
func updateContextDocument(_ *plan, e *entityState, ch ApplyChange) error {
	for i := range e.aspects.ContextDocuments {
		d := &e.aspects.ContextDocuments[i]
		if d.ID != ch.Target {
			continue
		}
		d.Content = firstNonEmpty(ch.Detail, d.Content)
		d.Title = firstNonEmpty(ch.QuerySQL, d.Title)
		d.Category = firstNonEmpty(ch.QueryDescription, d.Category)
		e.touch(catalog.AspectContextDocuments)
		return nil
	}
	return fmt.Errorf("no context document %q on %s", ch.Target, e.ref)
}

func removeContextDocument(_ *plan, e *entityState, ch ApplyChange) error {
	before := len(e.aspects.ContextDocuments)
	e.aspects.ContextDocuments = slices.DeleteFunc(e.aspects.ContextDocuments,
		func(d catalog.ContextDocument) bool { return d.ID == ch.Target })
	if len(e.aspects.ContextDocuments) == before {
		return fmt.Errorf("no context document %q on %s", ch.Target, e.ref)
	}
	e.touch(catalog.AspectContextDocuments)
	return nil
}

// firstNonEmpty returns v unless it is empty, then fallback.
func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// untag removes a tag from every dataset and column carrying it, at most
// limit of them, and returns the entities it rewrote.
func (c curator) untag(ctx context.Context, tag string, limit int, by string) ([]string, curationResult, error) {
	type untagged struct {
		refs    []string
		written curationResult
	}
	out, err := retryStale(func() (untagged, error) {
		p := &plan{c: c, ctx: ctx}
		if _, err := p.untagAll(tag, limit); err != nil {
			return untagged{}, err
		}
		written, err := p.write(by)
		if err != nil {
			return untagged{}, err
		}
		refs := make([]string, len(p.entities))
		for i, e := range p.entities {
			refs[i] = e.ref.String()
		}
		return untagged{refs: refs, written: written}, nil
	})
	return out.refs, out.written, err
}

// untagAll plans the removal of a tag from every dataset and column carrying
// it, at most limit of them, and reports whether more carry it.
func (p *plan) untagAll(tag string, limit int) (truncated bool, err error) {
	refs, err := p.c.store.TaggedEntities(p.ctx, tag, limit+1)
	if err != nil {
		return false, fmt.Errorf("listing entities tagged %q: %w", tag, err)
	}
	if len(refs) > limit {
		refs, truncated = refs[:limit], true
	}
	for _, s := range refs {
		ref, err := catalog.ParseRef(s)
		if err != nil {
			return false, fmt.Errorf("tagged entity: %w", err)
		}
		e, err := p.entity(ref)
		if err != nil {
			return false, err
		}
		removeTagName(e, tag)
	}
	return truncated, nil
}

// curationResult is what an apply wrote to the catalog: the changes, in the
// order written, and the ids of what it created (incidents, context documents,
// glossary terms).
type curationResult struct {
	changes []catalog.Change
	created []string
}

// changeIDs lists the ids of the changes written.
func (r curationResult) changeIDs() []string {
	ids := make([]string, len(r.changes))
	for i, c := range r.changes {
		ids[i] = c.ID
	}
	return ids
}

// apply plans every catalog change of an apply against target and writes them
// in one transaction. add_prompt changes are left to the caller. A delete_tag
// change removes the tag target names from every entity carrying it.
func (c curator) apply(ctx context.Context, target catalog.Ref, changes []ApplyChange, by string) (curationResult, error) {
	return retryStale(func() (curationResult, error) { return c.applyOnce(ctx, target, changes, by) })
}

// retryStale runs a read, plan and write again when another writer changed an
// aspect it read, so two applies to one entity at once both land.
func retryStale[T any](fn func() (T, error)) (T, error) {
	for attempt := 1; ; attempt++ {
		out, err := fn()
		if !errors.Is(err, catalog.ErrStale) || attempt == staleRetries {
			return out, err
		}
	}
}

// staleRetries is how many times an apply is planned again after another
// writer changed an aspect it read.
const staleRetries = 3

// applyOnce reads, plans and writes an apply once.
func (c curator) applyOnce(ctx context.Context, target catalog.Ref, changes []ApplyChange, by string) (curationResult, error) {
	p := &plan{c: c, ctx: ctx, target: target}
	for i, ch := range changes {
		var err error
		switch actionType(ch.ChangeType) {
		case actionAddPrompt:
			continue
		case actionDeleteTag:
			err = p.deleteTag()
		default:
			err = p.add(ch)
		}
		if err != nil {
			return curationResult{}, fmt.Errorf("changes[%d]: %w (nothing was written)", i, err)
		}
	}
	return p.write(by)
}

// deleteTag plans delete_tag: the entity_urn names a tag, which is taken off
// every dataset and column carrying it.
func (p *plan) deleteTag() error {
	if p.target.Kind != catalog.KindTag {
		return fmt.Errorf("delete_tag needs a tag entity_urn (mcp:tag:<name>), got a %s", p.target.Kind)
	}
	truncated, err := p.untagAll(p.target.Key, deleteTagMaxEntities)
	if err != nil {
		return err
	}
	if truncated {
		return fmt.Errorf("more than %d entities carry tag %q; remove it in parts with action=bulk_untag", deleteTagMaxEntities, p.target.Key)
	}
	return nil
}

// deleteTagMaxEntities bounds how many entities one delete_tag rewrites in its
// single transaction.
const deleteTagMaxEntities = 5000

// write writes the plan's edits in one transaction.
func (p *plan) write(by string) (curationResult, error) {
	edits, err := p.edits()
	if err != nil {
		return curationResult{}, err
	}
	if len(edits) == 0 {
		return curationResult{created: p.created}, nil
	}
	changes, err := p.c.store.WriteAspects(p.ctx, edits, by)
	if err != nil {
		return curationResult{}, fmt.Errorf("writing to the catalog: %w (nothing was written)", err)
	}
	return curationResult{changes: changes, created: p.created}, nil
}

// metadata reads the summary of an entity's curation the review, synthesize
// and changeset records carry.
func (c curator) metadata(ctx context.Context, ref catalog.Ref) (*EntityMetadata, error) {
	a, err := c.readAspects(ctx, ref)
	if err != nil {
		return nil, err
	}
	owners := make([]string, 0, len(a.Owners))
	for _, o := range a.Owners {
		owners = append(owners, o.Owner)
	}
	return &EntityMetadata{
		Description:   a.Description,
		Tags:          nonNil(a.Tags),
		GlossaryTerms: nonNil(a.GlossaryTerms),
		Owners:        owners,
	}, nil
}

// nonNil returns s, or an empty slice for nil, so JSON carries [] not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ErrNoQueryTarget refuses a promoted query none of whose datasets the catalog
// holds: there is no dataset to keep it on.
var ErrNoQueryTarget = errors.New("the platform catalog holds none of the datasets the statement reads")

// CatalogQueryWriter keeps a promoted statement as a curated query on every
// dataset it reads (#1321, #1920).
type CatalogQueryWriter struct {
	c curator
}

// NewCatalogQueryWriter returns the curated-query writer over store, or nil
// when there is no catalog to write to.
func NewCatalogQueryWriter(store catalog.Store) *CatalogQueryWriter {
	if store == nil {
		return nil
	}
	return &CatalogQueryWriter{c: curator{store: store}}
}

// CreateCuratedQuery adds the query to each dataset the targets name, in one
// transaction, and returns the reference of the first. A target the catalog
// does not hold is left out; none held is ErrNoQueryTarget.
func (w *CatalogQueryWriter) CreateCuratedQuery(ctx context.Context, targets []string, name, sql, description string) (string, error) {
	return retryStale(func() (string, error) { return w.createOnce(ctx, targets, name, sql, description) })
}

// createOnce reads, plans and writes a promoted query once.
func (w *CatalogQueryWriter) createOnce(ctx context.Context, targets []string, name, sql, description string) (string, error) {
	p := &plan{c: w.c, ctx: ctx}
	for _, t := range targets {
		ref, err := w.c.resolveTarget(ctx, t)
		if err != nil || ref.Kind != catalog.KindDataset {
			continue
		}
		e, err := p.entity(ref)
		if err != nil {
			continue
		}
		appendCuratedQuery(&e.aspects, catalog.CuratedQuery{Name: name, SQL: sql, Description: description})
		e.touch(catalog.AspectCuratedQueries)
	}
	if len(p.entities) == 0 {
		return "", ErrNoQueryTarget
	}
	if _, err := p.write(""); err != nil {
		return "", err
	}
	return p.entities[0].ref.String(), nil
}
