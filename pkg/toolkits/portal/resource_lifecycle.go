package portal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ResourceHolds counts the records still pointing at a managed resource
// (#1665): assets whose content references it, and prompts that attach it as
// reference material.
//
// Neither is a foreign key -- deleting the file leaves the row behind so the
// thing that depended on it reports the material as missing -- which is why the
// count has to be gathered and put to whoever is deleting, before the delete
// rather than after it.
//
// A knowledge page is deliberately not counted: the platform refuses an
// mcp:resource: citation on a shared page, because a resource is
// visibility-scoped, so no page can point at one.
//
// It is counts and not names because each of those records carries an audience
// of its own and the person deleting the file is not necessarily in any of
// them. The portal's used-by panel resolves those audiences and names what its
// reader may open; this is the part that can be said to anybody who can see the
// file.
type ResourceHolds struct {
	Assets  int `json:"assets"`
	Prompts int `json:"prompts"`
	// More says a count was cut at the bound rather than being the whole of
	// what points at the file. It is reported rather than left implicit
	// because a short count read as a complete one is the mistake this answer
	// exists to prevent.
	More bool `json:"more,omitempty"`
}

// Any reports whether anything at all points at the file.
func (h ResourceHolds) Any() bool {
	return h.Assets > 0 || h.Prompts > 0
}

// Describe renders the counts as the phrases a refusal lists, one per kind that
// has anything, in the order a reader cares about them: what serves the file to
// people, then what a procedure depends on.
func (h ResourceHolds) Describe() []string {
	var out []string
	for _, part := range []struct {
		n           int
		one, plural string
	}{
		{h.Assets, "1 asset references this file", "%d assets reference this file"},
		{h.Prompts, "1 prompt attaches this file", "%d prompts attach this file"},
	} {
		switch {
		case part.n == 1:
			out = append(out, part.one)
		case part.n > 1:
			out = append(out, fmt.Sprintf(part.plural, part.n))
		}
	}
	return out
}

// ResourceHoldReader answers what still points at a managed resource, so a
// delete can say what it would break before it breaks it.
//
// It is a capability the toolkit asks for rather than one it implements: the
// two reverse lookups live in two different layers, and the toolkit must not
// learn what either of them is.
type ResourceHoldReader interface {
	ResourceHolds(ctx context.Context, resourceID string) (ResourceHolds, error)
}

// SetResourceHolds binds the reader behind the delete warning. Called by the
// composition root once the asset and prompt layers exist. Without it a delete
// cannot establish what depends on the file, and says so rather than reporting
// that nothing does.
func (t *Toolkit) SetResourceHolds(r ResourceHoldReader) {
	t.resourceHolds = r
}

