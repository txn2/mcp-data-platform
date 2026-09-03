package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/agentinstructions"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// fakeInstructionsStore is an in-memory InstructionsStore for sink tests: it
// holds one document, records every write, and can fail either operation.
type fakeInstructionsStore struct {
	text    string
	writes  []string
	authors []string
	getErr  error
	setErr  error
}

func (f *fakeInstructionsStore) AgentInstructions(context.Context) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.text, nil
}

func (f *fakeInstructionsStore) SetAgentInstructions(_ context.Context, value, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.text = value
	f.writes = append(f.writes, value)
	f.authors = append(f.authors, author)
	return nil
}

var _ InstructionsStore = (*fakeInstructionsStore)(nil)

// testInventory is the tool inventory a sink test measures a rule's tool
// references against: an api_ family that exists, so a retired api_ name in a
// promoted rule is recognizable as stale.
func testInventory() []string {
	return []string{"platform_info", "search", "fetch", "api_discover", "api_invoke_endpoint", "trino_query"}
}

// newInstructionsToolkit assembles a toolkit with the agent_instructions sink
// wired, returning the toolkit and the store behind it.
func newInstructionsToolkit(t *testing.T, store InsightStore, cs ChangesetStore, current string) (*Toolkit, *fakeInstructionsStore) {
	t.Helper()
	tk := newApplyToolkit(t, store, cs, &spyWriter{})
	ins := &fakeInstructionsStore{text: current}
	tk.SetInstructionsSink(ins, testInventory)
	return tk, ins
}

// applyInstructionsInput is an apply call on the agent_instructions sink.
func applyInstructionsInput(section, body string, insightIDs []string) applyKnowledgeInput {
	return applyKnowledgeInput{
		Action:       actionApply,
		Sink:         sinkAgentInstructions,
		InsightIDs:   insightIDs,
		Instructions: &instructionsPromotionInput{Section: section, Body: body},
	}
}

// ruleInsightID is the source capture a sink test promotes from.
const ruleInsightID = "i1"

// ruleInsight is a store holding one operational_rule capture, the class this
// sink promotes.
func ruleInsight() *fullSpyStore {
	return &fullSpyStore{Insights: []Insight{{ID: ruleInsightID, SinkClass: memory.SinkOperationalRule}}}
}

// --- Criterion 1: the rule lands in the customized layer ---

func TestPromoteToInstructions_WritesTheRuleAsItsOwnSection(t *testing.T) {
	cs := &spyChangesetStore{}
	store := ruleInsight()
	tk, ins := newInstructionsToolkit(t, store, cs, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("OpenSearch aggregations", "Aggregations go through raw_query.", []string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))

	assert.Equal(t, "## OpenSearch aggregations\n\nAggregations go through raw_query.\n\n", ins.text)

	out := parseJSONResult(t, res)
	assert.Equal(t, "created", out["action"])
	assert.Equal(t, "OpenSearch aggregations", out["section"])
	assert.Equal(t, "ai:OpenSearch aggregations", out["target_urn"])
	assert.Equal(t, true, out["revertible"])
	assert.EqualValues(t, len(ins.text), out["instructions_bytes"])
	assert.EqualValues(t, agentinstructions.MaxCustomizedBytes, out["instructions_limit"])

	require.Len(t, cs.Changesets, 1)
	assert.Equal(t, "ai:OpenSearch aggregations", cs.Changesets[0].TargetURN)
	assert.Equal(t, changeUpdateInstructions, cs.Changesets[0].ChangeType)
	assert.Equal(t, "", cs.Changesets[0].PreviousValue[instructionsFieldText])
	assert.Equal(t, ins.text, cs.Changesets[0].NewValue[instructionsFieldText])

	require.Len(t, store.MarkAppliedCalls, 1, "the source insight should be marked applied")
	assert.Equal(t, "i1", store.MarkAppliedCalls[0].ID)
	assert.Equal(t, "admin@example.com", ins.authors[0])
}

// --- Criterion 2: every other section is byte-identical ---

