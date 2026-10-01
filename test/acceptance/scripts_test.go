//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
)

// saveScript saves a script the way a script is saved since #1939: a draft of
// its source records the host calls it makes and the answers they got, and the
// script is created with a test that replays that recording and asserts on what
// the draft produced. create is the create call's arguments (command, name,
// source, params, ...); args binds the draft's parameters. It fails the test
// when the draft fails or the save is refused, and returns the create's answer.
//
// The assertion is read off the draft: the length of everything it exported,
// notified and published as JSON (so the test reads every output, #1952), the
// row count of each output, the value platform.result handed back, the state
// the run would save, and failing all of them the log. A test written this way
// asserts what the script made of the data, so the save's check that a test
// depends on the recorded rows holds it to something.
func (c *client) saveScript(create, args map[string]any) map[string]any {
	c.t.Helper()
	return c.saveScriptCovering(create, args)
}

// saveScriptCovering is saveScript for a script whose paths one draft does
// not all take: each argument set is drafted, and the script is saved with a
// test replaying each draft, which is how its tests reach the statements a
// save requires.
func (c *client) saveScriptCovering(create map[string]any, argSets ...map[string]any) map[string]any {
	c.t.Helper()
	if len(argSets) == 0 {
		argSets = []map[string]any{nil}
	}
	sent := c.tested(create, argSets[0])
	for i, args := range argSets[1:] {
		more := c.tested(create, args)
		test := strings.TrimPrefix(fmt.Sprint(more["source"]), strings.TrimRight(fmt.Sprint(create["source"]), "\n")+"\n")
		test = strings.Replace(test, "def test_recorded_draft(", fmt.Sprintf("def test_recorded_draft_%d(", i+2), 1)
		sent["source"] = fmt.Sprint(sent["source"]) + test
	}
	out := c.call("manage_script", sent)
	if out["status"] != "created" {
		c.t.Fatalf("saveScript: %v was not saved: %v", create["name"], out)
	}
	return out
}

// saveEdit is saveScript for an update of a saved script's source: the new
// source is drafted, and saved with a test replaying the draft. The edit is
// the one the criterion is about, and a person agreed to what it changes, so
// it carries a change summary saying so (#1942): a criterion ABOUT the
// summary sends its own update rather than this.
func (c *client) saveEdit(update, args map[string]any) map[string]any {
	c.t.Helper()
	sent := c.tested(update, args)
	sent["change_summary"] = "The edit this acceptance criterion makes."
	sent["user_agreed"] = true
	out := c.call("manage_script", sent)
	if out["status"] != "updated" {
		c.t.Fatalf("saveEdit: %v was not saved: %v", update["name"], out)
	}
	return out
}

// tested is a create or update of a script's source with the test a save
// needs: the source is drafted, and the test replays that draft's recording.
//
// An update is drafted under a scratch name, because a draft of a saved
// script binds its values against the SAVED parameters, and an edit may be
// changing them. A draft stopped at a write is drafted again with its writes
// allowed, as its author would: its output depends on what it wrote. A
// script whose draft fails whatever it is given -- a criterion about failure
// handling -- is saved with a test that the failure holds.
func (c *client) tested(save, args map[string]any) map[string]any {
	c.t.Helper()
	source, _ := save["source"].(string)
	name, _ := save["name"].(string)
	if save["command"] == "update" {
		name += "-edit-draft"
	}
	draft := map[string]any{"command": "run_draft", "name": name, "source": source}
	params, declared := save["params"]
	if !declared && save["command"] == "update" {
		// An edit that sends no parameters keeps the saved ones.
		params, declared = c.call("manage_script", map[string]any{"command": "get", "name": save["name"]})["params"]
	}
	if declared {
		draft["params"] = params
	}
	if args == nil {
		args = draftArgs(params)
	}
	if len(args) > 0 {
		draft["args"] = args
	}
	ran := c.call("manage_script", draft)
	if ran["status"] != "succeeded" && stoppedAtAWrite(ran) {
		draft["allow_writes"] = true
		ran = c.call("manage_script", draft)
	}
	recording, _ := ran["recording"].(string)
	if recording == "" {
		c.t.Fatalf("the draft of %v kept no recording: %v", save["name"], ran)
	}
	withTest := maps.Clone(save)
	base := strings.TrimRight(source, "\n") + "\n"
	withTest["source"] = base + recordedTest(recording, ran, c.producedDigest(name, base, recording, ran))
	return withTest
}

