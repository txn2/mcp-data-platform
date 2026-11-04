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
		Slug:      "platform-application-databases",
		Reference: knowledgePageRefPrefix + "platform-application-databases",
		Summary: "An application's own PostgreSQL database from a script: bound statements, " +
			"platform.transaction and its retry contract, tx.savepoint, platform.batch, query_iter, copy_in and copy_out, the values each type maps to, and the schema.",
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
	// Designing an application's PostgreSQL database (#2448).
	{
		Slug:      "platform-postgres-write-patterns",
		Reference: knowledgePageRefPrefix + "platform-postgres-write-patterns",
		Summary:   "Designing an application database's writes: upsert with ON CONFLICT, RETURNING, MERGE, batches as arrays, optimistic concurrency and idempotency keys.",
	},
	{
		Slug:      "platform-postgres-integrity",
		Reference: knowledgePageRefPrefix + "platform-postgres-integrity",
		Summary:   "Rules the database enforces: constraints, partial unique indexes, exclusion constraints, deferrable constraints, domains, generated columns, keys and money.",
	},
	{
		Slug:      "platform-postgres-documents",
		Reference: knowledgePageRefPrefix + "platform-postgres-documents",
		Summary:   "jsonb against columns, GIN and expression indexes, and SQL/JSON on 17.",
	},
	{
		Slug:      "platform-postgres-search",
		Reference: knowledgePageRefPrefix + "platform-postgres-search",
		Summary:   "Full text search, pg_trgm and case-insensitive keys over an application's own rows.",
	},
	{
		Slug:      "platform-postgres-time",
		Reference: knowledgePageRefPrefix + "platform-postgres-time",
		Summary:   "timestamptz, zones for recurrence, half-open ranges, buckets, and months that are not seconds.",
	},
	{
		Slug:      "platform-postgres-functions-triggers",
		Reference: knowledgePageRefPrefix + "platform-postgres-functions-triggers",
		Summary:   "SQL functions with BEGIN ATOMIC, SECURITY DEFINER done safely, statement and constraint triggers, and when a trigger is wrong.",
	},
	{
		Slug:      "platform-postgres-security",
		Reference: knowledgePageRefPrefix + "platform-postgres-security",
		Summary:   "Login roles per persona, grants, row level security reading the caller's claims, and security_invoker views.",
	},
	{
		Slug:      "platform-postgres-queues-locks-events",
		Reference: knowledgePageRefPrefix + "platform-postgres-queues-locks-events",
		Summary:   "A SKIP LOCKED job queue and what limits it, advisory and row locks, SERIALIZABLE with retry, LISTEN/NOTIFY and the outbox.",
	},
	{
		Slug:      "platform-postgres-partitioning-caching-bulk",
		Reference: knowledgePageRefPrefix + "platform-postgres-partitioning-caching-bulk",
		Summary:   "Range partitions and retention, materialized views, unlogged tables, and bulk data through copy_in and copy_out.",
	},
	{
		Slug:      "platform-postgres-queries-and-values",
		Reference: knowledgePageRefPrefix + "platform-postgres-queries-and-values",
		Summary:   "Keyset pagination, window functions, DISTINCT ON, LATERAL, grouping sets, recursive CTEs, and each type's value in a script.",
	},
	{
		Slug:      "platform-postgres-platform-provides",
		Reference: knowledgePageRefPrefix + "platform-postgres-platform-provides",
		Summary:   "What the platform runs so an application does not build it in PostgreSQL: schedules, job runs, row events, files, endpoints, mail.",
	},
	{
		Slug:      "platform-postgres-what-does-not-work",
		Reference: knowledgePageRefPrefix + "platform-postgres-what-does-not-work",
		Summary:   "What does not work through pooled connections (a procedure that commits, session state) and common claims, corrected.",
	},
}
