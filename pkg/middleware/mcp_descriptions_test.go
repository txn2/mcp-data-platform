package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPDescriptionOverrideMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		overrides     map[string]string
		tools         []*mcp.Tool
		nextResult    mcp.Result
		expectReplace bool
		wantDesc      string
	}{
		{
			name:       "non-tools/list passes through",
			method:     "resources/list",
			overrides:  map[string]string{"trino_query": "overridden"},
			nextResult: &mcp.ListResourcesResult{},
		},
		{
			name:      "matching tool gets description replaced",
			method:    methodToolsList,
			overrides: map[string]string{"trino_query": "overridden description"},
			nextResult: &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "trino_query", Description: "original"},
				},
			},
			expectReplace: true,
			wantDesc:      "overridden description",
		},
		{
			name:      "non-matching tool unchanged",
			method:    methodToolsList,
			overrides: map[string]string{"trino_query": "overridden"},
			nextResult: &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "datahub_search", Description: "original"},
				},
			},
			expectReplace: false,
			wantDesc:      "original",
		},
		{
			name:      "empty overrides is no-op",
			method:    methodToolsList,
			overrides: map[string]string{},
			nextResult: &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "trino_query", Description: "original"},
				},
			},
			expectReplace: false,
			wantDesc:      "original",
		},
		{
			name:       "nil result passes through",
			method:     methodToolsList,
			overrides:  map[string]string{"trino_query": "overridden"},
			nextResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := MCPDescriptionOverrideMiddleware(tt.overrides)
			handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
				return tt.nextResult, nil
			})

			result, err := handler(context.Background(), tt.method, nil)
			require.NoError(t, err)

			if tt.wantDesc != "" {
				listResult, ok := result.(*mcp.ListToolsResult)
				require.True(t, ok)
				require.Len(t, listResult.Tools, 1)
				assert.Equal(t, tt.wantDesc, listResult.Tools[0].Description)
			}
		})
	}
}

func TestMCPDescriptionOverrideMiddleware_ErrorPassthrough(t *testing.T) {
	mw := MCPDescriptionOverrideMiddleware(map[string]string{"trino_query": "overridden"})
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, assert.AnError
	})

	result, err := handler(context.Background(), methodToolsList, nil)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, result)
}

func TestMergedDescriptionOverrides(t *testing.T) {
	t.Run("empty config uses defaults", func(t *testing.T) {
		merged := MergedDescriptionOverrides(nil)
		assert.Contains(t, merged, "trino_query")
		assert.Contains(t, merged, "trino_execute")
		assert.Contains(t, merged["trino_query"], "datahub_search")
	})

	t.Run("config overrides win", func(t *testing.T) {
		merged := MergedDescriptionOverrides(map[string]string{
			"trino_query": "custom description",
		})
		assert.Equal(t, "custom description", merged["trino_query"])
		// trino_execute still has default
		assert.Contains(t, merged["trino_execute"], "datahub_search")
	})

	t.Run("config adds new overrides", func(t *testing.T) {
		merged := MergedDescriptionOverrides(map[string]string{
			"s3_list_objects": "custom s3 description",
		})
		assert.Contains(t, merged, "s3_list_objects")
		assert.Contains(t, merged, "trino_query")
	})
}

func TestMCPDescriptionOverrideMiddlewareDynamic(t *testing.T) {
	// Bug guard: the middleware must re-resolve overrides on every
	// tools/list call. Otherwise admin-API edits to a tool description
	// don't take effect until platform restart, which the UI presents
	// as a successful save.
	current := map[string]string{"trino_query": "v1"}

	mw := MCPDescriptionOverrideMiddlewareDynamic(func() map[string]string {
		return current
	})
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{
			Tools: []*mcp.Tool{{Name: "trino_query", Description: "original"}},
		}, nil
	})

	// First call sees "v1".
	result, err := handler(context.Background(), methodToolsList, nil)
	require.NoError(t, err)
	listResult, ok := result.(*mcp.ListToolsResult)
	require.True(t, ok)
	assert.Equal(t, "v1", listResult.Tools[0].Description)

	// Mutate the source map (simulates an admin-API save into live config).
	current = map[string]string{"trino_query": "v2"}

	// Second call must reflect the new value without rebuilding middleware.
	result, err = handler(context.Background(), methodToolsList, nil)
	require.NoError(t, err)
	listResult, ok = result.(*mcp.ListToolsResult)
	require.True(t, ok)
	assert.Equal(t, "v2", listResult.Tools[0].Description)
}