func TestPromoteToInstructions_LeavesEverySectionUntouched(t *testing.T) {
	const before = "# Deployment notes\n\nRead this first.\n\n" +
		"## Query engines\n\nOld text.\n\n" +
		"## Naming\n\nTables are singular.\n"
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, before)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))

	assert.True(t, strings.HasPrefix(ins.text, "# Deployment notes\n\nRead this first.\n\n"),
		"the preamble changed: %q", ins.text)
	assert.True(t, strings.HasSuffix(ins.text, "## Naming\n\nTables are singular.\n"),
		"the following section changed: %q", ins.text)
	assert.Contains(t, ins.text, "## Query engines\n\nTrino holds the warehouse.\n")
	assert.NotContains(t, ins.text, "Old text.")
}

// --- Criterion 3: a second promotion of the same rule consolidates ---

func TestPromoteToInstructions_SecondPromotionRewritesItsOwnSection(t *testing.T) {
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, "")

	var last *mcp.CallToolResult
	for _, body := range []string{"First wording.", "Corrected wording."} {
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
			applyInstructionsInput("Query engines", body, nil))
		require.NoError(t, err)
		require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
		last = res
	}

	assert.Equal(t, 1, strings.Count(ins.text, "## Query engines"),
		"the section was appended instead of rewritten: %q", ins.text)
	assert.Contains(t, ins.text, "Corrected wording.")
	assert.NotContains(t, ins.text, "First wording.")

	require.Len(t, cs.Changesets, 2)
	assert.Equal(t, "updated", parseJSONResult(t, last)["action"],
		"the second promotion reports an update, not another create")
	assert.Equal(t, "## Query engines\n\nFirst wording.\n\n",
		cs.Changesets[1].PreviousValue[instructionsFieldText],
		"the before-image must hold the text the second promotion replaced")
}

// --- Criterion 5: a rule naming an unregistered tool is refused ---

func TestPromoteToInstructions_RefusesAnUnregisteredToolName(t *testing.T) {
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("API discovery",
			"Enumerate the operations with api_list_endpoints before invoking one.", nil))
	require.NoError(t, err)
	require.True(t, res.IsError, "a rule naming a retired tool must be refused")
	assert.Contains(t, resultMessage(t, res), "api_list_endpoints")
	assert.Empty(t, ins.writes, "nothing may be written when the promotion is refused")
}

// A registered name in the same family must pass, or the guard would refuse
// every rule that names a tool at all.
func TestPromoteToInstructions_AcceptsARegisteredToolName(t *testing.T) {
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("API discovery", "Enumerate operations with api_discover.", nil))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	require.Len(t, ins.writes, 1)
}

// With no inventory to measure against, the guard must not refuse every
// snake_case token it sees.
func TestPromoteToInstructions_ToolGuardIsInactiveWithoutAnInventory(t *testing.T) {
	tk, _ := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	tk.SetInstructionsSink(&fakeInstructionsStore{}, nil)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("API discovery", "Use api_list_endpoints.", nil))
	require.NoError(t, err)
	assert.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
}

// --- Criterion 7: the byte cap ---

func TestPromoteToInstructions_RefusesAPromotionPastTheCap(t *testing.T) {
	current := strings.Repeat("x", agentinstructions.MaxCustomizedBytes-10)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, current)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.True(t, res.IsError, "a promotion past the cap must be refused")
	msg := resultMessage(t, res)
	assert.Contains(t, msg, "over the")
	assert.Contains(t, msg, "knowledge page")
	assert.Empty(t, ins.writes, "the refused promotion must not be stored")
}

// Below the cap but above the advisory, the write succeeds and the response
// says the layer is getting long.
func TestPromoteToInstructions_AdvisesWhenTheLayerIsLong(t *testing.T) {
	current := "## Existing\n\n" + strings.Repeat("x", agentinstructions.AdviseCustomizedBytes) + "\n"
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, current)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	require.Len(t, ins.writes, 1, "the write succeeds despite the advisory")

	out := parseJSONResult(t, res)
	notice, ok := out["size_notice"].(string)
	require.True(t, ok, "expected a size_notice on an over-advisory layer")
	assert.Contains(t, notice, "knowledge page")
	assert.Contains(t, out["message"], notice, "the message must carry the advisory too")
}

// --- The sink refuses when the deployment cannot hold a write ---

