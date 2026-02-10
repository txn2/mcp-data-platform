//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

// nanos returns a unique suffix based on UnixNano for test isolation.
func nanos() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// testCtx returns a context with a 30-second timeout and cleanup.
func testCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// testWriter creates a DataHubClientWriter from E2E config and a raw client
// for verification reads. Skips the test if DataHub is not available.
func testWriter(t *testing.T) (*knowledge.DataHubClientWriter, *dhclient.Client) {
	t.Helper()

	cfg := helpers.DefaultE2EConfig()
	if !cfg.IsDataHubAvailable() {
		t.Skip("E2E_DATAHUB_URL not configured; skipping DataHub writer integration test")
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = cfg.DataHubURL
	clientCfg.Token = cfg.DataHubToken

	c, err := dhclient.New(clientCfg)
	if err != nil {
		t.Fatalf("creating datahub client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	writer := knowledge.NewDataHubClientWriter(c)

	return writer, c
}

// testEntityURN returns TEST_ENTITY_URN or discovers a dataset via Search.
func testEntityURN(t *testing.T, c *dhclient.Client) string {
	t.Helper()

	if urn := os.Getenv("TEST_ENTITY_URN"); urn != "" {
		return urn
	}

	ctx := testCtx(t)

	result, err := c.Search(ctx, "*", dhclient.WithEntityType("DATASET"), dhclient.WithLimit(1))
	if err != nil {
		t.Fatalf("Search for test entity: %v", err)
	}
	if len(result.Entities) == 0 {
		t.Fatal("no datasets found; set TEST_ENTITY_URN explicitly")
	}

	urn := result.Entities[0].URN
	t.Logf("discovered test entity: %s", urn)

	return urn
}

// TestDataHubWriterUpdateDescription tests that UpdateDescription writes to
// a real DataHub instance and GetCurrentMetadata reads it back correctly.
func TestDataHubWriterUpdateDescription(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	// Read original metadata so we can restore it
	original, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata (original): %v", err)
	}

	uniqueDesc := fmt.Sprintf("E2E writer test description %s", nanos())

	// Register cleanup to restore original description
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()

		if restoreErr := writer.UpdateDescription(cleanCtx, urn, original.Description); restoreErr != nil {
			t.Logf("cleanup: failed to restore description: %v", restoreErr)
		}
	})

	// Write via writer
	if err := writer.UpdateDescription(ctx, urn, uniqueDesc); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}

	// Verify via GetCurrentMetadata (proves the full round-trip through the writer)
	meta, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata (verify): %v", err)
	}
	if meta.Description != uniqueDesc {
		t.Errorf("description mismatch: got %q, want %q", meta.Description, uniqueDesc)
	}
}

// TestDataHubWriterAddTag tests that AddTag writes a tag to a real DataHub entity
// and GetCurrentMetadata reads it back.
func TestDataHubWriterAddTag(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	tagURN := fmt.Sprintf("urn:li:tag:e2e_writer_%s", nanos())

	// Register cleanup to remove the tag
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()

		if err := writer.RemoveTag(cleanCtx, urn, tagURN); err != nil {
			t.Logf("cleanup: failed to remove tag: %v", err)
		}
	})

	// Add tag
	if err := writer.AddTag(ctx, urn, tagURN); err != nil {
		t.Fatalf("AddTag: %v", err)
	}

	// Verify via GetCurrentMetadata
	meta, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata: %v", err)
	}
	if !containsString(meta.Tags, tagURN) {
		t.Errorf("tag %s not found in metadata tags %v", tagURN, meta.Tags)
	}
}

// TestDataHubWriterRemoveTag tests that RemoveTag removes a tag from a real
// DataHub entity.
func TestDataHubWriterRemoveTag(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	tagURN := fmt.Sprintf("urn:li:tag:e2e_writer_%s", nanos())

	// Setup: add tag first
	if err := writer.AddTag(ctx, urn, tagURN); err != nil {
		t.Fatalf("setup AddTag: %v", err)
	}

	// Cleanup (RemoveTag is idempotent)
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()

		if err := writer.RemoveTag(cleanCtx, urn, tagURN); err != nil {
			t.Logf("cleanup: failed to remove tag: %v", err)
		}
	})

	// Remove tag
	if err := writer.RemoveTag(ctx, urn, tagURN); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}

	// Verify removed
	meta, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata: %v", err)
	}
	if containsString(meta.Tags, tagURN) {
		t.Errorf("tag %s still present after RemoveTag", tagURN)
	}
}

