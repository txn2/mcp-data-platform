package portal

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ResourceWriter writes a managed resource on an agent's behalf (#1487).
//
// A managed resource is the only kind of file an asset can reference, so
// without a writer the data half of a referencing asset can only be refreshed
// by a person at an upload form. This is the capability that makes an asset's
// numbers refreshable by the platform itself.
//
// The acting caller is not a parameter, the same way it is not one on
// TableRegistrar: both methods take the resource claims this toolkit derives
// from the call's own identity, so the tool cannot present an identity the REST
// surface would refuse, and the scope boundary a tool call meets is the one an
// upload meets.
type ResourceWriter interface {
	// Create files new content as a managed resource, refusing a scope the
	// caller may not write to.
	Create(ctx context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error)
	// Replace records new content as the resource's next revision, keeping the
	// id, the canonical URI and the filename, and returns the version number
	// the content was recorded as.
	Replace(
		ctx context.Context, id string, up resource.RevisionUpload, claims resource.Claims,
	) (*resource.Resource, int, error)
	// Get reads a resource the caller may see. A replacement resolves the file
	// before it decodes anything, so a reference naming a file that is not
	// there is refused without the payload being read, and so type detection
	// is given the stored filename rather than a name the caller offered --
	// the filename is what a replacement must not change.
	Get(ctx context.Context, id string, claims resource.Claims) (*resource.Resource, error)
}

// SetResourceWriter binds the writer behind manage_resource. Called by the
// composition root once the managed-resource layer exists, which is later than
// toolkit construction; without it the tool reports that the deployment has no
// managed-resource layer, which is what a deployment with no database or no
// blob storage can do.
func (t *Toolkit) SetResourceWriter(w ResourceWriter) {
	t.resourceWriter = w
}

// manageResourceInput defines the input for manage_resource.
type manageResourceInput struct {
	Action string `json:"action"`
	// Reference names the resource to replace, in the vocabulary every other
	// tool uses: mcp:resource:<id>.
	Reference string `json:"reference,omitempty"`
	// Placement and description, for create.
	Filename    string   `json:"filename,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Path        string   `json:"path,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	ScopeID     string   `json:"scope_id,omitempty"`
	// Content carries text, ContentBase64 carries bytes; exactly one is given.
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	// ChangeSummary is what the version history shows beside a replacement.
	ChangeSummary string `json:"change_summary,omitempty"`
}

// resourceOutput is what both actions report. It leads with the two names the
// caller needs next -- the reference other tools take, and the mcp:// URI
// save_asset's `resources` argument takes -- because a write whose result
// cannot be handed to the next call is a write the caller has to go looking
// for.
type resourceOutput struct {
	ResourceID  string `json:"resource_id"`
	Reference   string `json:"reference"`
	URI         string `json:"uri"`
	Filename    string `json:"filename"`
	DisplayName string `json:"display_name"`
	Scope       string `json:"scope"`
	ScopeID     string `json:"scope_id,omitempty"`
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	// Version is the version number a replacement was recorded as, and is
	// absent on a create: a create records version 1 only where the deployment
	// keeps a version trail, and reporting a number the history may not hold
	// would be worse than reporting none.
	Version int `json:"version,omitempty"`
	// Tables is what a replacement did to the tables registered over the
	// file (#1536): one sentence per table, saying it followed onto the new
	// version, or is pinned and now behind it and how to move it. Absent when
	// no table is registered over the file.
	Tables  []string `json:"tables,omitempty"`
	Message string   `json:"message"`
}

// handleManageResource dispatches a manage_resource call.
func (t *Toolkit) handleManageResource(
	ctx context.Context, _ *mcp.CallToolRequest, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	if t.resourceWriter == nil {
		return toolkit.ErrorResult(resourceWriteUnavailable), nil, nil
	}
	if resolveOwnerID(ctx) == anonymousUserName {
		return toolkit.ErrorResult(resourceIdentityRequired), nil, nil
	}
	switch input.Action {
	case resourceActionCreate:
		return t.handleCreateResource(ctx, input)
	case resourceActionReplace:
		return t.handleReplaceResourceContent(ctx, input)
	default:
		return toolkit.ErrorResult(fmt.Sprintf(
			"invalid action %q: must be one of: create, replace_content", input.Action)), nil, nil
	}
}

