package attachserve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeAttachments is an in-memory prompt.AttachmentStore.
type fakeAttachments struct {
	byPrompt map[string][]prompt.Attachment
	listErr  error
}

func (*fakeAttachments) Attach(context.Context, prompt.Attachment) error { return nil }
func (*fakeAttachments) Detach(context.Context, string, string) error    { return nil }

func (f *fakeAttachments) ListByPrompt(_ context.Context, id string) ([]prompt.Attachment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byPrompt[id], nil
}

func (*fakeAttachments) ListByResource(context.Context, string) ([]string, error) { return nil, nil }
func (*fakeAttachments) Reorder(context.Context, string, []string) error          { return nil }

// fakeResources is an in-memory resource.Store.
type fakeResources struct {
	byID   map[string]*resource.Resource
	getErr error
}

func (*fakeResources) Insert(context.Context, resource.Resource) error { return nil }

func (f *fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	res, ok := f.byID[id]
	if !ok {
		// The Postgres store reports a missing row as a wrapped
		// sql.ErrNoRows, not as (nil, nil); a fake that returned nil would
		// let broken-link handling pass here and fail in production.
		return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
	}
	return res, nil
}

func (*fakeResources) GetByIDs(context.Context, []string) (map[string]*resource.Resource, error) {
	return map[string]*resource.Resource{}, nil
}

func (*fakeResources) GetByURI(context.Context, string) (*resource.Resource, error) {
	return nil, nil //nolint:nilnil // interface contract: not-found is (nil, nil)
}

func (*fakeResources) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}
func (*fakeResources) Update(context.Context, string, resource.Update) error { return nil }
func (*fakeResources) Move(context.Context, string, resource.Move) error {
	return errors.New("fakeResources does not move resources")
}
func (*fakeResources) Delete(context.Context, string) error { return nil }

// fakeBlobs is an in-memory blob backend keyed by S3 key.
type fakeBlobs struct {
	byKey map[string]string
	err   error
}

func (f *fakeBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	if f.err != nil {
		return nil, "", f.err
	}
	text, ok := f.byKey[key]
	if !ok {
		return nil, "", errors.New("no such key")
	}
	return []byte(text), "", nil
}

// testResolver builds a resolver over the standard fixture: a global markdown
// template that inlines, a global PNG that links, and a persona-scoped rubric
// only analysts can read.
func testResolver(t *testing.T, links []prompt.Attachment) *Resolver {
	t.Helper()
	res := &fakeResources{byID: map[string]*resource.Resource{
		"tpl": {
			ID: "tpl", Scope: resource.ScopeGlobal, DisplayName: "Q4 Template",
			Description: "Fill every section", MIMEType: "text/markdown", SizeBytes: 21,
			S3Key: "k/tpl.md", URI: "mcp://global/templates/q4.md",
		},
		"logo": {
			ID: "logo", Scope: resource.ScopeGlobal, DisplayName: "Brand Logo",
			MIMEType: "image/png", SizeBytes: 4096,
			S3Key: "k/logo.png", URI: "mcp://global/brand/logo.png",
		},
		"rubric": {
			ID: "rubric", Scope: resource.ScopePersona, ScopeID: "analyst", DisplayName: "Analyst Rubric",
			Description: "secret", MIMEType: "text/markdown", SizeBytes: 10,
			S3Key: "k/rubric.md", URI: "mcp://persona/analyst/rubric.md",
		},
	}}
	return New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{"p1": links}},
		Resources:   res,
		Blobs:       &fakeBlobs{byKey: map[string]string{"k/tpl.md": "# Q4\n\n## Findings\n", "k/rubric.md": "rubric"}},
		Bucket:      "bkt",
	})
}

// TestResolveEmbedsTextAndLinksBinary is acceptance criterion 1 at the resolver
// level: a text template comes back with its contents inline, a binary asset
// comes back as a link.
func TestResolveEmbedsTextAndLinksBinary(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "tpl", Position: 0},
		{PromptID: "p1", ResourceID: "logo", Position: 1},
	})

	got := r.Resolve(context.Background(), "p1", resource.Claims{Sub: "u1"})
	require.Len(t, got, 2)

	assert.Equal(t, AvailableEmbedded, got[0].Availability)
	assert.Equal(t, "# Q4\n\n## Findings\n", got[0].Text, "the template's contents must reach the agent")
	assert.Equal(t, "mcp://global/templates/q4.md", got[0].URI)

	assert.Equal(t, AvailableLinked, got[1].Availability)
	assert.Empty(t, got[1].Text, "a binary asset must never be inlined")
	assert.Equal(t, int64(4096), got[1].SizeBytes)
}

