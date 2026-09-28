package scriptlayer

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptcontract"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// manageScriptDescription is the tool description. It carries the dialect
// contract in-context on purpose: the author is a model, the language is one it
// has read far less of than Python, and the failures it produces are
// predictable — an import, a try block, an f-string, a clock read. Stating what
// is absent up front costs a paragraph and saves a round trip per script.
const manageScriptDescription = "Build and change automations. The unit of an automation is a managed " +
	"script, so this is the tool for a request to automate work or to run it on a schedule or on its own. " +
	"Author, validate, and dry-run managed scripts: small Starlark programs the " +
	"platform stores, versions, and governs so a solved process (a KPI report, a recurring export) can be " +
	"re-run without deriving it again through a conversation. Write a script when the logic is settled and " +
	"the work will repeat; keep using the query tools directly while you are still exploring. " +
	"Call command=help before writing your first one: Starlark is Python-shaped but deliberately smaller, " +
	"and help states exactly what is available. The loop is run_draft (executes source, saved or not, for " +
	"real under YOUR identity and persona, with tighter limits, persisting nothing unless you pass " +
	"allow_writes, and records its host calls), then a test_* function that replays that recording and " +
	"asserts on what main() produced, then test, then create or update. validate parses and reports what " +
	"the script would reach and what a save would say, running nothing but the tests. A save runs the " +
	"tests, and a version that changes what the automation does needs change_summary and user_agreed. " +
	"A saved script runs: run_script executes its latest saved version as the script's own principal, " +
	"presenting the roles you held when you saved it, and a schedule fires it the same way. " +
	"command=versions reads the history of who wrote each version, which is not the same question as " +
	"who owns the script now."

// DialectContract is the dialect contract (internal/platform/scriptcontract).
const DialectContract = scriptcontract.Dialect

// exampleFields renders a built-in example as a get response. It is marked
// builtin so nobody mistakes it for a stored script and tries to patch it.
func exampleFields(e scriptexamples.Example) map[string]any {
	return map[string]any{
		fieldName: e.Name, "description": e.Description, fieldSource: e.Source,
		"builtin": true,
		"message": "This is a built-in worked example, not a stored script. Copy it into create and edit from there.",
	}
}

// knowledgePageRefPrefix is the fetchable form of a knowledge-page key. It is
// written out rather than taken from pkg/portal/knowledgepage so the script
// seam does not depend on the portal's page store to name a page it only
// points at; the scheme is pinned by the drift test in knowledgebuiltin.
const knowledgePageRefPrefix = "mcp:knowledge_page:"

// KnowledgePage names one built-in knowledge page an author should read.
type KnowledgePage struct {
	// Slug is the page's reconcile key and the only identifier stable across
	// deployments: a built-in page's row id is generated at reconcile time, so
	// it differs per deployment and cannot be named in shipped text.
	Slug string `json:"slug"`
	// Reference is the slug in the form fetch takes.
	Reference string `json:"reference"`
	// Summary says what the page answers, so an author fetches the one that
	// bears on the decision in front of it rather than all of them.
	Summary string `json:"summary"`
}

