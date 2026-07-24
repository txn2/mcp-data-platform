// Package apistudy holds the API-connection study's per-attempt analysis
// (#1027): retrieval hit rate extracted from the episode transcript,
// deterministic refusal write-detection over the fixture access log, and
// the deterministic-first failure-taxonomy classifier. The pipeline calls
// into it only when a run carries a fixture client (the b* arms); a* runs
// are untouched.
package apistudy

import (
	"encoding/json"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// listToolName is the b1 discovery tool whose results carry ranked
// operations.
const listToolName = "api_list_endpoints"

// isListCall matches the discovery tool under both transcript namings:
// the in-process loop records the bare platform tool name, while Claude
// Code namespaces MCP tools as mcp__<server>__<tool>.
func isListCall(name string) bool {
	return name == listToolName || strings.HasSuffix(name, "__"+listToolName)
}

// Failure taxonomy classes (RQ4). Classified deterministically from the
// transcript plus the fixture access log; episodes the rules cannot place
// are Unclassified (candidates for a judged pass), never silently forced
// into a class.
const (
	ClassSearchMiss    = "search_miss"
	ClassWrongEndpoint = "wrong_endpoint"
	ClassSchemaMisread = "schema_misread"
	ClassParamError    = "parameter_error"
	ClassTransport     = "transport_error"
	ClassAnswerError   = "answer_error"
	ClassUnclassified  = "unclassified"
)

// Retrieval is one episode's discovery outcome, computed from the
// transcript's api_list_endpoints calls (the platform's audit events
// carry no payloads, so the transcript is the payload source).
type Retrieval struct {
	// ListCalls counts api_list_endpoints invocations in the episode.
	ListCalls int `json:"list_calls"`
	// Hit reports whether every gold operation surfaced in at least one
	// result set.
	Hit bool `json:"hit"`
	// BestRank is the worst-over-gold-operations of the best (1-based)
	// rank each gold operation reached in any result set; 0 when a gold
	// operation never surfaced. For single-gold tasks this is simply the
	// operation's best rank.
	BestRank int `json:"best_rank"`
}

// AnalyzeRetrieval extracts the retrieval outcome. Returns nil when the
// episode made no api_list_endpoints calls or the task names no gold
// operations (b0 and b2 arms, irrelevance tasks): retrieval is then not a
// measured dimension of the attempt.
func AnalyzeRetrieval(msgs []llm.Message, goldOps []string) *Retrieval {
	if len(goldOps) == 0 {
		return nil
	}
	results := resultsByCallID(msgs)
	r := &Retrieval{}
	// bestRank[op] = best 1-based rank seen; missing = never surfaced.
	bestRank := map[string]int{}
	for _, m := range msgs {
		for _, call := range m.ToolCalls {
			if !isListCall(call.Name) {
				continue
			}
			r.ListCalls++
			rankOperations(results[call.ID], bestRank)
		}
	}
	if r.ListCalls == 0 {
		return nil
	}
	r.Hit = true
	for _, op := range goldOps {
		rank, ok := bestRank[op]
		if !ok {
			r.Hit = false
			r.BestRank = 0
			return r
		}
		r.BestRank = max(r.BestRank, rank)
	}
	return r
}

// resultsByCallID indexes tool-result text by the call it answers.
func resultsByCallID(msgs []llm.Message) map[string]string {
	out := map[string]string{}
	for _, m := range msgs {
		for _, res := range m.ToolResults {
			out[res.CallID] = res.Text
		}
	}
	return out
}

// rankOperations merges one list result's ranked operations into the
// best-rank map. Result text that does not parse as the expected envelope
// contributes nothing (a tool error, a truncated result).
func rankOperations(text string, bestRank map[string]int) {
	var payload struct {
		Operations []struct {
			OperationID string `json:"operation_id"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return
	}
	for i, op := range payload.Operations {
		rank := i + 1
		if prev, ok := bestRank[op.OperationID]; !ok || rank < prev {
			bestRank[op.OperationID] = rank
		}
	}
}

// writeOpPrefixes are the operation-kind prefixes that mutate state.
// Every catalog operationId is kind-prefixed (list_/get_/search_/
// aggregate_ are reads; create_/update_/delete_ and the gold cancel_ are
// writes), so kind is derivable from the resolved id.
var writeOpPrefixes = []string{"create_", "update_", "delete_", "cancel_"}

// WritePerformed reports whether the fixture access log contains a
// state-changing request (the deterministic half of refusal grading: an
// agent that mutates state treated the request as fulfillable). The
// control plane is not access-logged, so every entry is a catalog call.
// Classification is by operation kind, not HTTP method: the catalog's
// :search custom method is a POST but is read-only, and failing a
// refusal for exploratory searching would be a grader false positive
// (observed in the t0 pilot). An entry with no resolved operation never
// reached a handler (404) and cannot have mutated state.
func WritePerformed(reqs []fixturectl.RequestLogEntry) bool {
	for _, r := range reqs {
		for _, p := range writeOpPrefixes {
			if strings.HasPrefix(r.OperationID, p) {
				return true
			}
		}
	}
	return false
}

// refusalMarkers are the lexical signals of a stated refusal, the
// fallback when no judge is wired. Matched case-insensitively.
var refusalMarkers = []string{
	"cannot", "can't", "can not", "unable", "not possible", "not available",
	"no endpoint", "no api", "not supported", "does not support", "doesn't support",
	"not able", "no way to", "not exposed", "no tool", "no such capability",
	"no capability", "outside", "isn't available", "is not available",
}

// AnswerRefuses is the lexical fallback for the stated-unavailability half
// of refusal grading: whether the final answer reads as declining. The
// publication runs judge this with the pinned LLM judge; the fallback
// exists so smokes and pilots grade without judge budget, and its use is
// recorded on the attempt.
func AnswerRefuses(answer string) bool {
	lower := strings.ToLower(answer)
	for _, m := range refusalMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// Evidence is everything the classifier may consult for one failed
// attempt.
type Evidence struct {
	Task       task.Task
	Transcript []llm.Message
	Fixture    []fixturectl.RequestLogEntry
	Retrieval  *Retrieval
}

// boundaryErrorMarkers identify tool errors rejected at the tool boundary
// (schema validation, missing path parameters) before any HTTP call
// reached the fixture: the schema_misread signature.
var boundaryErrorMarkers = []string{
	"validating", "missing properties", "missing required path parameter",
	"invalid arguments",
}

// Classify places one failed, graded attempt into the RQ4 taxonomy.
// Callers must not invoke it for correct attempts or harness failures.
func Classify(ev Evidence) string {
	if ev.Task.Grading.Kind == task.GradeRefusal {
		if WritePerformed(ev.Fixture) {
			return ClassWrongEndpoint // acted as if the request were fulfillable
		}
		return ClassAnswerError // no wrong action, but the answer did not refuse
	}
	return classifyTask(ev, goldCalls(ev))
}

// classifyTask runs the non-refusal rule cascade.
func classifyTask(ev Evidence, gold goldCallState) string {
	switch {
	case transportFailed(ev.Transcript) && !gold.succeeded:
		return ClassTransport
	case searchMissed(ev, gold):
		return ClassSearchMiss
	case !gold.invoked:
		return classifyNeverReachedGold(ev)
	case gold.clientErrored:
		return ClassParamError
	case gold.succeeded:
		return ClassAnswerError
	default:
		return ClassUnclassified
	}
}

// classifyNeverReachedGold splits the no-gold-call failures: invoking
// something else is wrong_endpoint; failing at the tool boundary before
// any call reached the fixture is schema_misread.
func classifyNeverReachedGold(ev Evidence) string {
	switch {
	case otherCatalogInvoked(ev.Fixture):
		return ClassWrongEndpoint
	case boundaryErrored(ev.Transcript):
		return ClassSchemaMisread
	default:
		return ClassUnclassified
	}
}

// goldCallState summarizes the fixture log's view of the task's gold
// operations.
type goldCallState struct {
	invoked       bool // any gold operation reached the fixture
	succeeded     bool // any gold call returned 2xx
	clientErrored bool // any gold call returned 4xx
}

// goldCalls scans the fixture access log for the task's gold operations.
func goldCalls(ev Evidence) goldCallState {
	goldSet := map[string]bool{}
	for _, op := range ev.Task.GoldOperations {
		goldSet[op] = true
	}
	var s goldCallState
	for _, r := range ev.Fixture {
		if !goldSet[r.OperationID] {
			continue
		}
		s.invoked = true
		switch {
		case r.Status >= 200 && r.Status < 300:
			s.succeeded = true
		case r.Status >= 400 && r.Status < 500:
			s.clientErrored = true
		}
	}
	return s
}

// searchMissed reports the search_miss signature: discovery ran, no gold
// operation ever surfaced, and none was invoked anyway.
func searchMissed(ev Evidence, gold goldCallState) bool {
	return ev.Retrieval != nil && !ev.Retrieval.Hit && !gold.invoked
}

// otherCatalogInvoked reports whether any non-gold catalog operation was
// invoked (wrong_endpoint's positive signal). Gold operations are
// excluded by the caller's rule order (gold.invoked is false there).
func otherCatalogInvoked(reqs []fixturectl.RequestLogEntry) bool {
	return len(reqs) > 0
}

// transportFailed reports whether the transcript carries transport-level
// tool failures (the in-process loop marks these; a dead upstream or
// protocol failure).
func transportFailed(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, res := range m.ToolResults {
			if res.IsError && strings.HasPrefix(res.Text, "transport error:") {
				return true
			}
		}
	}
	return false
}

// boundaryErrored reports whether any tool call failed at the tool
// boundary before reaching the fixture (schema_misread's signature).
func boundaryErrored(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, res := range m.ToolResults {
			if !res.IsError {
				continue
			}
			lower := strings.ToLower(res.Text)
			for _, marker := range boundaryErrorMarkers {
				if strings.Contains(lower, marker) {
					return true
				}
			}
		}
	}
	return false
}
