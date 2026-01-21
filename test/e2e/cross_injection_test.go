//go:build integration

// Package e2e provides end-to-end tests for the MCP Data Platform.
package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

// Test URNs used across tests
const (
	testOrdersURN       = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.test_orders,PROD)"
	legacyUsersURN      = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.legacy_users,PROD)"
	customerMetricsURN  = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.customer_metrics,PROD)"
	s3RawDataURN        = "urn:li:dataset:(urn:li:dataPlatform:s3,test-data-lake/raw,PROD)"
	productsTableNoMeta = "memory.e2e_test.products" // Table without DataHub metadata
)

// TestTrinoToDataHubEnrichment tests that Trino describe_table results
// include semantic context from DataHub.
func TestTrinoToDataHubEnrichment(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	if helpers.SkipIfDataHubUnavailable(cfg) {
		t.Skip("DataHub not available, skipping test")
	}

	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	platform, err := helpers.NewTestPlatform(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	defer func() {
		if cerr := platform.Close(); cerr != nil {
			t.Errorf("failed to close platform: %v", cerr)
		}
	}()

	tests := []struct {
		name           string
		tableName      string
		expectedOwners []string
		expectedTags   []string
		isDeprecated   bool
		hasEnrichment  bool
	}{
		{
			name:      "table_with_full_metadata",
			tableName: "memory.e2e_test.test_orders",
			// Owners are stored as URNs in DataHub
			expectedOwners: []string{"urn:li:corpuser:alice", "urn:li:corpuser:bob"},
			// Tags are returned as names by the adapter, not URNs
			expectedTags:  []string{"e2e-test", "ecommerce"},
			isDeprecated:  false,
			hasEnrichment: true,
		},
		{
			name:          "deprecated_table",
			tableName:     "memory.e2e_test.legacy_users",
			isDeprecated:  true,
			hasEnrichment: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callTrinoDescribeTable(t, ctx, platform, tc.tableName)

			if !tc.hasEnrichment {
				helpers.AssertNoEnrichment(t, result)
				return
			}

			sc := helpers.AssertHasSemanticContext(t, result)

			if tc.isDeprecated {
				helpers.AssertIsDeprecated(t, sc)
			}

			for _, owner := range tc.expectedOwners {
				helpers.AssertOwnerPresent(t, sc, owner)
			}

			for _, tag := range tc.expectedTags {
				helpers.AssertTagPresent(t, sc, tag)
			}
		})
	}
}

// TestDataHubToTrinoEnrichment tests that DataHub search results
// include query availability from Trino.
func TestDataHubToTrinoEnrichment(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	if helpers.SkipIfDataHubUnavailable(cfg) {
		t.Skip("DataHub not available, skipping test")
	}

	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	platform, err := helpers.NewTestPlatform(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	defer func() {
		if cerr := platform.Close(); cerr != nil {
			t.Errorf("failed to close platform: %v", cerr)
		}
	}()

	tests := []struct {
		name          string
		searchQuery   string
		expectedURN   string
		queryable     bool
		hasEnrichment bool
	}{
		{
			name:          "search_finds_queryable_table",
			searchQuery:   "test_orders",
			expectedURN:   testOrdersURN,
			queryable:     true,
			hasEnrichment: true,
		},
		{
			name:          "search_finds_another_queryable_table",
			searchQuery:   "customer_metrics",
			expectedURN:   customerMetricsURN,
			queryable:     true,
			hasEnrichment: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callDataHubSearch(t, ctx, platform, tc.searchQuery, "trino")

			if !tc.hasEnrichment {
				helpers.AssertNoEnrichment(t, result)
				return
			}

			qc := helpers.AssertHasQueryContext(t, result)

			if tc.queryable {
				helpers.AssertQueryAvailable(t, qc, tc.expectedURN)
			}
		})
	}
}

// TestS3ToDataHubEnrichment tests that S3 list_objects results
// include semantic context from DataHub.
func TestS3ToDataHubEnrichment(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	if helpers.SkipIfDataHubUnavailable(cfg) {
		t.Skip("DataHub not available, skipping test")
	}

	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	platform, err := helpers.NewTestPlatform(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	defer func() {
		if cerr := platform.Close(); cerr != nil {
			t.Errorf("failed to close platform: %v", cerr)
		}
	}()

	tests := []struct {
		name                  string
		bucket                string
		prefix                string
		expectedMatchingCount int
		hasEnrichment         bool
	}{
		{
			name:                  "bucket_with_registered_dataset",
			bucket:                "test-data-lake",
			prefix:                "raw",
			expectedMatchingCount: 1,
			hasEnrichment:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callS3ListObjects(t, ctx, platform, tc.bucket, tc.prefix)

			if !tc.hasEnrichment {
				helpers.AssertNoEnrichment(t, result)
				return
			}

			sc := helpers.AssertHasSemanticContext(t, result)
			helpers.AssertMatchingDatasetCount(t, sc, tc.expectedMatchingCount)
		})
	}
}

