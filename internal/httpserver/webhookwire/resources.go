package webhookwire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/webhook/compactor"
	"github.com/txn2/mcp-data-platform/internal/webhook/whimap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// systemPrincipal is who writes a compacted window: the platform, not a
// person.
const systemPrincipal = "system:webhooks"

// defaultAdminPersona is the administrator persona when the platform
// configures none, as the admin API assumes.
const defaultAdminPersona = "admin"

// recompactedSummary is the version note of a window written again.
const recompactedSummary = "Compacted again: events arrived after the window was first compacted"

// resourceWriter is the part of resourcewrite.Writer the windows use.
type resourceWriter interface {
	Create(ctx context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error)
	Replace(ctx context.Context, id string, up resource.RevisionUpload, claims resource.Claims) (*resource.Resource, int, error)
	Get(ctx context.Context, id string, claims resource.Claims) (*resource.Resource, error)
	Delete(ctx context.Context, id string, claims resource.Claims) (*resource.Resource, error)
}

// resourceIndex finds a resource by its address and lists a folder of a
// library. resource.Store satisfies it.
type resourceIndex interface {
	GetByURI(ctx context.Context, uri string) (*resource.Resource, error)
	List(ctx context.Context, filter resource.Filter) ([]resource.Resource, int, error)
}

// windowResources writes each compacted window as a managed resource in the
// folder webhooks/{source}/{dt}, named {window}.parquet.
type windowResources struct {
	w         resourceWriter
	byURI     resourceIndex
	uriScheme string
	// adminPersona is the library a source that names no persona writes its
	// windows to, which is what keeps them to administrators while leaving them
	// findable by them: search shows a caller the libraries of the personas
	// they belong to, never another principal's own.
	adminPersona string
	// external reports whether a persona is external. The administrator
	// persona never is, but one written to the store as external would give
	// the windows to the people the platform created accounts for, so they
	// land in the platform's own folder instead (#2257).
	external func(name string) bool
}

// systemClaims are the platform's authority to write the windows: an
// administrator's, so it may write any persona's library.
func systemClaims() resource.Claims {
	return resource.BuildClaims(systemPrincipal, "", "", nil, true)
}

// address is where a window's resource sits: the day's folder, and a file
// named for the window and minute the window starts at.
func address(source string, start time.Time) (folder, filename string) {
	dt, hh, mm := whlayout.PartitionValues(start)
	return "webhooks/" + source + "/" + dt, hh + "-" + mm + tableparquet.Extension
}

// personaOf is the persona whose library a source's windows are written to:
// its own, or the administrator persona.
func (h windowResources) personaOf(src whsource.Source) string {
	if src.Config.Persona != "" {
		return src.Config.Persona
	}
	if h.adminPersona != "" {
		return h.adminPersona
	}
	return defaultAdminPersona
}

// libraryOf is the scope a source's windows are written to: its persona's
// library, or the platform's own folder, which only administrators read, when
// a source naming no persona would otherwise write to an administrator
// persona marked external.
func (h windowResources) libraryOf(src whsource.Source) (scope resource.Scope, scopeID string) {
	name := h.personaOf(src)
	if src.Config.Persona == "" && h.external != nil && h.external(name) {
		return resource.ScopeUser, systemPrincipal
	}
	return resource.ScopePersona, name
}

// Put writes a window's file: a new version of the window's resource when it has
// one, and a new resource otherwise.
func (h windowResources) Put(ctx context.Context, src whsource.Source, start time.Time, existingID string, content []byte) (stored compactor.StoredWindow, err error) {
	source := src.Name
	scope, scopeID := h.libraryOf(src)
	id := existingID
	if id == "" {
		folder, filename := address(source, start)
		found, err := h.byURI.GetByURI(ctx, resource.BuildURI(h.uriScheme, scope, scopeID, folder, filename))
		if err != nil && !resource.IsNotFound(err) {
			return compactor.StoredWindow{}, fmt.Errorf("looking up the window's resource: %w", err)
		}
		if found != nil {
			id = found.ID
		}
	}
	if id != "" {
		res, _, err := h.w.Replace(ctx, id, resource.RevisionUpload{
			Content: bytes.NewReader(content), MIMEType: tableparquet.ContentType, ChangeSummary: recompactedSummary,
			Index: src.Config.Index,
		}, systemClaims())
		if err == nil {
			return compactor.StoredWindow{ResourceID: res.ID, Key: res.S3Key}, nil
		}
		if !errors.Is(err, resourcewrite.ErrNoSuchResource) {
			return compactor.StoredWindow{}, err //nolint:wrapcheck // the writer's message names the resource
		}
	}
	folder, filename := address(source, start)
	dt, hh, mm := whlayout.PartitionValues(start)
	at := dt + " " + hh + ":" + mm + " UTC"
	res, err := h.w.Create(ctx, resource.NewResource{
		Scope: scope, ScopeID: scopeID, Path: folder, Filename: filename,
		DisplayName: source + " " + at,
		Description: "Webhook events received by source " + source + " in the compaction window starting " + at + ".",
		Tags:        []string{"webhook", source},
		Content:     bytes.NewReader(content),
		MIMEType:    tableparquet.ContentType,
		Index:       src.Config.Index,
		Origin:      indexpolicy.OriginWebhook,
	}, systemClaims())
	if err != nil {
		return compactor.StoredWindow{}, err //nolint:wrapcheck // the writer's message names the resource
	}
	return compactor.StoredWindow{ResourceID: res.ID, Key: res.S3Key}, nil
}

