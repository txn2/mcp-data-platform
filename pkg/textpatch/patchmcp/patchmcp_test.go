package patchmcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// textContent joins the text blocks of a tool result, which is what the model
// always sees.
func textContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	var sb strings.Builder
	for _, c := range result.Content {
		tc, ok := c.(*mcp.TextContent)
		require.True(t, ok, "unexpected content type %T", c)
		sb.WriteString(tc.Text)
	}
	return sb.String()
}

// errorEnvelope returns the structured error object an agent branches on.
func errorEnvelope(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	sc, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "result carries no structured content")
	payload, ok := sc["error"]
	require.True(t, ok, "result carries no error envelope")

	// The envelope is a typed struct; re-read it through the fields the
	// contract publishes rather than asserting on the concrete type.
	fields := map[string]any{}
	switch v := payload.(type) {
	case map[string]any:
		fields = v
	default:
		fields["value"] = v
	}
	return fields
}

// applyErr returns the error a refused patch produces.
func applyErr(t *testing.T, body string, edits ...textpatch.Edit) error {
	t.Helper()
	_, err := textpatch.Apply(body, edits, textpatch.Options{})
	require.Error(t, err)
	return err //nolint:wrapcheck // the patch error is the subject under test
}

func TestErrorResultCarriesCodeAndHint(t *testing.T) {
	err := applyErr(t, "a x b x\n", textpatch.Edit{Find: "x", Replace: "y"})

	result := ErrorResult(err)
	text := textContent(t, result)
	assert.Contains(t, text, textpatch.CodeAmbiguous)
	assert.Contains(t, text, "matches 2 spans")
	assert.Contains(t, text, "occurrence", "the hint tells the agent how to recover")
	assert.NotEmpty(t, errorEnvelope(t, result))
	assert.Error(t, result.GetError(), "the error stays available to audit and metrics")
}

func TestErrorResultCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		code     string
		category string
	}{
		{
			name:     "no match is a not-found",
			err:      applyErr(t, "body\n", textpatch.Edit{Find: "absent", Replace: "x"}),
			code:     textpatch.CodeNoMatch,
			category: middleware.ErrCategoryNotFound,
		},
		{
			name:     "unknown section is a not-found",
			err:      applyErr(t, "# One\n", textpatch.Edit{Op: textpatch.OpReplaceSection, Section: "Two", Text: "x"}),
			code:     textpatch.CodeSectionNotFound,
			category: middleware.ErrCategoryNotFound,
		},
		{
			name:     "stale base is caller input",
			err:      textpatch.StaleBaseError(2, 5),
			code:     textpatch.CodeStaleBase,
			category: middleware.ErrCategoryClientInput,
		},
		{
			name:     "non-text target is caller input",
			err:      textpatch.NotTextError("application/pdf"),
			code:     textpatch.CodeNotText,
			category: middleware.ErrCategoryClientInput,
		},
		{
			name:     "bad pattern is caller input",
			err:      applyErr(t, "body\n", textpatch.Edit{Pattern: "(", Replace: "x"}),
			code:     textpatch.CodeBadPattern,
			category: middleware.ErrCategoryClientInput,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Contains(t, textContent(t, ErrorResult(tc.err)), tc.code)
			assert.Equal(t, tc.category, category(tc.code))
		})
	}
}

func TestErrorResultWrapsForeignErrors(t *testing.T) {
	text := textContent(t, ErrorResult(errors.New("s3 unavailable")))
	assert.Contains(t, text, "s3 unavailable")
	assert.Contains(t, text, middleware.CodeToolError)
}

func TestErrorResultUnwrapsAWrappedPatchError(t *testing.T) {
	inner := textpatch.StaleBaseError(1, 9)
	result := ErrorResult(errors.Join(errors.New("context"), inner))
	assert.Contains(t, textContent(t, result), textpatch.CodeStaleBase)
}
