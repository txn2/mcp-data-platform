package scriptcontract

// ManageScriptDescription is the manage_script tool description. It carries the dialect
// contract in-context on purpose: the author is a model, the language is one it
// has read far less of than Python, and the failures it produces are
// predictable — an import, a try block, an f-string, a clock read. Stating what
// is absent up front costs a paragraph and saves a round trip per script.
const ManageScriptDescription = "Build and change automations. The unit of an automation is a managed " +
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