func TestPromoteToInstructions_RefusesWhenNotConfigured(t *testing.T) {
	tk := newApplyToolkit(t, ruleInsight(), &spyChangesetStore{}, &spyWriter{})

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	msg := resultMessage(t, res)
	assert.Contains(t, msg, "not configured")
	assert.Contains(t, msg, "sink=knowledge_page", "the refusal must name the alternative")
}

// A failed read must refuse rather than write the fallback text plus a section
// over whatever is actually stored.
func TestPromoteToInstructions_RefusesWhenTheCurrentTextCannotBeRead(t *testing.T) {
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	ins.getErr = errors.New("db down")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "db down")
	assert.Empty(t, ins.writes)
}

func TestPromoteToInstructions_ConfirmationGate(t *testing.T) {
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, "")
	tk.requireConfirmation = true

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := parseJSONResult(t, res)
	assert.Equal(t, true, out["confirmation_required"])
	assert.Empty(t, ins.writes, "nothing is written until the promotion is confirmed")

	in := applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil)
	in.Confirm = true
	res2, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	require.False(t, res2.IsError, "unexpected error result: %s", resultMessage(t, res2))
	require.Len(t, ins.writes, 1)
}

// --- Criterion 8/9: a long rule becomes a page with an index entry ---

func TestPromoteToInstructions_DivertsALongRuleToAPage(t *testing.T) {
	body := "Aggregations go through raw_query. " + strings.Repeat("Detail. ", 400)
	require.Greater(t, len(body), maxInlineRuleBytes, "the fixture must exceed the inline limit")

	cs := &spyChangesetStore{}
	store := ruleInsight()
	tk, ins := newInstructionsToolkit(t, store, cs, "")
	pw := newFakePageWriter()
	tk.SetPageWriter(pw)

	in := applyInstructionsInput("OpenSearch aggregations", body, []string{"i1"})
	in.Instructions.Summary = "how an aggregation is run against OpenSearch"
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))

	// The page carries the body.
	page := pw.pages["opensearch-aggregations"]
	require.NotNil(t, page, "the diverted body should land on a page keyed by the section slug")
	assert.Equal(t, body, page.Body)
	assert.Equal(t, "OpenSearch aggregations", page.Title)

	// The instructions carry one index entry, not the body.
	assert.Equal(t,
		"## OpenSearch aggregations\n\n- `mcp:knowledge_page:opensearch-aggregations` -- "+
			"how an aggregation is run against OpenSearch.\n\n", ins.text)
	assert.NotContains(t, ins.text, "Detail. Detail.")

	out := parseJSONResult(t, res)
	assert.Equal(t, "opensearch-aggregations", out["slug"])
	assert.Contains(t, out["message"], "knowledge page")

	// One changeset covers both halves.
	require.Len(t, cs.Changesets, 1)
	assert.Equal(t, changeIndexInstructions, cs.Changesets[0].ChangeType)
	assert.Equal(t, changeCreatePage, cs.Changesets[0].NewValue[instructionsFieldPageOp])
	assert.Equal(t, "opensearch-aggregations", cs.Changesets[0].NewValue[instructionsFieldSlug])
	assert.Equal(t, body, cs.Changesets[0].NewValue[pageFieldBody])
}

// With no page sink, a rule too long to stay inline is refused with both facts
// stated: it is over the inline limit, and there is no page to move it to.
func TestPromoteToInstructions_RefusesALongRuleWithNoPageSink(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	msg := resultMessage(t, res)
	assert.Contains(t, msg, "knowledge page")
	assert.Contains(t, msg, "not configured")
	assert.Empty(t, ins.writes)
}

// The size check runs before the page write, so an over-cap diverted promotion
// leaves no page behind.
func TestPromoteToInstructions_DivertRefusedPastTheCapWritesNoPage(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	current := strings.Repeat("x", agentinstructions.MaxCustomizedBytes-20)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, current)
	pw := newFakePageWriter()
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Empty(t, pw.inserted, "no page may be written when the index entry cannot fit")
	assert.Empty(t, ins.writes)
}

// --- Criterion 4: list_changesets and rollback ---

func TestInstructionsChangesetIsReportedRevertible(t *testing.T) {
	revertible, blocking := changesetRevertibility(InstructionsTargetURN("Query engines"), nil)
	assert.True(t, revertible, "an instructions promotion records the whole prior text, so it reverts")
	assert.Empty(t, blocking)
}

