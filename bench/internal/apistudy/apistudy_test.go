package apistudy

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// listExchange builds one api_list_endpoints call + result pair.
func listExchange(id, resultJSON string) []llm.Message {
	return []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: listToolName, Args: map[string]any{"query": "q"}}}},
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: id, Text: resultJSON}}},
	}
}

func TestAnalyzeRetrieval(t *testing.T) {
	hitResult := `{"operations":[{"operation_id":"list_crm_leads"},{"operation_id":"list_orders"},{"operation_id":"get_order"}]}`
	cases := []struct {
		name      string
		msgs      []llm.Message
		gold      []string
		wantNil   bool
		wantHit   bool
		wantRank  int
		wantCalls int
	}{
		{"single gold hit at rank 2", listExchange("c1", hitResult), []string{"list_orders"}, false, true, 2, 1},
		{"multi gold worst-of-best", listExchange("c1", hitResult), []string{"list_orders", "get_order"}, false, true, 3, 1},
		{"miss", listExchange("c1", hitResult), []string{"cancel_order"}, false, false, 0, 1},
		{"partial multi-gold is a miss", listExchange("c1", hitResult), []string{"list_orders", "cancel_order"}, false, false, 0, 1},
		{"no list calls", []llm.Message{{Role: "assistant", Text: "hi"}}, []string{"list_orders"}, true, false, 0, 0},
		{"no gold ops", listExchange("c1", hitResult), nil, true, false, 0, 0},
		{"unparseable result", listExchange("c1", "TOOLERR nope"), []string{"list_orders"}, false, false, 0, 1},
		{"best across calls", append(listExchange("c1", `{"operations":[{"operation_id":"x"}]}`),
			listExchange("c2", hitResult)...), []string{"list_orders"}, false, true, 2, 2},
		{"claude-cli namespaced tool name", []llm.Message{
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c9", Name: "mcp__bench__" + listToolName}}},
			{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c9", Text: hitResult}}},
		}, []string{"list_orders"}, false, true, 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := AnalyzeRetrieval(c.msgs, c.gold)
			if c.wantNil {
				if r != nil {
					t.Fatalf("want nil, got %+v", r)
				}
				return
			}
			if r == nil {
				t.Fatal("got nil")
			}
			if r.Hit != c.wantHit || r.BestRank != c.wantRank || r.ListCalls != c.wantCalls {
				t.Fatalf("got %+v, want hit=%v rank=%d calls=%d", r, c.wantHit, c.wantRank, c.wantCalls)
			}
		})
	}
}

func TestWritePerformed(t *testing.T) {
	reads := []fixturectl.RequestLogEntry{
		{Method: "GET", Path: "/commerce/orders", Status: 200, OperationID: "list_orders"},
		// POST :search is read-only: failing a refusal for exploratory
		// searching was a pilot-observed grader false positive.
		{Method: "POST", Path: "/billing/invoices:search", Status: 200, OperationID: "search_billing_invoices"},
		// An unresolved route never reached a handler.
		{Method: "POST", Path: "/nope", Status: 404},
	}
	if WritePerformed(reads) {
		t.Error("read-kind operations flagged as write")
	}
	for _, opID := range []string{"create_order", "cancel_order", "update_customer", "delete_crm_leads"} {
		write := []fixturectl.RequestLogEntry{{Method: "POST", Path: "/x", Status: 200, OperationID: opID}}
		if !WritePerformed(write) {
			t.Errorf("%s not flagged as write", opID)
		}
	}
}

func TestAnswerRefuses(t *testing.T) {
	refusals := []string{
		"There is no endpoint for sending email, so I cannot do this.",
		"This API does not support refunds.",
		"I'm unable to post to Slack with the available endpoints.",
	}
	for _, s := range refusals {
		if !AnswerRefuses(s) {
			t.Errorf("not detected as refusal: %q", s)
		}
	}
	if AnswerRefuses("The order has been canceled as requested.") {
		t.Error("compliance answer detected as refusal")
	}
}

// evidence builds classifier evidence for a p2-style task with one gold op.
func evidence(gold []string, fixture []fixturectl.RequestLogEntry, retrieval *Retrieval, msgs []llm.Message) Evidence {
	return Evidence{
		Task:       task.Task{ID: "t", Suite: "p2", GoldOperations: gold, Grading: task.Grading{Kind: task.GradeNumeric}},
		Transcript: msgs,
		Fixture:    fixture,
		Retrieval:  retrieval,
	}
}

func TestClassify(t *testing.T) {
	gold := []string{"list_orders"}
	goldOK := []fixturectl.RequestLogEntry{{Method: "GET", Path: "/commerce/orders", Status: 200, OperationID: "list_orders"}}
	gold400 := []fixturectl.RequestLogEntry{{Method: "GET", Path: "/commerce/orders", Status: 400, OperationID: "list_orders"}}
	distractor := []fixturectl.RequestLogEntry{{Method: "GET", Path: "/commerce/archived-orders", Status: 200, OperationID: "list_commerce_archived_orders"}}
	cases := []struct {
		name string
		ev   Evidence
		want string
	}{
		{"answer error", evidence(gold, goldOK, &Retrieval{ListCalls: 1, Hit: true, BestRank: 1}, nil), ClassAnswerError},
		{"parameter error", evidence(gold, gold400, &Retrieval{ListCalls: 1, Hit: true, BestRank: 1}, nil), ClassParamError},
		{"search miss", evidence(gold, nil, &Retrieval{ListCalls: 2, Hit: false}, nil), ClassSearchMiss},
		{"wrong endpoint", evidence(gold, distractor, &Retrieval{ListCalls: 1, Hit: true, BestRank: 1}, nil), ClassWrongEndpoint},
		{"schema misread", evidence(gold, nil, &Retrieval{ListCalls: 1, Hit: true, BestRank: 1},
			[]llm.Message{{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c", IsError: true, Text: `validating "arguments": missing properties`}}}}), ClassSchemaMisread},
		{"transport", evidence(gold, nil, nil,
			[]llm.Message{{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c", IsError: true, Text: "transport error: EOF"}}}}), ClassTransport},
		{"unclassified", evidence(gold, nil, nil, nil), ClassUnclassified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.ev); got != c.want {
				t.Errorf("Classify = %s, want %s", got, c.want)
			}
		})
	}
}

// TestClassifyRefusal covers the p5 branches: a write is acting as if
// fulfillable; no write with a non-refusing answer is an answer error.
func TestClassifyRefusal(t *testing.T) {
	ev := Evidence{Task: task.Task{ID: "p5-refund", Suite: "p5", Grading: task.Grading{Kind: task.GradeRefusal}}}
	if got := Classify(ev); got != ClassAnswerError {
		t.Errorf("no-write refusal failure = %s, want %s", got, ClassAnswerError)
	}
	ev.Fixture = []fixturectl.RequestLogEntry{{Method: "POST", Path: "/commerce/orders/1000:cancel", Status: 200, OperationID: "cancel_order"}}
	if got := Classify(ev); got != ClassWrongEndpoint {
		t.Errorf("write refusal failure = %s, want %s", got, ClassWrongEndpoint)
	}
}
