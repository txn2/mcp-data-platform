package portal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ResourceExtractor writes the members of an archive stored as a managed
// resource out as managed resources of their own (#1879).
//
// The acting caller is the claims this toolkit derives from the call, as on
// ResourceWriter. A failure part way through returns what was written before it
// alongside the error, so the result never leaves a written file unreported.
type ResourceExtractor interface {
	ExtractArchive(
		ctx context.Context, req ArchiveExtraction, claims resource.Claims,
	) (*ExtractedArchive, error)
}

// ArchiveExtraction is what manage_resource extract asks of the extractor.
type ArchiveExtraction struct {
	ArchiveID     string
	Scope         string
	ScopeID       string
	Path          string
	Members       string
	Filename      string
	Replace       bool
	Description   string
	Tags          []string
	ChangeSummary string
}

// ExtractedMember is one member written: its name in the archive, and the
// resource it landed as, in the result shape every resource landing reports.
type ExtractedMember struct {
	Member string `json:"member"`
	toolkit.ResourceLanding
}

// ExtractedArchive is what an extraction wrote. On a failure part way through,
// Members are the ones written before it.
type ExtractedArchive struct {
	Format  string
	Members []ExtractedMember
	Skipped []string
}

// SetResourceExtractor binds the extractor behind manage_resource extract.
// Without it the action reports that the deployment cannot extract archives.
func (t *Toolkit) SetResourceExtractor(x ResourceExtractor) {
	t.resourceExtractor = x
}

// resourceExtractUnavailable is what extract answers on a deployment whose
// resource library cannot read part of a stored file.
const resourceExtractUnavailable = "This deployment cannot extract archives: it needs a managed-resource " +
	"library whose storage can be read by range. Nothing was written."

// extractOutput is what an extraction reports: the archive it read, and each
// member it wrote as the resource the next call takes.
type extractOutput struct {
	// Archive is the mcp:resource:<id> reference of the archive read.
	Archive string `json:"archive"`
	Format  string `json:"format"`
	// Members are the files written, in archive order, each with its
	// reference, uri, version and what the write did to tables over it.
	Members []ExtractedMember `json:"members"`
	// Skipped are entries that are not regular files, for which nothing was
	// written.
	Skipped []string `json:"skipped,omitempty"`
	Message string   `json:"message"`
}

// handleExtractResource writes an archive's members out as managed resources.
func (t *Toolkit) handleExtractResource(
	ctx context.Context, input manageResourceInput,
) (*mcp.CallToolResult, any, error) {
	if t.resourceExtractor == nil {
		return toolkit.ErrorResult(resourceExtractUnavailable), nil, nil
	}
	req, err := extractionOf(input)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out, err := t.resourceExtractor.ExtractArchive(ctx, req, refClaims(ctx))
	if err != nil {
		return toolkit.ErrorResult(extractFailure(err, out)), nil, nil
	}
	return toolkit.JSONResultTyped(extractOutput{
		Archive: strings.TrimSpace(input.Reference),
		Format:  out.Format,
		Members: out.Members,
		Skipped: out.Skipped,
		Message: extractMessage(out),
	})
}

// extractionOf validates the arguments an extract needs and builds the request.
func extractionOf(input manageResourceInput) (ArchiveExtraction, error) {
	if strings.TrimSpace(input.Reference) == "" {
		return ArchiveExtraction{}, errors.New("reference is required for extract: pass the " +
			"mcp:resource:<id> reference of the archive, as a create, an export with a resource destination, " +
			"or a search hit reported it")
	}
	id, err := parseResourceReference(input.Reference)
	if err != nil {
		return ArchiveExtraction{}, err
	}
	if strings.TrimSpace(input.Path) == "" {
		return ArchiveExtraction{}, errors.New("path is required for extract: it is the folder the " +
			"members are filed under, for example pipelines/feed/staging; a member's own folders are " +
			"created beneath it")
	}
	if err := validateIfExists(input.IfExists); err != nil && !replaceIfExists(input.IfExists) {
		return ArchiveExtraction{}, err
	}
	return ArchiveExtraction{
		ArchiveID: id,
		Scope:     input.Scope, ScopeID: input.ScopeID, Path: input.Path,
		Members:       input.Members,
		Filename:      input.Filename,
		Replace:       replaceIfExists(input.IfExists),
		Description:   input.Description,
		Tags:          normalizeTags(input.Tags),
		ChangeSummary: input.ChangeSummary,
	}, nil
}

// extractMessage says what the extraction wrote.
func extractMessage(out *ExtractedArchive) string {
	created, revised := 0, 0
	var changes []string
	for _, m := range out.Members {
		if m.Created {
			created++
		} else {
			revised++
		}
		changes = append(changes, m.TableChanges...)
	}
	msg := fmt.Sprintf("Extracted %d files from the %s archive: %d created, %d recorded as the next version "+
		"of the file already at their address. Register a member as a table with manage_table action=register "+
		"on its reference; with follow left on, extracting to the same address again moves the table to the "+
		"new version.", len(out.Members), out.Format, created, revised)
	if len(out.Skipped) > 0 {
		msg += " Skipped because they are links or devices rather than files: " +
			strings.Join(out.Skipped, ", ") + "."
	}
	return strings.Join(append([]string{msg}, changes...), " ")
}

// extractFailure is the error an extraction answers, naming the members it had
// already written when it stopped part way through.
func extractFailure(err error, out *ExtractedArchive) string {
	if out == nil || len(out.Members) == 0 {
		return err.Error() + ". Nothing was written."
	}
	written := make([]string, 0, len(out.Members))
	for _, m := range out.Members {
		written = append(written, fmt.Sprintf("%s as %s (%s, version %d)", m.Member, m.URI, m.Reference, m.Version))
	}
	return fmt.Sprintf("%s. The extraction stopped there; the member being written when it failed was not "+
		"stored, and these %d were written before it: %s.", err.Error(), len(written), strings.Join(written, "; "))
}