func TestRevertInstructionsChangeset_RestoresThePriorText(t *testing.T) {
	const before = "## Naming\n\nTables are singular.\n"
	cs := &spyChangesetStore{}
	store := ruleInsight()
	tk, ins := newInstructionsToolkit(t, store, cs, before)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", []string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	csID, _ := parseJSONResult(t, res)["changeset_id"].(string)
	require.NotEmpty(t, csID)

	rb, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionRollback, ChangesetID: csID, Confirm: true,
	})
	require.NoError(t, err)
	require.False(t, rb.IsError, "unexpected rollback error: %s", resultMessage(t, rb))

	assert.Equal(t, before, ins.text, "rollback must restore the text byte for byte")
	assert.True(t, cs.Changesets[0].RolledBack)
	assert.Equal(t, []string{"i1"}, store.ReturnToReviewIDs,
		"the source insight returns to the review queue")
}

func TestRevertInstructionsChangeset_RefusesWhenTheLayerWasEditedSince(t *testing.T) {
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	csID, _ := parseJSONResult(t, res)["changeset_id"].(string)

	// An operator edits the instructions afterwards through the admin page.
	ins.text += "\n## Later\n\nSomething else.\n"

	rb, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionRollback, ChangesetID: csID, Confirm: true,
	})
	require.NoError(t, err)
	require.True(t, rb.IsError, "a rollback over a later edit must be refused")
	assert.Contains(t, resultMessage(t, rb), "edited after this changeset")
	assert.Contains(t, ins.text, "Something else.", "the later edit must survive the refusal")
}

func TestRevertInstructionsChangeset_RevertsTheDivertedPage(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, "")
	pw := newFakePageWriter()
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	csID, _ := parseJSONResult(t, res)["changeset_id"].(string)
	page := pw.pages["long-rule"]
	require.NotNil(t, page)

	rb, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionRollback, ChangesetID: csID, Confirm: true,
	})
	require.NoError(t, err)
	require.False(t, rb.IsError, "unexpected rollback error: %s", resultMessage(t, rb))

	assert.Equal(t, "", ins.text, "the index entry must be gone")
	assert.Equal(t, []string{page.ID}, pw.deleted, "the page the entry pointed at must be deleted")
}

func TestRevertInstructionsChangeset_RefusesWhenNotConfigured(t *testing.T) {
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Query engines"),
		ChangeType: changeUpdateInstructions,
		NewValue:   map[string]any{instructionsFieldText: "x"},
	}
	_, err := RevertChangeset(context.Background(), RollbackDeps{Changesets: &spyChangesetStore{}}, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// A diverted promotion whose page is gone from the store cannot be reverted
// half-way: the instructions must be left as they are.
func TestRevertInstructionsChangeset_RefusesWhenThePageIsGone(t *testing.T) {
	ins := &fakeInstructionsStore{text: "## Long rule\n\n- ref\n\n"}
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Long rule"),
		ChangeType:    changeIndexInstructions,
		PreviousValue: map[string]any{instructionsFieldText: ""},
		NewValue: map[string]any{
			instructionsFieldText:   ins.text,
			instructionsFieldSlug:   "long-rule",
			instructionsFieldPageOp: changeCreatePage,
		},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Pages: newFakePageWriter(), Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer exists")
	assert.Equal(t, "## Long rule\n\n- ref\n\n", ins.text, "the instructions must be untouched")
}

// --- Validation and payload shaping ---

func TestValidateInstructionsPromotion(t *testing.T) {
	tests := []struct {
		name  string
		input instructionsPromotionInput
		want  string
	}{
		{"a section and body is valid", instructionsPromotionInput{Section: "S", Body: "B"}, ""},
		{"section is required", instructionsPromotionInput{Body: "B"}, "instructions.section is required"},
		{"body is required", instructionsPromotionInput{Section: "S"}, "instructions.body is required"},
		{
			"a section carrying a heading marker is refused",
			instructionsPromotionInput{Section: "## S", Body: "B"},
			"cannot contain a newline or a '#'",
		},
		{
			"a multi-line section is refused",
			instructionsPromotionInput{Section: "S\nT", Body: "B"},
			"cannot contain a newline or a '#'",
		},
		{
			"an over-long section is refused",
			instructionsPromotionInput{Section: strings.Repeat("s", maxInstructionsSectionLen+1), Body: "B"},
			"exceeds 120 characters",
		},
		{
			"an over-long body is refused",
			instructionsPromotionInput{Section: "S", Body: strings.Repeat("b", maxPageBodyLen+1)},
			"instructions.body exceeds",
		},
		{
			"an over-long summary is refused under its own name",
			instructionsPromotionInput{Section: "S", Body: "B", Summary: strings.Repeat("s", maxPageSummaryLen+1)},
			"instructions.summary exceeds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateInstructionsPromotion(tt.input)
			if tt.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tt.want)
		})
	}
}

