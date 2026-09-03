//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1610: mcp-datahub v1.15.1 makes the catalog report a URN it has never
// ingested (a dataset and a glossary term by DataHub's exists field, a data
// product by the absence of the properties aspect), so the platform's own
// existence heuristic, which read a record as absent when it carried nothing
// beyond what its reference supplied, could only remove records the catalog had
// confirmed. This suite proves both directions on the same surface: a reference
// that names nothing is still a clean not-found, and a record that exists and
// nobody has documented resolves.
//
// The bare fixtures are written straight to DataHub's OpenAPI with their key
// aspect and nothing else, because that is the shape the question is about: the
// entity exists, and every field a reader would learn something from is null.
// Creating one through datahub_create would carry a name and a definition and
// prove nothing.
//
// Criterion 4 (a confirmed miss is read once per cache TTL, not once per call)
// needs the semantic cache on, which the dev stack leaves off by default:
//
//	SEMANTIC_CACHE_ENABLED=true DATAHUB_ENABLED=true \
//	  DATAHUB_ENDPOINT=http://localhost:8080 make dev
//
// Wire forms: fetch declares "reference" as {"type":"string"} and search
// declares "entity_urns" and "sources" as {"type":"array","items":{"type":
// "string"}}, both under additionalProperties:false; "purpose", added by the
// purpose-schema decorator, is a string. Each parameter admits exactly one JSON
// form and every call below sends it as a literal of that form.

const issue1610Purpose = "Acceptance for #1610: the catalog settles existence, so an undocumented record resolves."

// issue1610Stamp is a per-run suffix so a dangling reference names an entity
// that has never been ingested rather than one that is merely absent today.
func issue1610Stamp() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }

// datahubEndpoint is the catalog the running platform reads, which the bare
// fixtures are written to directly.
func datahubEndpoint() string {
	if v := os.Getenv("DATAHUB_ENDPOINT"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "http://localhost:8080"
}

// ingestKeyOnly creates one entity carrying its key aspect and nothing else,
// and registers its removal. entityType is DataHub's OpenAPI path segment
// ("dataset", "glossaryterm"), aspect its key aspect's name ("datasetKey",
// "glossaryTermKey") -- the two are cased differently -- and value the single
// aspect written.
func ingestKeyOnly(t *testing.T, entityType, aspect, urn string, value map[string]any) {
	t.Helper()
	body := []any{map[string]any{"urn": urn, aspect: map[string]any{"value": value}}}
	status, payload := datahubOpenAPI(t, http.MethodPost,
		fmt.Sprintf("/openapi/v3/entity/%s?async=false", entityType), body)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("ingesting the key-only %s: status %d: %s", entityType, status, payload)
	}
	t.Cleanup(func() {
		_, _ = datahubOpenAPI(t, http.MethodDelete,
			fmt.Sprintf("/openapi/v3/entity/%s/%s", entityType, url.PathEscape(urn)), nil)
	})
}

// datahubOpenAPI issues one request to the catalog's OpenAPI and returns the
// status and body.
func datahubOpenAPI(t *testing.T, method, path string, body any) (status int, payload string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding the %s body: %v", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, datahubEndpoint()+path, reader)
	if err != nil {
		t.Fatalf("building the %s request: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("DATAHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reaching the catalog at %s: %v", datahubEndpoint(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestIssue1610_ADanglingCitationIsStillNotFound is acceptance 1, and the
// regression guard on #1605: the answer is unchanged, but the catalog is what
// produces it now rather than a rule over the record's fields.
func TestIssue1610_ADanglingCitationIsStillNotFound(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	stamp := issue1610Stamp()

	for _, tc := range []struct{ kind, ref string }{
		{"dataset", fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1610_nonexistent_%s,PROD)", stamp)},
		{"data product", "urn:li:dataProduct:qa-1610-nonexistent-" + stamp},
		{"glossary term", "urn:li:glossaryTerm:Qa1610Nonexistent" + stamp},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			out := c.call("fetch", map[string]any{"reference": tc.ref, "purpose": issue1610Purpose})
			if found, _ := out["found"].(bool); found {
				t.Fatalf("fetch of a %s that does not exist answered found:true: %v", tc.kind, out)
			}
			if doc, ok := out["document"]; ok && doc != nil {
				t.Errorf("a not-found answer still carries a document: %v", doc)
			}
		})
	}
}

// TestIssue1610_AnUndocumentedRecordResolves is acceptance 2: a glossary term
// that exists, sits at the glossary root and has no definition, no steward and
// no properties is the residual #1609 left open, and a dataset the catalog
// holds with no documentation at all is the same case on the dataset arm. Both
// carry nothing beyond what their own URN supplies, and both must resolve.
func TestIssue1610_AnUndocumentedRecordResolves(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	stamp := issue1610Stamp()

	termURN := "urn:li:glossaryTerm:Qa1610Bare" + stamp
	ingestKeyOnly(t, "glossaryterm", "glossaryTermKey", termURN, map[string]any{"name": "Qa1610Bare" + stamp})

	table := "warehouse.public.qa_1610_bare_" + stamp
	datasetURN := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,%s,PROD)", table)
	ingestKeyOnly(t, "dataset", "datasetKey", datasetURN, map[string]any{
		"platform": "urn:li:dataPlatform:trino", "name": table, "origin": "PROD",
	})

	for _, tc := range []struct{ kind, ref string }{
		{"glossary term", termURN},
		{"dataset", datasetURN},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			out := c.call("fetch", map[string]any{"reference": tc.ref, "purpose": issue1610Purpose})
			if found, _ := out["found"].(bool); !found {
				t.Fatalf("a %s the catalog holds but nobody has documented answered found:false: %v", tc.kind, out)
			}
			doc, _ := out["document"].(map[string]any)
			content, _ := doc["content"].(map[string]any)
			if content == nil {
				t.Fatalf("the resolved %s carries no content: %v", tc.kind, out)
			}
			// The fixture has to be the bare shape for this to prove anything:
			// a record carrying a description of its own would have resolved
			// under the retired rule too.
			if desc, _ := content["description"].(string); strings.TrimSpace(desc) != "" {
				t.Errorf("the %s fixture carries a description (%q), so it does not test the bare shape", tc.kind, desc)
			}
		})
	}
}