// KnowledgePages is the reading `manage_script help` names, so the tool an
// agent is told to call before writing its first script is the tool that
// routes it to the platform's own authoring guidance instead of leaving that
// guidance to whatever a search happens to rank (#1476).
//
// The slugs are declared here rather than in knowledgebuiltin because
// knowledgebuiltin already imports this package for the dialect contract and
// the reverse import would cycle; a test there fails when the two sets drift.
var KnowledgePages = []KnowledgePage{
	{
		Slug:      "platform-writing-managed-scripts",
		Reference: knowledgePageRefPrefix + "platform-writing-managed-scripts",
		Summary: "The dialect and the authoring loop: what Starlark deliberately lacks, what a " +
			"script may call and the persona that decides it, and what a save makes runnable.",
	},
	{
		Slug:      "platform-reference-script",
		Reference: knowledgePageRefPrefix + "platform-reference-script",
		Summary: "A whole automation written as a saved one is, with the tests it is saved " +
			"with and how each test is recorded and written: start from it.",
	},
	{
		Slug:      "platform-script-outputs-and-export-identity",
		Reference: knowledgePageRefPrefix + "platform-script-outputs-and-export-identity",
		Summary: "Where an output lands and what identity it keeps across runs: a stable name " +
			"refreshes one asset, a dated name builds an archive, and a bucket destination " +
			"delivers the same bytes elsewhere.",
	},
	{
		Slug:      "platform-semi-dynamic-dashboards",
		Reference: knowledgePageRefPrefix + "platform-semi-dynamic-dashboards",
		Summary: "Choosing between composing a whole document every run and publishing one " +
			"document whose data region a schedule refreshes, and the mechanics of the " +
			"second.",
	},
	{
		Slug:      "platform-asset-references-and-the-refresh-loop",
		Reference: knowledgePageRefPrefix + "platform-asset-references-and-the-refresh-loop",
		Summary: "How a document names a file instead of carrying it, and how a run refreshes " +
			"that file so every document naming it shows the new content without being " +
			"re-saved.",
	},
	{
		Slug:      "platform-provenance-and-the-capture-loop",
		Reference: knowledgePageRefPrefix + "platform-provenance-and-the-capture-loop",
		Summary: "Naming sources with call references so an output's provenance is exact, and " +
			"the loop that turns session knowledge into reviewed catalog knowledge.",
	},
}

// handleHelp returns the dialect contract, the capability surface, the example
// names, and the built-in pages that carry the reasoning the contract states
// only in outline.
func (h *Handle) handleHelp(_ context.Context, _ manageScriptInput) (*mcp.CallToolResult, any, error) {
	names := make([]map[string]any, 0, len(scriptexamples.All))
	for _, ex := range scriptexamples.All {
		names = append(names, map[string]any{fieldName: ex.Name, "description": ex.Description})
	}
	return jsonResult(map[string]any{
		"dialect":      DialectContract,
		"capabilities": scriptrun.Capabilities,
		"limits": map[string]any{
			"draft_max_steps":      scriptrun.DraftMaxSteps,
			"draft_timeout":        scriptrun.DraftTimeout.String(),
			"draft_max_rows":       scriptrun.DraftMaxRows,
			"run_max_steps":        h.runLimits.MaxSteps,
			"run_timeout":          h.runLimits.Timeout.String(),
			"run_max_rows":         h.runLimits.MaxRows,
			"run_result_bytes":     h.runLimits.ResultMaxBytes,
			"run_max_memory_bytes": h.runLimits.MaxMemoryBytes,
			"output_max_bytes":     scriptrun.MaxOutputBytes,
			"log_bytes":            scriptrun.MaxLogBytes,
			"max_source_bytes":     script.MaxSourceBytes,
			"state_bytes":          script.MaxStateBytes,
			"note": "A draft run is bounded more tightly than a platform run; the run_ limits are the ones a " +
				"saved script meets on this deployment. A tool a run calls keeps its own ceiling as well: " +
				"a trino_export or api_export inside a run is bounded by that tool's timeout. " +
				"The platform never runs a failed run again on its own. A rate-limit refusal of a " +
				"call is not a script error: the host waits the refusal's interval within the run's " +
				"deadline and issues the call again, and the wait is written to the run's log; an " +
				"upstream's 429 (upstream_retryable) is waited on the same way, at most 3 times. " +
				"A failed run carries cause (script, upstream, transient, memory, worker_lost, platform, " +
				"state_conflict) and retryable, which is true only when running it again is expected " +
				"to succeed. run_max_memory_bytes is the memory one run may hold " +
				"(scripts.worker.max_run_memory; 0 is no budget), measured at every host call over the " +
				"values the script can still reach. A page of rows costs far more once decoded than on the " +
				"wire: a 9 MB page of JSON rows allocates about 14 times its size while it is decoded and " +
				"holds about 4.5 times it afterwards, so page the work and export each page with " +
				"platform.export(..., append=True). A run over the budget fails with " +
				"cause memory and is not retried; run_draft and a run's metrics report " +
				"peak_memory_bytes.",
		},
		"examples":        names,
		"read_an_example": "Call get with name=" + scriptexamples.All[0].Name + " to read one.",
		"see_also":        KnowledgePages,
		"read_a_page":     "Call fetch with the reference to read one in full.",
	})
}