func TestPromoteToInstructions_RequiresTheInstructionsObject(t *testing.T) {
	tk, _ := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionApply, Sink: sinkAgentInstructions,
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "instructions object")
}

func TestHandleApply_UnknownSinkNamesEveryValidSink(t *testing.T) {
	tk, _ := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionApply, Sink: "nowhere",
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	msg := resultMessage(t, res)
	for _, sink := range []string{sinkDataHub, sinkKnowledgePage, sinkAgentInstructions} {
		assert.Contains(t, msg, sink)
	}
}

func TestUpsertInstructionSection(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		section     string
		body        string
		want        string
		wantCreated bool
		wantErr     string
	}{
		{
			name: "an empty document becomes the section alone", doc: "", section: "S", body: "B",
			want: "## S\n\nB\n\n", wantCreated: true,
		},
		{
			name: "a whitespace-only document is treated as empty", doc: "  \n\n", section: "S", body: "B",
			want: "## S\n\nB\n\n", wantCreated: true,
		},
		{
			name: "a new section is appended after the existing text",
			doc:  "## A\n\nalpha\n", section: "S", body: "B",
			want: "## A\n\nalpha\n\n## S\n\nB\n\n", wantCreated: true,
		},
		{
			name: "an existing section is rewritten in place",
			doc:  "## A\n\nalpha\n\n## S\n\nold\n\n## Z\n\nzeta\n", section: "S", body: "new",
			want: "## A\n\nalpha\n\n## S\n\nnew\n\n## Z\n\nzeta\n", wantCreated: false,
		},
		{
			// The heading is reused as the document wrote it: a promotion onto a
			// section an operator wrote at another level must not change its level.
			name: "an existing heading keeps its own level",
			doc:  "# S\n\nold\n", section: "S", body: "new",
			want: "# S\n\nnew\n\n", wantCreated: false,
		},
		{
			name:    "a section owning nested headings is refused, not flattened",
			doc:     "# Deployment notes\n\nintro\n\n## Engines\n\ntrino\n\n## Naming\n\nsingular\n",
			section: "Deployment notes", body: "new",
			wantErr: "owns 2 nested heading(s)",
		},
		{
			name: "an ambiguous section name is an error, not a guess",
			doc:  "## S\n\none\n\n## S\n\ntwo\n", section: "S", body: "new",
			wantErr: "resolving agent-instruction section",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, created, err := upsertInstructionSection(tt.doc, tt.section, tt.body)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantCreated, created)
		})
	}
}

func TestSectionSlug(t *testing.T) {
	tests := []struct{ section, want string }{
		{"OpenSearch aggregations", "opensearch-aggregations"},
		{"  Trino / Iceberg  ", "trino-iceberg"},
		{"Rule #3: don't join", "rule-3-don-t-join"},
		{"---", ""},
		{strings.Repeat("a", maxPageSlugLen+40), strings.Repeat("a", maxPageSlugLen)},
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			assert.Equal(t, tt.want, sectionSlug(tt.section))
		})
	}
}

func TestIndexEntryAbout(t *testing.T) {
	tests := []struct {
		name  string
		input instructionsPromotionInput
		want  string
	}{
		{
			"the summary is preferred",
			instructionsPromotionInput{Summary: "what the page answers", Body: "Body sentence. More."},
			"what the page answers",
		},
		{
			"without a summary the opening sentence stands in",
			instructionsPromotionInput{Body: "Aggregations go through raw_query. The rest is detail."},
			"Aggregations go through raw_query",
		},
		{
			"a first line with no period still stands in",
			instructionsPromotionInput{Body: "Never join the denormalized indices\nmore text"},
			"Never join the denormalized indices",
		},
		{
			"an opening sentence longer than the entry is cut",
			instructionsPromotionInput{Body: strings.Repeat("w", 260)},
			strings.Repeat("w", 200),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, indexEntryAbout(tt.input))
		})
	}
}