// TestDataHubWriterAddGlossaryTerm tests that AddGlossaryTerm writes to a real
// DataHub instance and is visible via GetCurrentMetadata.
func TestDataHubWriterAddGlossaryTerm(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	termURN := fmt.Sprintf("urn:li:glossaryTerm:e2e_writer_%s", nanos())

	// Cleanup: remove term (RemoveGlossaryTerm is on the underlying client)
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()

		if err := c.RemoveGlossaryTerm(cleanCtx, urn, termURN); err != nil {
			t.Logf("cleanup: failed to remove glossary term: %v", err)
		}
	})

	// Add glossary term
	if err := writer.AddGlossaryTerm(ctx, urn, termURN); err != nil {
		t.Fatalf("AddGlossaryTerm: %v", err)
	}

	// Verify via GetCurrentMetadata
	meta, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata: %v", err)
	}
	if !containsString(meta.GlossaryTerms, termURN) {
		t.Errorf("term %s not found in metadata glossary_terms %v", termURN, meta.GlossaryTerms)
	}
}

// TestDataHubWriterAddDocumentationLink tests that AddDocumentationLink writes
// a link to a real DataHub entity. Verification reads the institutionalMemory
// aspect directly via the DataHub REST API since the types.Entity struct does
// not expose links.
func TestDataHubWriterAddDocumentationLink(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	cfg := helpers.DefaultE2EConfig()
	linkURL := fmt.Sprintf("https://e2e-writer-%s.example.com", nanos())
	linkDesc := "E2E writer integration test link"

	// Cleanup: remove link via underlying client
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()

		if err := c.RemoveLink(cleanCtx, urn, linkURL); err != nil {
			t.Logf("cleanup: failed to remove link: %v", err)
		}
	})

	// Add link via writer
	if err := writer.AddDocumentationLink(ctx, urn, linkURL, linkDesc); err != nil {
		t.Fatalf("AddDocumentationLink: %v", err)
	}

	// Verify by reading the institutionalMemory aspect via REST API directly.
	// The types.Entity struct doesn't include links, and readInstitutionalMemory
	// is unexported in the client package. This calls the REST endpoint directly.
	found := readAndFindLink(t, cfg.DataHubURL, cfg.DataHubToken, urn, linkURL)
	if !found {
		t.Errorf("link %s not found in institutionalMemory aspect after AddDocumentationLink", linkURL)
	}
}

// readAndFindLink reads the institutionalMemory aspect from the DataHub REST API
// and checks whether a link with the given URL exists.
func readAndFindLink(t *testing.T, datahubURL, token, urn, linkURL string) bool {
	t.Helper()

	apiURL := fmt.Sprintf("%s/aspects/%s?aspect=institutionalMemory&version=0",
		datahubURL, url.PathEscape(urn))

	req, err := http.NewRequestWithContext(testCtx(t), http.MethodGet, apiURL, nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reading institutionalMemory aspect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// No institutionalMemory aspect yet — link not found
		return false
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d reading institutionalMemory: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	// The REST response wraps the aspect value
	var wrapper struct {
		Aspect struct {
			Value json.RawMessage `json:"value"`
		} `json:"aspect"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		t.Fatalf("parsing REST response: %v", err)
	}

	var memory struct {
		Elements []struct {
			URL string `json:"url"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(wrapper.Aspect.Value, &memory); err != nil {
		t.Fatalf("parsing institutionalMemory value: %v", err)
	}

	for _, e := range memory.Elements {
		if e.URL == linkURL {
			return true
		}
	}

	return false
}

// TestDataHubWriterGetCurrentMetadata tests that GetCurrentMetadata correctly
// maps entity fields to EntityMetadata, including tags, glossary terms, and owners.
func TestDataHubWriterGetCurrentMetadata(t *testing.T) {
	writer, c := testWriter(t)
	urn := testEntityURN(t, c)
	ctx := testCtx(t)

	// Read metadata via writer
	meta, err := writer.GetCurrentMetadata(ctx, urn)
	if err != nil {
		t.Fatalf("GetCurrentMetadata: %v", err)
	}

	// Basic sanity: slices should be non-nil (the writer always initializes them)
	if meta.Tags == nil {
		t.Error("Tags should be non-nil (empty slice, not nil)")
	}
	if meta.GlossaryTerms == nil {
		t.Error("GlossaryTerms should be non-nil (empty slice, not nil)")
	}
	if meta.Owners == nil {
		t.Error("Owners should be non-nil (empty slice, not nil)")
	}

	// Cross-verify with the underlying client's GetEntity
	entity, err := c.GetEntity(ctx, urn)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	// Description should match
	if meta.Description != entity.Description {
		t.Errorf("Description mismatch: writer=%q, client=%q", meta.Description, entity.Description)
	}

	// Tag count should match
	if len(meta.Tags) != len(entity.Tags) {
		t.Errorf("Tags count mismatch: writer=%d, client=%d", len(meta.Tags), len(entity.Tags))
	}

	// GlossaryTerms count should match
	if len(meta.GlossaryTerms) != len(entity.GlossaryTerms) {
		t.Errorf("GlossaryTerms count mismatch: writer=%d, client=%d",
			len(meta.GlossaryTerms), len(entity.GlossaryTerms))
	}

	// Owners count should match
	if len(meta.Owners) != len(entity.Owners) {
		t.Errorf("Owners count mismatch: writer=%d, client=%d",
			len(meta.Owners), len(entity.Owners))
	}
}

// containsString checks if a string slice contains a value.
func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}

	return false
}