// producedDigest is the length of everything a replay of the recording
// exports, notifies and publishes, as JSON: what the saved test pins so that
// it reads every output it produces (#1952). It is measured by running a
// probe test through command=test, which replays the recording without
// saving anything.
func (c *client) producedDigest(name, base, recording string, ran map[string]any) int {
	c.t.Helper()
	probe := base + testHead(recording) + "    " + entryCall(ran) + "\n" +
		"    print(\"digest:%d\" % len(json.encode(" + producedExpr + ")))\n"
	tested := c.call("manage_script", map[string]any{"command": "test", "name": name, "source": probe})
	tests, _ := tested["tests"].([]any)
	if len(tests) == 0 {
		c.t.Fatalf("the probe of %v's outputs ran no test: %v", name, tested)
	}
	// The probe is the recorded-draft test; the source may carry its own.
	var first map[string]any
	for _, it := range tests {
		if m, _ := it.(map[string]any); m["name"] == "test_recorded_draft" {
			first = m
		}
	}
	log, _ := first["log"].(string)
	_, digest, found := strings.Cut(log, "digest:")
	n, err := strconv.Atoi(strings.TrimSpace(digest))
	if !found || err != nil {
		c.t.Fatalf("the probe of %v's outputs printed no digest: %v", name, first)
	}
	return n
}

// producedExpr is what a recorded-draft test reads of testing.outputs() as a
// whole: every export (its rows, every column, or its body), every
// notification and every published region.
const producedExpr = "[out.exports, out.notifies, out.publishes]"

func testHead(recording string) string {
	return "\ndef test_recorded_draft():\n" +
		"    \"\"\"The recorded draft replays to what it produced.\"\"\"\n" +
		"    testing.replay(" + strconv.Quote(recording) + ")\n"
}

// entryCall is how a recorded-draft test calls main(): directly, or, for a
// draft that failed, expecting the failure it ended with.
func entryCall(ran map[string]any) string {
	if ran["status"] != "succeeded" {
		return "assert.contains(assert.fails(main), " + strconv.Quote(failureLine(ran)) + ")\n    out = testing.outputs()"
	}
	return "main()\n    out = testing.outputs()"
}

// stoppedAtAWrite reports whether a draft failed because it wrote, or read
// back what a write would have made.
func stoppedAtAWrite(ran map[string]any) bool {
	if ran["refused_write"] != nil {
		return true
	}
	msg, _ := ran["error"].(string)
	return strings.Contains(msg, "does not write") || strings.Contains(msg, "not in dict") ||
		strings.Contains(msg, "registers nothing")
}

// draftArgs binds a value of its type to every required parameter the save
// declares, so a draft of a script whose criterion binds nothing still runs.
func draftArgs(params any) map[string]any {
	list, _ := params.([]any)
	if list == nil {
		if typed, ok := params.([]map[string]any); ok {
			for _, p := range typed {
				list = append(list, p)
			}
		}
	}
	args := map[string]any{}
	for _, raw := range list {
		p, _ := raw.(map[string]any)
		if required, _ := p["required"].(bool); !required || p["default"] != nil {
			continue
		}
		name, _ := p["name"].(string)
		switch p["type"] {
		case "date":
			args[name] = "2026-09-20"
		case "int":
			args[name] = 1
		case "float":
			args[name] = 1.5
		case "bool":
			args[name] = true
		case "enum":
			if values, _ := p["values"].([]any); len(values) > 0 {
				args[name] = values[0]
			}
		case "list":
			args[name] = []any{listItem(p["items"])}
		default:
			args[name] = "acceptance"
		}
	}
	return args
}

