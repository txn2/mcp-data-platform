package knowledge

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

const citedScriptRef = "mcp:script:6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// A knowledge page may cite a managed script (#1855): an explicit
// page.references entry naming an existing script is attached as a promoted
// reference, and a script named in the body is attached as an inline one.
func TestPromoteToPage_CitesAScript(t *testing.T) {
	const bodyScript = "mcp:script:0b7e2f0c-1111-4a4a-8b8b-000000000000"
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(pw)

	input := applyKnowledgeInput{
		Action: actionApply, Sink: sinkKnowledgePage, InsightIDs: []string{"i1"},
		Page: &pagePromotionInput{
			Slug: "orders-sync", Title: "Orders sync",
			Body:       "The orders table is kept in sync by [the backfill](" + bodyScript + "), daily at 05:00.",
			References: []string{citedScriptRef},
		},
	}
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, input)
	require.NoError(t, err)
	require.False(t, res.IsError, "citing an existing script is accepted: %s", resultMessage(t, res))
	page := pw.pages["orders-sync"]
	require.NotNil(t, page)

	sources := map[string]string{}
	for _, r := range pw.refs[page.ID] {
		sources[r.URN()] = r.Source
	}
	assert.Equal(t, knowledgepage.RefSourcePromoted, sources[citedScriptRef], "the explicit script citation lands")
	assert.Equal(t, knowledgepage.RefSourceInline, sources[bodyScript], "a script named in the body lands")
}

// A citation of a script that does not exist is refused before anything is
// written, the same as a missing asset or prompt.
func TestPromoteToPage_RefusesAMissingScript(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	pw.validateErr = fmt.Errorf("reference %q: %w", "script:6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55", knowledgepage.ErrRefTargetNotFound)
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(pw)

	input := applyKnowledgeInput{
		Action: actionApply, Sink: sinkKnowledgePage, InsightIDs: []string{"i1"},
		Page: &pagePromotionInput{
			Slug: "orders-sync", Title: "Orders sync", Body: "x",
			References: []string{citedScriptRef},
		},
	}
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, input)
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "does not exist")
	assert.Nil(t, pw.pages["orders-sync"], "no page is written for a missing script")
}

// A script id that is not a UUID is refused as a malformed reference.
func TestPromoteToPage_RefusesAMalformedScriptID(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(pw)

	input := applyKnowledgeInput{
		Action: actionApply, Sink: sinkKnowledgePage, InsightIDs: []string{"i1"},
		Page: &pagePromotionInput{
			Slug: "orders-sync", Title: "Orders sync", Body: "x",
			References: []string{"mcp:script:orders-sync"},
		},
	}
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, input)
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "script reference id must be a uuid")
	assert.Nil(t, pw.pages["orders-sync"])
}