// resourceRecord is one managed resource as get and list report it: everything
// a fetch of its reference would carry except the bytes.
//
// It leads with the two names the caller hands to the next call for the reason
// a write's result does -- a lookup whose answer cannot be passed on is a
// lookup the caller has to repeat -- and it carries the address as well,
// because the address is what a caller holding no id looked the file up by.
type resourceRecord struct {
	ResourceID  string    `json:"resource_id"`
	Reference   string    `json:"reference"`
	URI         string    `json:"uri"`
	Filename    string    `json:"filename"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Scope       string    `json:"scope"`
	ScopeID     string    `json:"scope_id,omitempty"`
	Path        string    `json:"path"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedBy  string    `json:"uploaded_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// resourceGetOutput is what action=get reports.
type resourceGetOutput struct {
	// Found distinguishes an empty address from a file, because the two are
	// what the caller asked about: a create-or-replace decides on it, and a
	// person deciding whether to write there needs to know before they do.
	Found bool `json:"found"`
	// URI is the address that was looked up, reported whether or not anything
	// is filed at it.
	URI      string          `json:"uri"`
	Resource *resourceRecord `json:"resource,omitempty"`
	Message  string          `json:"message"`
}

// resourceListOutput is what action=list reports.
type resourceListOutput struct {
	Path      string           `json:"path"`
	Resources []resourceRecord `json:"resources"`
	// Total is everything under the folder, of which Resources is one page.
	Total   int    `json:"total"`
	Offset  int    `json:"offset"`
	Message string `json:"message"`
}

// resourceDeleteOutput is what action=delete reports, whether it deleted or
// refused.
type resourceDeleteOutput struct {
	ResourceID string `json:"resource_id"`
	URI        string `json:"uri"`
	Filename   string `json:"filename"`
	Deleted    bool   `json:"deleted"`
	// Holds is what pointed at the file. It is reported on a refusal, which is
	// what the refusal is about, and on a forced delete, which is what the
	// caller chose to break.
	Holds *ResourceHolds `json:"holds,omitempty"`
	// TableRegistrations are the query-engine tables registered over the file,
	// in the shape and under the key manage_table action=list reports (#1666).
	// They are named where the other holders are counted because the caller
	// already reaches them through manage_table, and a caller deciding whether
	// to force the delete is deciding about those registrations.
	TableRegistrations []TableRegistration `json:"table_registrations,omitempty"`
	Message            string              `json:"message"`
}

// handleGetResource answers what is filed at an address, or at a reference.
//
// It is the lookup `search` is not: search is relevance-ranked and capped, so a
// file it does not return is not a file that is not there. A caller landing one
// rolling file per source needs the second question answered, and answering it
// by remembering an id is what fails the moment the memory is cleared.
func (t *Toolkit) handleGetResource(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	claims := refClaims(ctx)
	if ref := strings.TrimSpace(input.Reference); ref != "" {
		id, err := parseResourceReference(ref)
		if err != nil {
			return toolkit.ErrorResult(err.Error()), nil, nil
		}
		res, err := t.resourceWriter.Get(ctx, id, claims)
		if err != nil {
			return toolkit.ErrorResult(err.Error()), nil, nil
		}
		return toolkit.JSONResultTyped(resourceGetOutput{
			Found: true, URI: res.URI, Resource: recordOf(res),
			Message: "This is the file " + ref + " names. Its content is not here: read it with fetch.",
		})
	}

	if err := requireAddress(input); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	res, uri, err := t.resourceWriter.Locate(ctx, addressOf(input), claims)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if res == nil {
		return toolkit.JSONResultTyped(resourceGetOutput{
			URI: uri,
			Message: "Nothing is filed at " + uri + ". Create it with action=create, or pass " +
				"if_exists=replace on a create to write there whether or not a file is already at that address.",
		})
	}
	return toolkit.JSONResultTyped(resourceGetOutput{
		Found: true, URI: res.URI, Resource: recordOf(res),
		Message: "This is the file at that address. Its content is not here: read it with fetch, replace it " +
			"with action=replace_content, and delete it with action=delete.",
	})
}

// handleListResources answers what is filed under a folder.
func (t *Toolkit) handleListResources(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	// Clamped here as well as in the writer, so the offset the result reports
	// is the one the page was actually taken from: a caller told it paged from
	// -3 would ask for the next page from the wrong place.
	offset := max(input.Offset, 0)
	found, total, err := t.resourceWriter.List(ctx, toolkit.ResourceQuery{
		Scope: input.Scope, ScopeID: input.ScopeID, Path: input.Path,
		Limit: input.Limit, Offset: offset,
	}, refClaims(ctx))
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	records := make([]resourceRecord, 0, len(found))
	for i := range found {
		records = append(records, *recordOf(&found[i]))
	}
	out := resourceListOutput{
		Path: input.Path, Resources: records, Total: total, Offset: offset,
		Message: fmt.Sprintf("%d of %d files, newest first.", len(records), total),
	}
	if out.Path == "" {
		out.Message += " The listing is rooted at the whole library; pass a path to narrow it to one folder."
	} else {
		out.Message += " The listing is rooted at " + out.Path + " and includes everything beneath it."
	}
	if len(records)+offset < total {
		out.Message += fmt.Sprintf(" Pass offset=%d for the next page.", offset+len(records))
	}
	return toolkit.JSONResultTyped(out)
}

// handleDeleteResource removes a file, or refuses while something still points
// at it.
//
// The refusal is the point of the action rather than an obstacle to it: nothing
// in the database stops the delete -- an asset reference and a prompt
// attachment both deliberately outlive the file so the thing that depended on
// it reports the material as missing -- so what would break has to be put to
// whoever is deleting, before the delete.
func (t *Toolkit) handleDeleteResource(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	claims := refClaims(ctx)
	res, errResult := t.resolveResourceTarget(ctx, input, claims)
	if errResult != nil {
		return errResult, nil, nil
	}

	reference := knowledgepage.EntityRef{
		TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID,
	}.URN()
	if !input.Force {
		refusal, refused, err := t.refuseDelete(ctx, res, reference)
		if err != nil {
			return toolkit.ErrorResult(err.Error()), nil, nil
		}
		if refused {
			return toolkit.JSONResultTyped(refusal)
		}
	}

	out := resourceDeleteOutput{
		ResourceID: res.ID, URI: res.URI, Filename: res.Filename, Deleted: true,
	}
	if input.Force {
		// What the caller chose to break, gathered before the file goes: after
		// the delete the counts are the same rows and there is nothing left to
		// name them against.
		out.Holds, out.TableRegistrations = t.resourceHoldings(ctx, res.ID, reference)
	}
	if _, err := t.resourceWriter.Delete(ctx, res.ID, claims); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	t.dropResourceTables(ctx, res.ID)
	out.Message = deleteMessage(out)
	return toolkit.JSONResultTyped(out)
}

// refuseDelete gathers what points at the file and builds the refusal.
//
// refused says whether there was anything to refuse, which is what the caller
// acts on: nothing pointing at the file is an answer rather than an absent
// refusal, and it is the ordinary case.
func (t *Toolkit) refuseDelete(
	ctx context.Context, res *resource.Resource, reference string,
) (out resourceDeleteOutput, refused bool, err error) {
	if t.resourceHolds == nil {
		return out, false, errors.New("this deployment cannot establish what depends on a managed resource, " +
			"so a delete cannot say what it would break. Pass force=true to delete anyway. Nothing was deleted")
	}
	holds, err := t.resourceHolds.ResourceHolds(ctx, res.ID)
	if err != nil {
		// Reported rather than absorbed: an empty count would read as "nothing
		// depends on this file", which is the one wrong answer somebody about
		// to delete it must not act on.
		return out, false, fmt.Errorf("could not establish what depends on this file, so it was not deleted. "+
			"Try again, or pass force=true to delete without the check: %w", err)
	}
	regs := t.resourceTables(ctx, reference)
	if !holds.Any() && len(regs) == 0 {
		return out, false, nil
	}

	names := queryTableNames(regs)
	reasons := holds.Describe()
	switch len(names) {
	case 0:
	case 1:
		reasons = append(reasons, "the table "+names[0]+" is registered over it")
	default:
		reasons = append(reasons, fmt.Sprintf("%d tables are registered over it: %s",
			len(names), strings.Join(names, listSeparator)))
	}
	out = resourceDeleteOutput{
		ResourceID: res.ID, URI: res.URI, Filename: res.Filename,
		Holds: &holds, TableRegistrations: regs,
		Message: fmt.Sprintf("Not deleted: %s. Deleting it leaves each of those pointing at a file that is "+
			"not there. Open %s in the portal to see exactly what depends on it, or call this again with "+
			"force=true to delete it and break them.", strings.Join(reasons, listSeparator), reference),
	}
	if holds.More {
		out.Message += " More point at it than were counted."
	}
	return out, true, nil
}

// listSeparator joins the phrases a message lists.
const listSeparator = ", "

// resourceHoldings reads both halves of what points at the file, tolerating a
// failed read. It is what a FORCED delete records: the caller has already
// decided, so a count that could not be gathered must not stop them.
func (t *Toolkit) resourceHoldings(
	ctx context.Context, resourceID, reference string,
) (holds *ResourceHolds, regs []TableRegistration) {
	if t.resourceHolds != nil {
		if h, err := t.resourceHolds.ResourceHolds(ctx, resourceID); err == nil && h.Any() {
			holds = &h
		}
	}
	return holds, t.resourceTables(ctx, reference)
}

// resourceTables reads the query-engine tables registered over the file. A
// deployment that cannot register tables has none.
func (t *Toolkit) resourceTables(ctx context.Context, reference string) []TableRegistration {
	if t.tables == nil {
		return nil
	}
	regs, err := t.tables.Tables(ctx, reference)
	if err != nil {
		return nil
	}
	return regs
}

// queryTableNames is the registrations as a sentence lists them: the qualified
// name a query writes, in the order they were reported.
func queryTableNames(regs []TableRegistration) []string {
	names := make([]string, 0, len(regs))
	for _, reg := range regs {
		names = append(names, reg.QueryTable)
	}
	return names
}

// dropResourceTables takes down the tables registered over a deleted file. A
// table over a file that is gone answers queries from a location nothing owns,
// which is the state the drop exists to prevent; it is best-effort by contract,
// because the file is already gone and an unrelated query-engine outage must
// not read as a broken delete.
func (t *Toolkit) dropResourceTables(ctx context.Context, resourceID string) {
	if t.tables == nil {
		return
	}
	t.tables.DropResourceTables(ctx, resourceID)
}

// resolveResourceTarget settles which file an action names, by reference or by
// address, and reports the first thing wrong in the caller's own vocabulary.
func (t *Toolkit) resolveResourceTarget(
	ctx context.Context, input manageResourceInput, claims resource.Claims,
) (*resource.Resource, *mcp.CallToolResult) {
	if ref := strings.TrimSpace(input.Reference); ref != "" {
		id, err := parseResourceReference(ref)
		if err != nil {
			return nil, toolkit.ErrorResult(err.Error())
		}
		res, err := t.resourceWriter.Get(ctx, id, claims)
		if err != nil {
			return nil, toolkit.ErrorResult(err.Error())
		}
		return res, nil
	}
	if err := requireAddress(input); err != nil {
		return nil, toolkit.ErrorResult(err.Error())
	}
	res, uri, err := t.resourceWriter.Locate(ctx, addressOf(input), claims)
	if err != nil {
		return nil, toolkit.ErrorResult(err.Error())
	}
	if res == nil {
		return nil, toolkit.ErrorResult("there is no managed resource at " + uri +
			" that you can see. Nothing was deleted.")
	}
	return res, nil
}

// requireAddress refuses a call that named neither of the two ways to say which
// file it means. Said here rather than left to the address validator because
// the caller's mistake is having named nothing, and "path is required" answers
// a question they were not asking.
func requireAddress(input manageResourceInput) error {
	if strings.TrimSpace(input.Path) != "" || strings.TrimSpace(input.Filename) != "" {
		return nil
	}
	return errors.New("name the file to act on: either its mcp:resource:<id> reference, or the address it " +
		"is filed at as scope, path and filename. Nothing was changed")
}

// deleteMessage says what the delete took with it.
func deleteMessage(out resourceDeleteOutput) string {
	msg := fmt.Sprintf("Deleted %s and its version history. The reference and the uri it was reachable by "+
		"now resolve to nothing.", out.URI)
	if out.Holds != nil {
		msg += " It was forced past what still pointed at it: " + strings.Join(out.Holds.Describe(), listSeparator) +
			". Each of those now points at a file that is not there."
	}
	// "unregistered" rather than "dropped": taking the table down is
	// best-effort by contract, and the registration is what definitively goes.
	names := queryTableNames(out.TableRegistrations)
	switch len(names) {
	case 0:
	case 1:
		msg += " The table registered over it was unregistered with it: " + names[0] + "."
	default:
		msg += fmt.Sprintf(" The %d tables registered over it were unregistered with it: %s.",
			len(names), strings.Join(names, listSeparator))
	}
	return msg
}

// addressOf reads the address half of the tool's input.
func addressOf(input manageResourceInput) toolkit.ResourceAddress {
	return toolkit.ResourceAddress{
		Scope: input.Scope, ScopeID: input.ScopeID, Path: input.Path, Filename: input.Filename,
	}
}

// recordOf renders a resource as get and list report it.
func recordOf(res *resource.Resource) *resourceRecord {
	return &resourceRecord{
		ResourceID:  res.ID,
		Reference:   knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		URI:         res.URI,
		Filename:    res.Filename,
		DisplayName: res.DisplayName,
		Description: res.Description,
		Tags:        res.Tags,
		Scope:       string(res.Scope),
		ScopeID:     res.ScopeID,
		Path:        res.Path,
		ContentType: res.MIMEType,
		SizeBytes:   res.SizeBytes,
		UploadedBy:  res.UploaderEmail,
		CreatedAt:   res.CreatedAt,
		UpdatedAt:   res.UpdatedAt,
	}
}