// handleCreateResource files new content as a managed resource.
func (t *Toolkit) handleCreateResource(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	claims := refClaims(ctx)
	scope, scopeID := resolveResourceScope(input, claims)

	filename, err := resource.SanitizeFilename(input.Filename)
	if err != nil {
		return toolkit.ErrorResult("filename is required and must be a plain file name: " + err.Error()), nil, nil
	}
	if err := validateResourcePlacement(scope, scopeID, input); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	// Checked before the payload is decoded: a create with nothing to store it
	// under is refused whatever its size.
	if strings.TrimSpace(input.ContentType) == "" {
		return toolkit.ErrorResult(resourceContentTypeRequired), nil, nil
	}

	data, mimeType, errResult := t.resolveContent(input, filename, input.ContentType)
	if errResult != nil {
		return errResult, nil, nil
	}

	res, err := t.resourceWriter.Create(ctx, resource.NewResource{
		Scope: scope, ScopeID: scopeID,
		Path: input.Path, Filename: filename,
		DisplayName: input.DisplayName, Description: input.Description,
		Tags: normalizeTags(input.Tags),
		Data: data, MIMEType: mimeType, DeclaredMIMEType: input.ContentType,
	}, claims)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(resourceReport(res, 0,
		"Created. Reference it from an asset by passing the uri above in save_asset's 'references', and "+
			"refresh its contents later with manage_resource action=replace_content, which keeps this id, "+
			"uri and filename."))
}

// handleReplaceResourceContent records new content as the resource's next
// revision.
//
// The id, the canonical URI and the filename are unchanged by contract, which
// is the whole point: every asset referencing the file resolves to the new
// bytes without being re-saved, and every citation and prompt attachment
// pointing at it keeps resolving.
func (t *Toolkit) handleReplaceResourceContent(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	id, err := parseResourceReference(input.Reference)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	claims := refClaims(ctx)
	existing, err := t.resourceWriter.Get(ctx, id, claims)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	// Detection is given the stored filename, never one the caller offered: the
	// filename is embedded in the canonical URI, so a replacement that renamed
	// the file would break every reference to it -- the exact breakage this
	// action exists to avoid.
	//
	// The type the resource already carries stands in for a declaration the
	// caller did not make, for the same reason: a replacement refreshes a file
	// that is already referenced, and re-deciding its family from the bytes
	// would reclassify it under every reference at once.
	//
	// It stands in only where it would be accepted as a declaration. A file
	// stored under a type the deny list has since grown to cover would
	// otherwise be refused a replacement over a type its caller never sent,
	// which is the one way inheriting it could take away a write that worked
	// before; detection settles that file exactly as it did before.
	declared := strings.TrimSpace(input.ContentType)
	if declared == "" && resource.ValidateMIMEType(existing.MIMEType) == nil {
		declared = existing.MIMEType
	}
	data, mimeType, errResult := t.resolveContent(input, existing.Filename, declared)
	if errResult != nil {
		return errResult, nil, nil
	}

	summary := strings.TrimSpace(input.ChangeSummary)
	if summary == "" {
		summary = defaultResourceChangeSummary
	}

	res, version, err := t.resourceWriter.Replace(ctx, id, resource.RevisionUpload{
		Data: data, MIMEType: mimeType, ChangeSummary: summary,
	}, claims)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	// The revision moved the file's head, and every table registered over the
	// file either followed it or is now behind it (#1536). Both are said here,
	// because the write is where a table falling behind happens, and a result
	// that reported only the version would leave a script's run succeeding
	// over a table serving last month's file.
	out := resourceReport(res, version,
		fmt.Sprintf("Content replaced and recorded as version %d, restorable from the file's version history. "+
			"The id, uri and filename are unchanged, so every asset referencing this file now serves the new "+
			"bytes without being re-saved.", version))
	out.Tables = t.followResourceTables(ctx, id, version)
	if len(out.Tables) > 0 {
		out.Message += " " + strings.Join(out.Tables, " ")
	}
	return toolkit.JSONResultTyped(out)
}

