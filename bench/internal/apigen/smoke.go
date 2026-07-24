package apigen

import (
	"fmt"
	"maps"
	"strconv"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// ScriptedSmoke emits the deterministic playback script for the study
// (the report-1 pattern): every task discovers via api_list_endpoints,
// invokes a gold operation through the b1 invoke shape, and answers with
// the generated ground truth — so one no-API-key run proves harness,
// platform, fixture behavior, and grading agree end to end. Mutation
// tasks perform their real state changes (state grading would fail
// otherwise); irrelevance tasks decline without any invoke.
func ScriptedSmoke(tasks []task.Task) llm.Script {
	tr := newTruths(GenerateState(BuildCatalog()).Dataset)
	script := llm.Script{}
	for _, t := range tasks {
		script[t.ID] = smokeSteps(tr, t)
	}
	return script
}

// smokeSteps builds one task's playback.
func smokeSteps(tr *truths, t task.Task) []llm.Step {
	if t.Grading.Kind == task.GradeRefusal {
		return []llm.Step{{FinalText: "FINAL ANSWER: This capability is not available through the registered API; no endpoint supports it."}}
	}
	steps := []llm.Step{listStep(t)}
	for _, call := range smokeInvokes(tr, t) {
		steps = append(steps, llm.Step{ToolCalls: []llm.ToolCall{call}})
	}
	return append(steps, llm.Step{FinalText: "FINAL ANSWER: " + finalAnswer(t)})
}

// listStep is the discovery call: querying by the first gold operation's
// id, whose per-operation text contains it, so the smoke also exercises a
// retrieval hit.
func listStep(t task.Task) llm.Step {
	return llm.Step{ToolCalls: []llm.ToolCall{{
		Name: "api_list_endpoints",
		Args: map[string]any{"connection": "acme", "query": t.GoldOperations[0], "limit": 10},
	}}}
}

// finalAnswer renders the graded answer from the task's own ground truth.
func finalAnswer(t task.Task) string {
	switch t.Grading.Kind {
	case task.GradeNumeric:
		return strconv.FormatFloat(*t.Grading.Value, 'f', -1, 64)
	case task.GradeEntity:
		return t.Grading.Aliases[0]
	default: // state: graded from the fixture dump, not the answer
		return "done"
	}
}

// invoke builds one api_invoke_endpoint call.
func invoke(method, path string, extra map[string]any) llm.ToolCall {
	args := map[string]any{"connection": "acme", "method": method, "path": path}
	maps.Copy(args, extra)
	return llm.ToolCall{Name: "api_invoke_endpoint", Args: args}
}

// smokeInvokes returns the task's gold invocations: real mutations for
// state tasks (recomputed from the same deterministic exemplar pickers the
// task generator uses; any drift fails state grading loudly), and a
// minimal valid gold call otherwise.
func smokeInvokes(tr *truths, t task.Task) []llm.ToolCall {
	if t.Grading.Kind == task.GradeState {
		return mutationInvokes(tr, t.ID)
	}
	return []llm.ToolCall{goldInvoke(t.GoldOperations[0])}
}

// goldInvoke maps a gold operation to a minimal valid call. Exemplar ids
// are stable dataset members (customer ids are 1-based, order ids start
// at 1000).
func goldInvoke(opID string) llm.ToolCall {
	switch opID {
	case "list_customers":
		return invoke("GET", "/crm/customers", map[string]any{"query_params": map[string]any{"page_size": 100}})
	case "get_customer":
		return invoke("GET", "/crm/customers/10", nil)
	case "aggregate_customers":
		return invoke("GET", "/crm/customers:aggregate", map[string]any{"query_params": map[string]any{"group_by": "region"}})
	case "list_customer_orders":
		return invoke("GET", "/crm/customers/10/orders", nil)
	case "list_orders":
		return invoke("GET", "/commerce/orders", map[string]any{"query_params": map[string]any{"page_size": 100}})
	case "get_order":
		return invoke("GET", "/commerce/orders/1000", nil)
	case "aggregate_orders":
		return invoke("GET", "/commerce/orders:aggregate", map[string]any{"query_params": map[string]any{"group_by": "status"}})
	default:
		panic("apigen: no smoke invoke for gold operation " + opID)
	}
}

// mutationInvokes rebuilds each state task's real mutation calls.
func mutationInvokes(tr *truths, taskID string) []llm.ToolCall {
	cancel := func(id int) llm.ToolCall {
		return invoke("POST", fmt.Sprintf("/commerce/orders/%d:cancel", id), nil)
	}
	create := func(customerID int, amount int64) llm.ToolCall {
		return invoke("POST", "/commerce/orders", map[string]any{"body": map[string]any{"customer_id": customerID, "amount": amount}})
	}
	switch taskID {
	case "p3-cancel-order":
		return []llm.ToolCall{cancel(tr.pending[0].ID)}
	case "p3-cancel-only-pending":
		_, oOne := tr.customerWithOnePending()
		return []llm.ToolCall{cancel(oOne.ID)}
	case "p3-create-dollars":
		return []llm.ToolCall{create(12, 15000)}
	case "p3-create-cents":
		return []llm.ToolCall{create(33, 250000)}
	case "p3-cancel-two":
		return []llm.ToolCall{cancel(tr.pending[2].ID), cancel(tr.pending[3].ID)}
	case "p3-create-named":
		return []llm.ToolCall{create(tr.uniqueNamed[2].ID, 9900)}
	default:
		return []llm.ToolCall{customerPatchInvoke(tr, taskID)}
	}
}

// customerPatchInvoke rebuilds the update_customer mutations.
func customerPatchInvoke(tr *truths, taskID string) llm.ToolCall {
	patch := func(id int, body map[string]any) llm.ToolCall {
		return invoke("PATCH", fmt.Sprintf("/crm/customers/%d", id), map[string]any{"body": body})
	}
	switch taskID {
	case "p3-move-region":
		return patch(tr.customerNotIn("South", "").ID, map[string]any{"region": "South"})
	case "p3-upgrade-tier":
		return patch(tr.customerNotIn("", "enterprise").ID, map[string]any{"tier": "enterprise"})
	case "p3-downgrade-tier":
		return patch(tr.customerNotIn("", "basic").ID, map[string]any{"tier": "basic"})
	case "p3-move-and-upgrade":
		return patch(tr.customerNotIn("North", "plus").ID, map[string]any{"region": "North", "tier": "plus"})
	default:
		panic("apigen: no smoke mutations for state task " + taskID)
	}
}