// A promotion onto a built-in page's slug is refused: the next start would
// overwrite it, so the sink names the way forward instead.
func TestPromoteToInstructions_RefusesABuiltinPageSlug(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	pw := newFakePageWriter()
	pw.pages["long-rule"] = &knowledgepage.Page{ID: "p1", Slug: "long-rule", Builtin: true, CurrentVersion: 1}
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "read-only")
	assert.Empty(t, ins.writes)
}

// --- helpers ---

// resultMessage renders a CallToolResult's text for an assertion message.
func resultMessage(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// --- Error paths ---

func TestPromoteToInstructions_ReportsAFailedWrite(t *testing.T) {
	cs := &spyChangesetStore{}
	tk, ins := newInstructionsToolkit(t, ruleInsight(), cs, "")
	ins.setErr = errors.New("write refused")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "write refused")
	assert.Empty(t, cs.Changesets, "no changeset may be recorded for a write that failed")
}

// A failed instructions write on a diverted promotion says the page was
// already written, so the operator knows what state the deployment is in.
func TestPromoteToInstructions_DivertReportsAFailedInstructionsWrite(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	tk.SetPageWriter(newFakePageWriter())
	ins.setErr = errors.New("write refused")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	msg := resultMessage(t, res)
	assert.Contains(t, msg, "wrote the knowledge page")
	assert.Contains(t, msg, "write refused")
}

func TestPromoteToInstructions_ReportsAFailedChangesetRecord(t *testing.T) {
	cs := &spyChangesetStore{InsertErr: errors.New("changeset down")}
	tk, _ := newInstructionsToolkit(t, ruleInsight(), cs, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", []string{"i1"}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "changeset down")
}

// A MarkApplied failure is logged, not fatal: the rule is already live, and
// refusing the response would leave the caller thinking nothing happened.
func TestPromoteToInstructions_SurvivesAFailedMarkApplied(t *testing.T) {
	store := ruleInsight()
	store.MarkAppliedErr = errors.New("mark failed")
	tk, ins := newInstructionsToolkit(t, store, &spyChangesetStore{}, "")

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Query engines", "Trino holds the warehouse.", []string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", resultMessage(t, res))
	require.Len(t, ins.writes, 1)
}

func TestPromoteToInstructions_DivertReportsAFailedSlugLookup(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	pw := newFakePageWriter()
	pw.getErr = errKP
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "looking up knowledge page")
	assert.Empty(t, ins.writes)
}

func TestPromoteToInstructions_DivertRefusesAMalformedReference(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	tk.SetPageWriter(newFakePageWriter())

	in := applyInstructionsInput("Long rule", body, nil)
	in.Instructions.References = []string{"not-a-reference"}
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "invalid page reference")
	assert.Empty(t, ins.writes)
}

func TestPromoteToInstructions_DivertReportsAFailedReferenceCheck(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	pw := newFakePageWriter()
	pw.filterErr = errKP
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("Long rule", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "references")
	assert.Empty(t, ins.writes)
}

// The page half of a diverted promotion cannot revert without a page reverter,
// and the instructions must be left alone when it cannot.
func TestRevertInstructionsChangeset_RefusesADivertWithNoPageReverter(t *testing.T) {
	ins := &fakeInstructionsStore{text: "## Long rule\n\n- ref\n\n"}
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Long rule"),
		ChangeType:    changeIndexInstructions,
		PreviousValue: map[string]any{instructionsFieldText: ""},
		NewValue: map[string]any{
			instructionsFieldText:   ins.text,
			instructionsFieldSlug:   "long-rule",
			instructionsFieldPageOp: changeCreatePage,
		},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "knowledge-page rollback is not configured")
	assert.Equal(t, "## Long rule\n\n- ref\n\n", ins.text)
}

