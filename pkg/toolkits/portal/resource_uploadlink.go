package portal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceupload"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// resourceActionUploadLink issues a single-use link an agent PUTs a file to
// from its shell (#2639): a file larger than a tool call reaches the library
// with no portal, no API key and no public object storage.
const resourceActionUploadLink = "upload_link"

// UploadLinks issues upload links (resourceupload.Service).
type UploadLinks interface {
	Issue(ctx context.Context, in resourceupload.IssueRequest, claims resource.Claims, userID string) (resourceupload.Issued, error)
}

// SetUploadLinks gives manage_resource action=upload_link its issuer; without
// one the action says the deployment cannot issue them.
func (t *Toolkit) SetUploadLinks(u UploadLinks) {
	t.uploadLinks = u
}

// MountUploadLinks registers the route a link is PUT to (#2639) on mux, from
// the issuer this toolkit holds; nothing without one.
func (t *Toolkit) MountUploadLinks(mux *http.ServeMux) {
	if m, ok := t.uploadLinks.(interface{ Mount(mux *http.ServeMux) }); ok {
		m.Mount(mux)
	}
}

// uploadLinkOutput is a link as the caller is handed it.
type uploadLinkOutput struct {
	resourceupload.Issued
	Scope    string `json:"scope"`
	ScopeID  string `json:"scope_id,omitempty"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Example  string `json:"example"`
	Message  string `json:"message"`
}

// handleUploadLink issues a link for the address and size input names, after
// the checks a create of the same file makes before it reads any content.
func (t *Toolkit) handleUploadLink(ctx context.Context, input manageResourceInput) (*mcp.CallToolResult, any, error) {
	if t.uploadLinks == nil {
		return toolkit.ErrorResult("this deployment cannot issue upload links: it has no resource library or no database to keep them"), nil, nil
	}
	if pc := middleware.GetPlatformContext(ctx); mcpcontext.GetSource(ctx) == mcpcontext.SourceScript ||
		(pc != nil && strings.HasPrefix(pc.UserID, "script:")) {
		// A run has no shell to PUT from, and its recording keeps every answer
		// it is given, the link's token among them.
		return toolkit.ErrorResult("a saved script's run cannot use an upload link: it writes the file with " +
			"manage_resource action=create, or lands an export with platform.export"), nil, nil
	}
	claims := refClaims(ctx)
	in, refusal := uploadLinkRequest(input, claims)
	if refusal != "" {
		return toolkit.ErrorResult(refusal), nil, nil
	}
	issued, err := t.uploadLinks.Issue(ctx, in, claims, resolveOwnerID(ctx))
	if err != nil {
		msg := err.Error()
		if errors.Is(err, resourceupload.ErrRefused) {
			msg = strings.TrimSuffix(msg, ": "+resourceupload.ErrRefused.Error())
		}
		return toolkit.ErrorResult(msg), nil, nil
	}
	return toolkit.JSONResultTyped(uploadLinkOutput{
		Issued: issued, Scope: string(in.Scope), ScopeID: in.ScopeID, Path: in.Path, Filename: in.Filename,
		Example: "curl --fail -T " + shellQuoted(in.Filename) + " -H 'Content-Type: " + input.ContentType + "' '" + issued.URL + "'",
		Message: "PUT the file to upload_url from your shell, once, before expires_at; it is filed at this address as you, " +
			"with the checks a portal upload gets. The answer names the resource's reference, which manage_resource " +
			"action=extract and manage_site publish take. The link is good for one upload and at most max_bytes.",
	})
}

// uploadLinkRequest is the link input asks for, checked as a create of the
// same file is before it reads any content, or the refusal.
func uploadLinkRequest(input manageResourceInput, claims resource.Claims) (in resourceupload.IssueRequest, refusal string) {
	scope, scopeID := resolveResourceScope(input, claims)
	filename, err := resource.SanitizeFilename(input.Filename)
	if err != nil {
		return resourceupload.IssueRequest{}, "filename is required and must be a plain file name: " + err.Error()
	}
	if err := validateResourcePlacement(scope, scopeID, input); err != nil {
		return resourceupload.IssueRequest{}, err.Error()
	}
	if strings.TrimSpace(input.ContentType) == "" {
		return resourceupload.IssueRequest{}, resourceContentTypeRequired
	}
	if err := resource.ValidateMIMEType(input.ContentType); err != nil {
		return resourceupload.IssueRequest{}, err.Error()
	}
	if err := validateIfExists(input.IfExists); err != nil {
		return resourceupload.IssueRequest{}, err.Error()
	}
	return resourceupload.IssueRequest{
		Scope: scope, ScopeID: scopeID, Path: input.Path, Filename: filename,
		DisplayName: input.DisplayName, Description: input.Description, Tags: normalizeTags(input.Tags),
		ContentType: input.ContentType, IfExists: strings.TrimSpace(input.IfExists),
		MaxBytes: input.SizeBytes, TTL: time.Duration(input.TTLSeconds) * time.Second,
	}, ""
}

// shellQuoted is name as one shell word.
func shellQuoted(name string) string {
	return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'"
}
