package scriptcontract

// knowledgePageRefPrefix is the fetchable form of a knowledge-page key. It is
// written out rather than taken from pkg/portal/knowledgepage so the contract
// does not depend on the portal's page store to name a page it only
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
		Slug:      "platform-managed-script-dialect",
		Reference: knowledgePageRefPrefix + "platform-managed-script-dialect",
		Summary: "The dialect contract this help returns, as a page: every module, member and " +
			"argument a script has, and every rule a save holds it to.",
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