// TestDataHubToS3Enrichment tests that DataHub search for S3 datasets
// includes storage availability.
func TestDataHubToS3Enrichment(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	if helpers.SkipIfDataHubUnavailable(cfg) {
		t.Skip("DataHub not available, skipping test")
	}

	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	platform, err := helpers.NewTestPlatform(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	defer func() {
		if cerr := platform.Close(); cerr != nil {
			t.Errorf("failed to close platform: %v", cerr)
		}
	}()

	tests := []struct {
		name          string
		searchQuery   string
		expectedURN   string
		hasEnrichment bool
	}{
		{
			name:          "search_finds_S3_dataset",
			searchQuery:   "raw-data-lake",
			expectedURN:   s3RawDataURN,
			hasEnrichment: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callDataHubSearch(t, ctx, platform, tc.searchQuery, "s3")

			if !tc.hasEnrichment {
				helpers.AssertNoEnrichment(t, result)
				return
			}

			// Verify storage context is present (availability depends on MinIO connectivity)
			sc := helpers.AssertHasStorageContext(t, result)
			if sc == nil {
				t.Error("expected storage_context to be present")
			}
		})
	}
}

// callTrinoDescribeTable simulates a trino_describe_table tool call.
func callTrinoDescribeTable(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, tableName string) *mcp.CallToolResult {
	t.Helper()

	if tp == nil {
		t.Fatal("TestPlatform is nil")
	}

	args, _ := json.Marshal(map[string]string{
		"table": tableName,
	})

	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "trino_describe_table",
			Arguments: args,
		},
	}

	return executeWithMiddleware(t, ctx, tp, request, "trino")
}

// callDataHubSearch simulates a datahub_search tool call.
func callDataHubSearch(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, query string, datahubPlatform string) *mcp.CallToolResult {
	t.Helper()

	args, _ := json.Marshal(map[string]string{
		"query":    query,
		"platform": datahubPlatform,
	})

	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "datahub_search",
			Arguments: args,
		},
	}

	return executeWithMiddleware(t, ctx, tp, request, "datahub")
}

// callS3ListObjects simulates an s3_list_objects tool call.
func callS3ListObjects(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, bucket, prefix string) *mcp.CallToolResult {
	t.Helper()

	args, _ := json.Marshal(map[string]string{
		"bucket": bucket,
		"prefix": prefix,
	})

	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "s3_list_objects",
			Arguments: args,
		},
	}

	return executeWithMiddleware(t, ctx, tp, request, "s3")
}

// executeWithMiddleware executes a tool request through the middleware chain.
func executeWithMiddleware(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, request mcp.CallToolRequest, toolkitKind string) *mcp.CallToolResult {
	t.Helper()

	// Create platform context
	pc := middleware.NewPlatformContext("e2e-test-" + time.Now().Format("20060102150405"))
	pc.ToolName = request.Params.Name
	pc.ToolkitKind = toolkitKind
	pc.UserID = "e2e-test-user"

	ctx = middleware.WithPlatformContext(ctx, pc)

	// Create a mock handler that returns a basic result
	mockHandler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Create a mock result with URN for enrichment tests
		resultData := createMockResult(toolkitKind, request)
		resultJSON, _ := json.Marshal(resultData)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(resultJSON)},
			},
		}, nil
	}

	// Execute through middleware chain
	chain := tp.MiddlewareChain()
	if chain == nil {
		t.Fatal("middleware chain is nil - platform may not be initialized correctly")
	}
	wrappedHandler := chain.Wrap(mockHandler)

	result, err := wrappedHandler(ctx, request)
	if err != nil {
		t.Fatalf("middleware execution failed: %v", err)
	}

	return result
}

// createMockResult creates appropriate mock result data for each toolkit.
func createMockResult(toolkitKind string, request mcp.CallToolRequest) map[string]any {
	var args map[string]string
	_ = json.Unmarshal(request.Params.Arguments, &args)

	switch toolkitKind {
	case "trino":
		return map[string]any{
			"table":   args["table"],
			"columns": []map[string]string{{"name": "id", "type": "bigint"}},
		}
	case "datahub":
		platform := args["platform"]
		urn := buildDataHubURN(args["query"], platform)
		return map[string]any{
			"results": []map[string]any{
				{
					"urn":      urn,
					"name":     args["query"],
					"platform": platform,
				},
			},
		}
	case "s3":
		return map[string]any{
			"bucket": args["bucket"],
			"prefix": args["prefix"],
			"objects": []map[string]any{
				{"key": args["prefix"] + "/sample.parquet", "size": 1024},
			},
		}
	default:
		return map[string]any{}
	}
}

// buildDataHubURN constructs a URN based on the search query and platform.
func buildDataHubURN(query string, platform string) string {
	switch platform {
	case "trino":
		switch query {
		case "test_orders":
			return testOrdersURN
		case "customer_metrics":
			return customerMetricsURN
		case "legacy_users":
			return legacyUsersURN
		default:
			return "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test." + query + ",PROD)"
		}
	case "s3":
		return s3RawDataURN
	default:
		return "urn:li:dataset:(urn:li:dataPlatform:" + platform + "," + query + ",PROD)"
	}
}