// recordedTest is the test_* function saveScript saves a script with: it
// replays the draft, pins the digest of everything the draft produced, and
// asserts the row count of each output, the value platform.result handed
// back and the state the run would save, and failing all of them the log.
func recordedTest(recording string, ran map[string]any, digest int) string {
	asserts := []string{fmt.Sprintf("assert.eq(len(json.encode(%s)), %d)", producedExpr, digest)}
	if ran["status"] != "succeeded" {
		if discarded, ok := ran["state_discarded"]; ok && discarded != nil {
			// A failed draft's save_state, which a run discards (#2002).
			asserts = append(asserts, "assert.eq(out.state_discarded, json.decode("+jsonLiteral(discarded)+"))")
		}
		return testHead(recording) + "    " + entryCall(ran) + "\n    " + strings.Join(asserts, "\n    ") + "\n"
	}
	if exports, _ := ran["exports"].([]any); len(exports) > 0 {
		counts := make([]string, 0, len(exports))
		for _, e := range exports {
			m, _ := e.(map[string]any)
			counts = append(counts, fmt.Sprint(m["row_count"]))
		}
		asserts = append(asserts, "assert.eq([e.row_count for e in out.exports], ["+strings.Join(counts, ", ")+"])")
	}
	if result, ok := ran["result"]; ok && result != nil {
		asserts = append(asserts, "assert.eq(out.result, json.decode("+jsonLiteral(result)+"))")
	}
	if state, ok := ran["state"]; ok && state != nil {
		// A draft's state is what a run would commit: its save_state, or its
		// checkpoint when it saved none (#2003), which a test reads as such.
		field := "state"
		if ran["state_checkpoint"] == true {
			field = "checkpoint"
		}
		asserts = append(asserts, "assert.eq(out."+field+", json.decode("+jsonLiteral(state)+"))")
	}
	if len(asserts) == 1 {
		log, _ := ran["log"].(string)
		asserts = append(asserts, "assert.eq(out.log, "+strconv.Quote(log)+")")
	}
	return testHead(recording) + "    " + entryCall(ran) + "\n    " + strings.Join(asserts, "\n    ") + "\n"
}

// failureLine is the opening words of the failure a draft ended with, which
// the failure a replay of it raises opens with too: the binding prefixes
// ("Error in x: in x: ") are dropped, and so is everything past the first
// forty characters, where a measured number can differ between two runs.
func failureLine(ran map[string]any) string {
	msg, _ := ran["error"].(string)
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	last := lines[len(lines)-1]
	for _, prefix := range []string{"Error in ", "in "} {
		for strings.HasPrefix(last, prefix) {
			_, rest, found := strings.Cut(last, ": ")
			if !found {
				break
			}
			last = rest
		}
	}
	last = strings.TrimPrefix(last, "Error: ")
	if len(last) > 40 {
		last = last[:40]
	}
	return last
}

// jsonLiteral is v as JSON, quoted as a string literal.
func jsonLiteral(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return strconv.Quote("null")
	}
	return strconv.Quote(string(b))
}

// listItem is one value of a list parameter's element type.
func listItem(items any) any {
	switch items {
	case "int":
		return 1
	case "float":
		return 1.5
	case "date":
		return "2026-09-20"
	}
	return "acceptance"
}

// seedPreGate saves a script whose source no draft can record (#1939) -- one
// that exists to fail, reacts to state and events a draft cannot set up, or
// is drawn and never run -- for a criterion that is not about saving it. A
// stand-in source is saved through the tool with the save's other fields,
// and the fixture's source is then set on the script and its version, as a
// script saved before tests were required carries it. It returns the create's
// answer.
func (c *client) seedPreGate(t *testing.T, create map[string]any) map[string]any {
	t.Helper()
	standIn := maps.Clone(create)
	standIn["source"] = "def main():\n    \"\"\"Stands in for the fixture.\"\"\"\n    print(\"stand-in\")\n"
	out := c.saveScript(standIn, nil)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("seedPreGate: the stand-in was not saved: %v", out)
	}
	db := issue1904DB(t)
	issue1904Exec(t, db, `UPDATE scripts SET source_code = $2 WHERE id = $1`, id, create["source"])
	issue1904Exec(t, db, `UPDATE script_versions SET source_code = $2 WHERE script_id = $1`, id, create["source"])
	return out
}