// A page edited after the promotion blocks the rollback of both halves.
func TestRevertInstructionsChangeset_RefusesWhenTheDivertedPageWasEdited(t *testing.T) {
	ins := &fakeInstructionsStore{text: "## Long rule\n\n- ref\n\n"}
	pw := newFakePageWriter()
	pw.pages["long-rule"] = &knowledgepage.Page{ID: "p1", Slug: "long-rule", CurrentVersion: 4}
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Long rule"),
		ChangeType:    changeIndexInstructions,
		PreviousValue: map[string]any{instructionsFieldText: ""},
		NewValue: map[string]any{
			instructionsFieldText:   ins.text,
			instructionsFieldSlug:   "long-rule",
			instructionsFieldPageOp: changeUpdatePage,
			pageFieldVersion:        2,
		},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Pages: pw, Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	var edited *PageEditedError
	require.ErrorAs(t, err, &edited)
	assert.Equal(t, "## Long rule\n\n- ref\n\n", ins.text)
}

// A read failure at rollback time must not be read as "the layer is unchanged".
func TestRevertInstructionsChangeset_ReportsAFailedRead(t *testing.T) {
	ins := &fakeInstructionsStore{getErr: errors.New("db down")}
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Query engines"),
		ChangeType: changeUpdateInstructions,
		NewValue:   map[string]any{instructionsFieldText: "x"},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestRevertInstructionsChangeset_ReportsAFailedRestore(t *testing.T) {
	ins := &fakeInstructionsStore{text: "current"}
	ins.setErr = errors.New("write refused")
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Query engines"),
		ChangeType:    changeUpdateInstructions,
		PreviousValue: map[string]any{instructionsFieldText: "prior"},
		NewValue:      map[string]any{instructionsFieldText: "current"},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restoring the agent instructions")
}

func TestRevertInstructionsChangeset_ReportsAFailedChangesetUpdate(t *testing.T) {
	ins := &fakeInstructionsStore{text: "current"}
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Query engines"),
		ChangeType:    changeUpdateInstructions,
		PreviousValue: map[string]any{instructionsFieldText: "prior"},
		NewValue:      map[string]any{instructionsFieldText: "current"},
	}
	deps := RollbackDeps{
		Changesets:   &spyChangesetStore{RollbackErr: errors.New("changeset down")},
		Instructions: ins,
	}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recording the rollback failed")
}

// A section with nothing a slug can be built from cannot divert onto a page,
// and the refusal names the payload the caller can fix rather than surfacing a
// knowledge-page message on an agent_instructions call.
func TestPromoteToInstructions_DivertNeedsASlug(t *testing.T) {
	body := strings.Repeat("Detail. ", 400)
	tk, ins := newInstructionsToolkit(t, ruleInsight(), &spyChangesetStore{}, "")
	tk.SetPageWriter(newFakePageWriter())

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyInstructionsInput("!!!", body, nil))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultMessage(t, res), "instructions.slug")
	assert.Empty(t, ins.writes)
}

// When the page half of a diverted rollback fails, the layer is already
// restored: the error says so and names the page left behind, rather than
// leaving an operator to discover a stale pointer.
func TestRevertInstructionsChangeset_ReportsAPageLeftInPlace(t *testing.T) {
	ins := &fakeInstructionsStore{text: "## Long rule\n\n- ref\n\n"}
	pw := newFakePageWriter()
	pw.pages["long-rule"] = &knowledgepage.Page{ID: "p1", Slug: "long-rule", CurrentVersion: 2}
	pw.updateErr = errKP
	cs := &Changeset{
		ID: "cs1", TargetURN: InstructionsTargetURN("Long rule"),
		ChangeType:    changeIndexInstructions,
		PreviousValue: map[string]any{instructionsFieldText: "prior"},
		NewValue: map[string]any{
			instructionsFieldText:   ins.text,
			instructionsFieldSlug:   "long-rule",
			instructionsFieldPageOp: changeUpdatePage,
			pageFieldVersion:        2,
		},
	}
	deps := RollbackDeps{Changesets: &spyChangesetStore{}, Pages: pw, Instructions: ins}
	_, err := RevertChangeset(context.Background(), deps, cs, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restored the agent instructions")
	assert.Contains(t, err.Error(), "long-rule")
	assert.Equal(t, "prior", ins.text, "the layer is restored even when the page is not")
}
