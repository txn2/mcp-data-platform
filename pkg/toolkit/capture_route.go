package toolkit

// CaptureRoute tells an agent what to do with a query that worked.
//
// Most queries worth reusing never reach an asset: the agent refines a
// statement, the last one answers the question, the answer goes into chat, and
// the statement is gone. Every result carries its own call_id, and naming that
// id in a capture is the one way the platform learns the query answered
// something (#1321). It deliberately costs a description rather than a
// checkbox: an agent grading its own query for free would grade every query.
// It lives here, in the package every toolkit already depends on, because the
// sentence belongs on every tool whose result is worth reusing: the query tools
// take it through the platform's description overrides, and the API gateway —
// which authors its own descriptions and does not import the middleware —
// appends it directly.
const CaptureRoute = "When a result meets the purpose you stated and the statement is worth " +
	"running again, record it: memory_capture with sources=[\"<the call_id this result returned>\"] " +
	"and a description of what the query answers and any caveats. That is what makes it findable " +
	"by the next person, and what puts it up for promotion to the catalog."