// TestResolvePreservesAuthoredOrder proves the order the author set is the
// order the agent receives: an SOP that says "fill the template, then check the
// rubric" depends on it.
func TestResolvePreservesAuthoredOrder(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "logo", Position: 0},
		{PromptID: "p1", ResourceID: "tpl", Position: 1},
	})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 2)
	assert.Equal(t, "logo", got[0].ResourceID)
	assert.Equal(t, "tpl", got[1].ResourceID)
}

// TestResolveWithholdsUnreadableAttachment is acceptance criterion 3: a caller
// who cannot read an attachment gets the prompt with that attachment marked
// unavailable, and nothing about it leaks.
func TestResolveWithholdsUnreadableAttachment(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "rubric"}})

	got := r.Resolve(context.Background(), "p1", resource.Claims{Sub: "u1", Personas: []string{"engineer"}})
	require.Len(t, got, 1)
	assert.Equal(t, UnavailableForbidden, got[0].Availability)
	assert.Empty(t, got[0].Text, "contents must not leak")
	assert.Empty(t, got[0].DisplayName, "the name must not leak either")
	assert.Empty(t, got[0].Description)
	assert.Empty(t, got[0].URI)
	assert.Zero(t, got[0].SizeBytes)
}

// TestResolveDeliversToPermittedPersona is the other half of the check above:
// the rule withholds from outsiders without withholding from the audience.
func TestResolveDeliversToPermittedPersona(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "rubric"}})
	got := r.Resolve(context.Background(), "p1", resource.Claims{Sub: "u1", Personas: []string{"analyst"}})
	require.Len(t, got, 1)
	assert.Equal(t, AvailableEmbedded, got[0].Availability)
	assert.Equal(t, "rubric", got[0].Text)
}

// TestResolveDeletedResourceDegrades is acceptance criterion 4: deleting an
// attached resource leaves the prompt serving, with the attachment noted.
func TestResolveDeletedResourceDegrades(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "deleted-long-ago"}})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 1)
	assert.Equal(t, UnavailableMissing, got[0].Availability)
	assert.Equal(t, "deleted-long-ago", got[0].ResourceID,
		"the id survives so an author can find and remove the dangling link")
}

// TestResolveMetadataReadFailureIsNotReportedAsDeleted separates the two
// failure modes the Postgres store expresses as errors: a missing row must read
// as a deleted resource, and any other failure must not, because telling the
// agent its material is gone when the database merely blinked is a different
// and worse lie.
func TestResolveMetadataReadFailureIsNotReportedAsDeleted(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "tpl"}},
		}},
		Resources: &fakeResources{getErr: errors.New("db down")},
	})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 1)
	assert.Equal(t, UnavailableUnreadable, got[0].Availability)
	assert.NotEqual(t, UnavailableMissing, got[0].Availability)
}

// TestContentOmitsLinkWithoutURI proves an attachment whose metadata never
// resolved is counted as undelivered rather than emitted as an empty
// resource_link, which no client could follow.
func TestContentOmitsLinkWithoutURI(t *testing.T) {
	content := Content([]Resolved{{ResourceID: "x", Availability: UnavailableUnreadable}})
	require.Len(t, content, 1)
	note, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, note.Text, "could not be read")
	assert.Contains(t, note.Text, "1 attached material was not delivered", "the note must agree in number")
}

// TestResolveBlobFailureFallsBackToLink proves an unreachable blob costs the
// contents, not the prompt: the caller still gets a link they can retry.
func TestResolveBlobFailureFallsBackToLink(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "tpl"}},
		}},
		Resources: &fakeResources{byID: map[string]*resource.Resource{
			"tpl": {ID: "tpl", Scope: resource.ScopeGlobal, MIMEType: "text/markdown", SizeBytes: 5, S3Key: "k", URI: "u"},
		}},
		Blobs:  &fakeBlobs{err: errors.New("s3 down")},
		Bucket: "bkt",
	})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 1)
	assert.Equal(t, UnavailableUnreadable, got[0].Availability)
	assert.Equal(t, "u", got[0].URI, "the link survives so the client can read it directly")
}

// TestResolveOversizedTextLinks proves the inline threshold is what decides
// between embedding and linking, not the media type alone.
func TestResolveOversizedTextLinks(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "big"}},
		}},
		Resources: &fakeResources{byID: map[string]*resource.Resource{
			"big": {ID: "big", Scope: resource.ScopeGlobal, MIMEType: "text/markdown", SizeBytes: 100, S3Key: "k", URI: "u"},
		}},
		Blobs:       &fakeBlobs{byKey: map[string]string{"k": "x"}},
		Bucket:      "bkt",
		InlineLimit: 50,
	})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 1)
	assert.Equal(t, AvailableLinked, got[0].Availability)
}