// resolveContent decodes the write's bytes and settles the type they are
// stored under.
//
// declared is what the write claims the bytes are: the caller's own
// content_type on a create, where it is required, and on a replacement the
// caller's when they sent one and the type the resource already carries when
// they did not.
//
// A generic declaration is still replaced by the type detected from the
// content, so a file stored years ago under application/octet-stream is
// refreshed into the family its bytes are. Detection cannot go the other way
// for every family -- SVG, HTML, JSX and Markdown are never named from content
// (see pkg/contenttype) -- which is why a declaration is required rather than
// inferred. The resolved type is validated as well as the declared one:
// detection over caller bytes is the other way a type reaches the store.
func (t *Toolkit) resolveContent(
	input manageResourceInput, filename, declared string,
) (data []byte, mimeType string, errResult *mcp.CallToolResult) {
	data, err := decodeResourceContent(input)
	if err != nil {
		return nil, "", toolkit.ErrorResult(err.Error())
	}
	if t.maxContentSize > 0 && len(data) > t.maxContentSize {
		return nil, "", toolkit.ErrorResult(fmt.Sprintf(
			"content size %d exceeds maximum %d bytes. A file this large has to be uploaded through the "+
				"portal's resource library rather than passed through a tool call.",
			len(data), t.maxContentSize))
	}
	if err := resource.ValidateMIMEType(declared); err != nil {
		return nil, "", toolkit.ErrorResult(err.Error())
	}
	mimeType = contenttype.DetectFileBytes(declared, filename, data)
	if err := resource.ValidateMIMEType(mimeType); err != nil {
		return nil, "", toolkit.ErrorResult(err.Error())
	}
	return data, mimeType, nil
}

// decodeResourceContent reads the write's bytes from whichever field carries
// them. Exactly one is given: text in content, bytes in content_base64.
func decodeResourceContent(input manageResourceInput) ([]byte, error) {
	hasText, hasBytes := input.Content != "", input.ContentBase64 != ""
	switch {
	case hasText && hasBytes:
		return nil, errors.New("pass content or content_base64, not both: content carries the file as text, " +
			"content_base64 carries it as base64-encoded bytes")
	case hasBytes:
		// Both the padded and the unpadded standard alphabets are accepted,
		// because models emit either -- the same allowance the API gateway's
		// multipart part already makes. Refusing an unpadded payload would
		// refuse a file for the way it was spelled rather than for what it is.
		if data, err := base64.StdEncoding.DecodeString(input.ContentBase64); err == nil {
			return data, nil
		}
		data, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(input.ContentBase64, "="))
		if err != nil {
			return nil, errors.New("content_base64 is not valid base64: " + err.Error())
		}
		return data, nil
	case hasText:
		return []byte(input.Content), nil
	default:
		return nil, errors.New("content is required: pass the file as text in 'content', or as " +
			"base64-encoded bytes in 'content_base64' for a binary file such as an image or a PDF")
	}
}

// resolveResourceScope settles where a create files the resource. The default
// is the caller's own user scope, the one place every authenticated caller may
// write; a persona or global resource is named explicitly, because it is
// visible to people other than its author.
//
// "The caller's own" is the person, not the principal. A managed-script run
// authenticates as script:<name>, and defaulting to that would file the
// resource in a library belonging to nobody -- present to the run and absent
// from its author's Resources page, which is where the person who scheduled it
// will look. It defaults to the address the run acts for instead (#1419).
func resolveResourceScope(input manageResourceInput, claims resource.Claims) (scope resource.Scope, scopeID string) {
	scope = resource.Scope(strings.TrimSpace(input.Scope))
	scopeID = strings.TrimSpace(input.ScopeID)
	if scope == "" {
		scope = resource.ScopeUser
	}
	if scope == resource.ScopeUser && scopeID == "" {
		scopeID = cmp.Or(claims.OnBehalfOf, claims.Sub)
	}
	if scope == resource.ScopeGlobal {
		scopeID = ""
	}
	return scope, scopeID
}