// Key returns where a resource's current file is.
func (h windowResources) Key(ctx context.Context, id string) (key string, found bool, err error) {
	res, err := h.w.Get(ctx, id, systemClaims())
	if errors.Is(err, resourcewrite.ErrNoSuchResource) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err //nolint:wrapcheck // the writer's message names the resource
	}
	return res.S3Key, true, nil
}

// Delete removes a resource and every version of its file.
func (h windowResources) Delete(ctx context.Context, id string) error {
	_, err := h.w.Delete(ctx, id, systemClaims())
	if err == nil || errors.Is(err, resourcewrite.ErrNoSuchResource) {
		return nil
	}
	return err //nolint:wrapcheck // the writer's message names the resource
}

// PutFile writes a file of a source into its library at folder/filename: a
// new version of the file at that address when there is one, a new resource
// otherwise. An imap source's attachments are written this way (#2247).
func (h windowResources) PutFile(ctx context.Context, src whsource.Source, f whimap.File) (uri, id string, err error) {
	scope, scopeID := h.libraryOf(src)
	address := resource.BuildURI(h.uriScheme, scope, scopeID, f.Folder, f.Name)
	found, err := h.byURI.GetByURI(ctx, address)
	if err != nil && !resource.IsNotFound(err) {
		return "", "", fmt.Errorf("looking up %s: %w", address, err)
	}
	if found != nil {
		res, _, err := h.w.Replace(ctx, found.ID, resource.RevisionUpload{
			Content: bytes.NewReader(f.Data), MIMEType: f.ContentType, ChangeSummary: "Read again from the mailbox", Index: src.Config.Index,
		}, systemClaims())
		if err != nil {
			return "", "", err //nolint:wrapcheck // the writer's message names the resource
		}
		return res.URI, res.ID, nil
	}
	res, err := h.w.Create(ctx, resource.NewResource{
		Scope: scope, ScopeID: scopeID, Path: f.Folder, Filename: f.Name,
		DisplayName: f.Name,
		Description: "An attachment of a message the " + src.Name + " source read from its mailbox.",
		Tags:        []string{"webhook", "mail", src.Name},
		Content:     bytes.NewReader(f.Data),
		MIMEType:    f.ContentType,
		Index:       src.Config.Index,
		Origin:      indexpolicy.OriginWebhook,
	}, systemClaims())
	if err != nil {
		return "", "", err //nolint:wrapcheck // the writer's message names the resource
	}
	return res.URI, res.ID, nil
}

// attachmentsPage is how many attachments one listing removes.
const attachmentsPage = 200

// DeleteAttachments removes every attachment an imap source saved: the
// resources under its attachments folder in its library (#2247). Each page is
// listed again from the start, since what the last one held is gone.
func (h windowResources) DeleteAttachments(ctx context.Context, src whsource.Source) error {
	scope, scopeID := h.libraryOf(src)
	filter := resource.Filter{
		Scopes: []resource.ScopeFilter{{Scope: scope, ScopeID: scopeID}},
		Path:   whimap.AttachmentsFolder(src.Name), Limit: attachmentsPage,
	}
	removed := map[string]bool{}
	for {
		page, _, err := h.byURI.List(ctx, filter)
		if err != nil {
			return fmt.Errorf("listing the attachments of %s: %w", src.Name, err)
		}
		for _, r := range page {
			if removed[r.ID] {
				return fmt.Errorf("attachment %s of %s is still listed after its delete", r.ID, src.Name)
			}
			if err := h.Delete(ctx, r.ID); err != nil {
				return err
			}
			removed[r.ID] = true
		}
		if len(page) < attachmentsPage {
			return nil
		}
	}
}
