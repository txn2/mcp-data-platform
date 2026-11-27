package portal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceupload"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// resourceActionUploadLink issues a link files are PUT to (#2639): from an
// agent's shell, a file larger than a tool call reaches the library with no
// portal, no API key and no public object storage; from an external service,
// its output lands in the library by URL, several files to a link and up to a
// day after it was issued (#2844).
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

// MountUploadLinkPortal registers the portal's upload link routes (#2844) on
// mux, from the issuer this toolkit holds; nothing without one.
func (t *Toolkit) MountUploadLinkPortal(mux *http.ServeMux, extract resource.ClaimsExtractor) {
	if m, ok := t.uploadLinks.(interface {
		MountPortal(mux *http.ServeMux, extract resource.ClaimsExtractor)
	}); ok {
		m.MountPortal(mux, extract)
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
	// A saved script's run may ask for one to hand an external service
	// (#2844). Its recording keeps the link, and the recording is read by the
	// script's owner and administrators, who can write the folder already.
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
		Example: uploadExample(in.Filename, input.ContentType, issued.URL),
		Message: uploadMessage(issued),
	})
}

// uploadExample is the curl that PUTs one file to the link.
func uploadExample(filename, contentType, url string) string {
	if filename == "" {
		return "curl --fail -T 'report.pdf' -H 'Content-Type: " + contentType + "' '" + url + "/report.pdf'"
	}
	return "curl --fail -T " + shellQuoted(filename) + " -H 'Content-Type: " + contentType + "' '" + url + "'"
}

// uploadMessage says how the link is used.
func uploadMessage(issued resourceupload.Issued) string {
	where := "PUT the file to upload_url"
	if issued.NameInURL {
		where = "PUT each file to upload_url followed by /<filename>"
	}
	return where + ", from your shell or by giving the URL to the service that produces the files, before " +
		"expires_at. Each file is filed in this folder as you, with the checks a portal upload gets, and the answer " +
		"to each PUT names the resource's reference, which manage_resource action=extract and manage_site publish " +
		"take. The link takes max_files files of at most max_bytes each; a PUT that files nothing can be sent again. " +
		"It is listed, and can be revoked, on the portal's Resources page."
}

// uploadLinkRequest is the link input asks for, checked as a create of the
// same file is before it reads any content, or the refusal.
func uploadLinkRequest(input manageResourceInput, claims resource.Claims) (in resourceupload.IssueRequest, refusal string) {
	scope, scopeID := resolveResourceScope(input, claims)
	filename := ""
	if strings.TrimSpace(input.Filename) != "" {
		clean, err := resource.SanitizeFilename(input.Filename)
		if err != nil {
			return resourceupload.IssueRequest{}, "filename must be a plain file name: " + err.Error()
		}
		filename = clean
	}
	// A display name is optional: a file a link files with none is named for
	// itself (#2844).
	if err := validatePlacement(scope, scopeID, input); err != nil {
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
		MaxBytes: input.SizeBytes, TTL: time.Duration(input.TTLSeconds) * time.Second, MaxFiles: input.MaxFiles,
	}, ""
}

// shellQuoted is name as one shell word.
func shellQuoted(name string) string {
	return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'"
}