// validateResourcePlacement checks the fields a create needs beyond its
// content, reporting the first problem in the caller's own vocabulary.
func validateResourcePlacement(scope resource.Scope, scopeID string, input manageResourceInput) error {
	if err := resource.ValidateScope(scope, scopeID); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := resource.ValidatePath(input.Path); err != nil {
		return fmt.Errorf("%w. A path is the folder chain the file is filed under inside the library, "+
			"for example \"datasets\" or \"datasets/media-manager/shows\"", err)
	}
	if err := resource.ValidateDisplayName(input.DisplayName); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := resource.ValidateDescription(input.Description); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := resource.ValidateTags(normalizeTags(input.Tags)); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	return nil
}

// normalizeTags substitutes an empty list for an absent one, which is what the
// store records rather than a null.
func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// parseResourceReference resolves the mcp:resource: reference a replacement
// names into the id it acts on. A well-formed reference to something else is
// refused by name, because naming what was passed tells the caller what to pass
// instead.
func parseResourceReference(reference string) (string, error) {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return "", errors.New("reference is required for replace_content: pass the mcp:resource:<id> reference " +
			"a search hit or a create reported, verbatim")
	}
	ref, err := knowledgepage.ParseEntityRef(trimmed)
	if err != nil {
		return "", fmt.Errorf("reference %q is not a reference this platform issues: %s", trimmed, err.Error())
	}
	if ref.TargetType != knowledgepage.RefTargetResource {
		return "", fmt.Errorf("reference %q names a target of type %q, not %q. Only a managed resource has "+
			"content this tool can replace; a saved asset's content is edited with manage_asset",
			trimmed, ref.TargetType, knowledgepage.RefTargetResource)
	}
	return ref.ResourceID, nil
}

// resourceReport renders a written resource as the tool reports it.
func resourceReport(res *resource.Resource, version int, message string) resourceOutput {
	return resourceOutput{
		ResourceID:  res.ID,
		Reference:   knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		URI:         res.URI,
		Filename:    res.Filename,
		DisplayName: res.DisplayName,
		Scope:       string(res.Scope),
		ScopeID:     res.ScopeID,
		Path:        res.Path,
		ContentType: res.MIMEType,
		SizeBytes:   res.SizeBytes,
		Version:     version,
		Message:     message,
	}
}

// resourceWriteUnavailable is what manage_resource says on a deployment with
// no managed-resource layer. It names the missing piece rather than reporting a
// generic failure, and it is said instead of a write, never after one.
const resourceWriteUnavailable = "This deployment has no managed-resource library to write to: it needs a " +
	"database and an S3 connection for resource storage. Ask an administrator to configure one. Nothing was saved."

// resourceIdentityRequired is what an unauthenticated call is told. A resource
// records who uploaded it and decides its scope on that, so there is nobody to
// file it under.
const resourceIdentityRequired = "Writing a managed resource needs a signed-in identity. This session has none, " +
	"so there is no scope to file the file under and no author to record."

// resourceContentTypeRequired is what a create with no content_type is told.
//
// The type is required rather than guessed because a create is the one moment
// it is known for certain -- the caller chose the bytes -- and because
// detection cannot recover it afterwards for the families an agent writes
// most: SVG, HTML, JSX and Markdown are all stored text/plain when nothing is
// declared, and a file served as text/plain under nosniff is a broken image or
// an unrendered document wherever an asset references it, with nothing
// reporting a problem (#1508).
var resourceContentTypeRequired = "content_type is required for create: name the media type the bytes are, " +
	"for example image/svg+xml for an SVG, text/markdown for a Markdown document, text/html for an HTML " +
	"page, text/csv for a CSV, or image/png for a PNG. It is not detected for you: SVG, HTML and Markdown " +
	"all read as plain text to a byte sniffer, and a file stored as text/plain is served under nosniff, " +
	"which stops a browser rendering it as an image or a document. Read " +
	knowledgepage.BuiltinReference(knowledgepage.BuiltinSlugContentTypes) +
	" for the types this platform stores. Nothing was saved."

// defaultResourceChangeSummary labels a replacement whose caller supplied no
// change_summary.
const defaultResourceChangeSummary = "Content replaced via manage_resource"