// TestIssue1610_SearchByEntityURNAgreesWithFetch is acceptance 3. The entity
// arm of search resolves through the same read, so a URN that names nothing
// must report no match while an undocumented dataset the catalog holds must be
// a hit; anything else and the two surfaces contradict each other on the same
// reference.
func TestIssue1610_SearchByEntityURNAgreesWithFetch(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	stamp := issue1610Stamp()

	table := "warehouse.public.qa_1610_search_" + stamp
	bareURN := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,%s,PROD)", table)
	ingestKeyOnly(t, "dataset", "datasetKey", bareURN, map[string]any{
		"platform": "urn:li:dataPlatform:trino", "name": table, "origin": "PROD",
	})
	missingURN := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1610_missing_%s,PROD)", stamp)

	if hits := issue1610CatalogHits(t, c, missingURN); hits != 0 {
		t.Errorf("search by a URN that names nothing reported %d catalog hit(s)", hits)
	}
	if hits := issue1610CatalogHits(t, c, bareURN); hits != 1 {
		t.Errorf("search by an undocumented dataset the catalog holds reported %d catalog hit(s), want 1", hits)
	}
}

// issue1610CatalogHits counts the catalog hits one entity-URN search returns.
func issue1610CatalogHits(t *testing.T, c *client, urn string) int {
	t.Helper()
	out := c.call("search", map[string]any{
		"entity_urns": []string{urn},
		"sources":     []string{"catalog"},
		"purpose":     issue1610Purpose,
	})
	count := 0
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		if source, _ := group["source"].(string); source != "catalog" {
			continue
		}
		hits, _ := group["hits"].([]any)
		count += len(hits)
	}
	return count
}

// TestIssue1610_AConfirmedMissIsReadOncePerTTL is acceptance 4. A miss is an
// answer, so the cache remembers it: the entity read behind a URN the catalog
// does not hold happens on the first call and not again while the entry stands.
// Before, only a hit was cached and the catalog was re-read for every call
// naming an uncataloged table, which is most tables in most deployments.
//
// The probe is search by entity_urns, which reads the same cached table context
// semantic enrichment reads, on a URN carrying this run's stamp so the entry is
// cold when the measurement starts.
func TestIssue1610_AConfirmedMissIsReadOncePerTTL(t *testing.T) {
	if os.Getenv("SEMANTIC_CACHE_ENABLED") != "true" {
		t.Skip("the semantic cache is off; rerun the stack with SEMANTIC_CACHE_ENABLED=true to execute this criterion")
	}
	c := connect(t)
	requireDataHub(t, c)

	urn := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1610_cache_%s,PROD)",
		issue1610Stamp())

	before := issue1610GetEntityReads(t)
	issue1610CatalogHits(t, c, urn)
	afterFirst := issue1610GetEntityReads(t)
	issue1610CatalogHits(t, c, urn)
	afterSecond := issue1610GetEntityReads(t)

	if afterFirst == before {
		t.Fatalf("the first lookup of a cold URN read the catalog %d times, so this measures nothing", afterFirst-before)
	}
	if afterSecond != afterFirst {
		t.Errorf("the second lookup of the same URN read the catalog %d more time(s); a confirmed miss must be remembered for the cache TTL",
			afterSecond-afterFirst)
	}
}

// issue1610GetEntityReads reads the platform's own count of catalog entity
// reads from its metrics endpoint.
func issue1610GetEntityReads(t *testing.T) int {
	t.Helper()
	endpoint := os.Getenv("OTEL_METRICS_URL")
	if endpoint == "" {
		endpoint = "http://localhost:9464/metrics"
	}
	resp, err := http.Get(endpoint) //nolint:gosec,noctx // a fixed local metrics endpoint
	if err != nil {
		t.Skipf("the platform's metrics endpoint (%s) is not reachable, so this criterion cannot be measured: %v", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", endpoint, err)
	}
	total := 0
	for line := range strings.SplitSeq(string(payload), "\n") {
		if !strings.HasPrefix(line, "datahub_requests_total{") || !strings.Contains(line, `operation="get_entity"`) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parsing %q: %v", line, err)
		}
		total += int(value)
	}
	return total
}