// TestResolveStoreFailureServesPromptWithoutMaterials proves an attachment
// store outage degrades to a prompt with no materials rather than a failure:
// the procedure is still worth serving.
func TestResolveStoreFailureServesPromptWithoutMaterials(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{listErr: errors.New("db down")},
		Resources:   &fakeResources{},
	})
	assert.Nil(t, r.Resolve(context.Background(), "p1", resource.Claims{}))
}

// TestResolveNilAndEmptyCases covers the shapes every serving site relies on
// being safe: an unbuilt resolver, and a prompt with no id.
func TestResolveNilAndEmptyCases(t *testing.T) {
	assert.Nil(t, New(Deps{}), "a resolver without stores is not constructible")

	var nilResolver *Resolver
	assert.Nil(t, nilResolver.Resolve(context.Background(), "p1", resource.Claims{}))

	r := testResolver(t, nil)
	assert.Nil(t, r.Resolve(context.Background(), "", resource.Claims{}))
	assert.Nil(t, r.Resolve(context.Background(), "p1", resource.Claims{}))
}

// TestResolveWithoutBlobBackendLinks covers a database-only deployment: with no
// blob backend nothing can be inlined, but the links are still useful.
func TestResolveWithoutBlobBackendLinks(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "tpl"}},
		}},
		Resources: &fakeResources{byID: map[string]*resource.Resource{
			"tpl": {ID: "tpl", Scope: resource.ScopeGlobal, MIMEType: "text/markdown", SizeBytes: 5, S3Key: "k", URI: "u"},
		}},
	})
	got := r.Resolve(context.Background(), "p1", resource.Claims{})
	require.Len(t, got, 1)
	assert.Equal(t, AvailableLinked, got[0].Availability)
}

// TestContentProducesProtocolForms is acceptance criterion 5 at the unit level:
// the served content uses the MCP embedded-resource and resource-link forms.
func TestContentProducesProtocolForms(t *testing.T) {
	items := []Resolved{
		{ResourceID: "tpl", Availability: AvailableEmbedded, URI: "u1", MIMEType: "text/markdown", Text: "# Q4"},
		{ResourceID: "logo", Availability: AvailableLinked, URI: "u2", DisplayName: "Brand Logo", MIMEType: "image/png", SizeBytes: 4096},
	}
	content := Content(items)
	require.Len(t, content, 3, "framing text, then one content item per material")

	framing, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, framing.Text, "authoritative")
	assert.Contains(t, framing.Text, "fill it")

	embedded, ok := content[1].(*mcp.EmbeddedResource)
	require.True(t, ok, "inlined text must use the embedded-resource form")
	assert.Equal(t, "# Q4", embedded.Resource.Text)
	assert.Equal(t, "text/markdown", embedded.Resource.MIMEType)
	assert.Equal(t, "u1", embedded.Resource.URI)

	link, ok := content[2].(*mcp.ResourceLink)
	require.True(t, ok, "binary material must use the resource-link form")
	assert.Equal(t, "u2", link.URI)
	assert.Equal(t, "Brand Logo", link.Name)
	require.NotNil(t, link.Size)
	assert.Equal(t, int64(4096), *link.Size)
}

// TestContentSingularFraming checks the framing text reads correctly for one
// attachment; the agent reads this sentence, so a stray plural is a real defect.
func TestContentSingularFraming(t *testing.T) {
	content := Content([]Resolved{{Availability: AvailableEmbedded, URI: "u"}})
	framing, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, framing.Text, "The following attached material accompanies")
}

// TestContentNotesWithheldWithoutNaming proves the withheld note tells the
// agent its materials are incomplete without becoming a metadata side channel.
func TestContentNotesWithheldWithoutNaming(t *testing.T) {
	content := Content([]Resolved{
		{ResourceID: "secret-report-template", Availability: UnavailableForbidden},
		{ResourceID: "deleted-thing", Availability: UnavailableMissing},
	})
	require.Len(t, content, 1, "nothing was delivered, so only the note is emitted")
	note, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, note.Text, "2 attached materials were not delivered")
	assert.Contains(t, note.Text, "1 you are not permitted to read")
	assert.Contains(t, note.Text, "1 no longer exists")
	assert.Contains(t, note.Text, "incomplete")
	assert.NotContains(t, note.Text, "secret-report-template")
	assert.NotContains(t, note.Text, "deleted-thing")
}

// TestContentUnreadableBlobStillLinks proves a blob failure keeps the material
// in the delivered set as a link rather than demoting it to a withheld note.
func TestContentUnreadableBlobStillLinks(t *testing.T) {
	content := Content([]Resolved{{Availability: UnavailableUnreadable, URI: "u", DisplayName: "T"}})
	require.Len(t, content, 2)
	_, ok := content[1].(*mcp.ResourceLink)
	assert.True(t, ok)
}

func TestContentEmpty(t *testing.T) {
	assert.Nil(t, Content(nil))
}

// TestSummaryCarriesProvenanceAndWithholds proves the use-response provenance
// lists what arrived and what did not, and that a forbidden entry carries only
// its reason.
func TestSummaryCarriesProvenanceAndWithholds(t *testing.T) {
	got := Summary([]Resolved{
		{ResourceID: "tpl", Availability: AvailableEmbedded, URI: "u1", DisplayName: "Q4 Template", MIMEType: "text/markdown", SizeBytes: 21, Text: "# Q4"},
		{ResourceID: "logo", Availability: AvailableLinked, URI: "u2", DisplayName: "Brand Logo", MIMEType: "image/png", SizeBytes: 4096},
		{ResourceID: "rubric", Availability: UnavailableForbidden},
	})
	require.Len(t, got, 3)

	assert.Equal(t, "embedded", got[0]["availability"])
	assert.Equal(t, "# Q4", got[0]["content"], "an embedded attachment carries its contents in the summary")
	assert.Equal(t, "Q4 Template", got[0]["display_name"])

	assert.Equal(t, "linked", got[1]["availability"])
	assert.NotContains(t, got[1], "content", "a link has no contents to report")
	assert.Equal(t, "u2", got[1]["uri"])

	assert.Equal(t, "unavailable", got[2]["availability"])
	for _, leaky := range []string{"resource_id", "uri", "display_name", "description", "mime_type", "size_bytes", "content"} {
		assert.NotContains(t, got[2], leaky,
			"an attachment the caller may not read must disclose nothing, not even its id")
	}
}

func TestSummaryEmpty(t *testing.T) {
	assert.Nil(t, Summary(nil))
}

// TestScopesProjectsResourceVisibility proves the promotion gate reads each
// attachment's own scope, and that a deleted resource is skipped rather than
// blocking the author on a link the UI already flags as broken.
func TestScopesProjectsResourceVisibility(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "rubric"},
		{PromptID: "p1", ResourceID: "gone"},
	})
	got, err := r.Scopes(context.Background(), "p1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "persona", got[0].Scope)
	assert.Equal(t, []string{"analyst"}, got[0].ScopeIDs)
	assert.Equal(t, "Analyst Rubric", got[0].DisplayName)
}

// TestScopesFailsClosedOnStoreError proves a lookup failure surfaces rather
// than silently reporting "no attachments", which would let a promotion through
// unchecked. A deleted resource is the opposite case and must NOT block: see
// TestScopesProjectsResourceVisibility.
func TestScopesFailsClosedOnStoreError(t *testing.T) {
	r := New(Deps{
		Attachments: &fakeAttachments{byPrompt: map[string][]prompt.Attachment{"p1": {{ResourceID: "x"}}}},
		Resources:   &fakeResources{getErr: errors.New("db down")},
	})
	_, err := r.Scopes(context.Background(), "p1")
	require.Error(t, err)

	require.Error(t, r.CheckPromotion(context.Background(), "p1", prompt.ScopeGlobal, nil))
}

// TestCheckPromotionBlocksAndAllows is acceptance criterion 2 at the resolver
// level: a persona-scoped attachment blocks promotion to global and names
// itself, while the same prompt promotes cleanly to its own persona.
func TestCheckPromotionBlocksAndAllows(t *testing.T) {
	r := testResolver(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "rubric"}})

	err := r.CheckPromotion(context.Background(), "p1", prompt.ScopeGlobal, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, prompt.ErrAttachmentScope)
	assert.True(t, strings.Contains(err.Error(), "Analyst Rubric"),
		"the message must name the resource the author has to fix, got %q", err)

	assert.NoError(t, r.CheckPromotion(context.Background(), "p1", prompt.ScopePersona, []string{"analyst"}))
}

// TestCheckPromotionOnNilResolver covers a deployment without managed
// resources: no resolver, no gate, prompts promote as they always did.
func TestCheckPromotionOnNilResolver(t *testing.T) {
	var r *Resolver
	assert.NoError(t, r.CheckPromotion(context.Background(), "p1", prompt.ScopeGlobal, nil))
}

// TestScopeOfFallsBackToFilename proves a resource saved without a display name
// still identifies itself in a rejection message.
func TestScopeOfFallsBackToFilename(t *testing.T) {
	got := ScopeOf(&resource.Resource{ID: "r1", Filename: "runbook.md", Scope: resource.ScopeUser, ScopeID: "s1"})
	assert.Equal(t, "runbook.md", got.DisplayName)
	assert.Equal(t, "user", got.Scope)
}
