//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2838: while search ranks by words alone, a natural-language
// intent finds what holds some of its words, and the response says
// the ranking is lexical and what that means.
//
// Wire forms: search is called through the MCP client with `intent` and
// `purpose` strings and `sources` absent, and again with `sources` an array of
// one string (the only form its schema admits for it); both must answer the
// same way. The files are uploaded with POST /api/v1/resources (multipart).
//
// The plain dev stack runs with no embedder, so every search there ranks
// lexically: the path an agent's sentence took while a busy embedder made
// search fall back. Each run files two resources of its own, named by a word
// no other file holds, and asks for that word beside one only the first file
// holds and one no file holds: a rule requiring every word matches neither.

const issue2838Purpose = "Acceptance #2838: find the files for a question while search ranks by words."

// issue2838Seed files the run's two resources and returns the run's word and
// the ids of the file holding two words of the intent and the one holding one.
func issue2838Seed(t *testing.T, c *client) (word, both, one string) {
	t.Helper()
	word = "acc2838x" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	both = issue2836Upload(t, c, "global", "", "acceptance-2838", word+"-outbound.md", []byte("# "+word+" outbound orders\n\nOutbound orders handed to a carrier.\n"))
	one = issue2836Upload(t, c, "global", "", "acceptance-2838", word+"-tally.md", []byte("# "+word+" tally\n\nA tally per warehouse per day.\n"))
	t.Cleanup(func() {
		for _, id := range []string{both, one} {
			c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		}
	})
	return word, both, one
}

// issue2838Resources are the ids of a search's resources hits, in rank order.
func issue2838Resources(out map[string]any) []string {
	var ids []string
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		if gm["source"] != "resources" {
			continue
		}
		hits, _ := gm["hits"].([]any)
		for _, h := range hits {
			hm, _ := h.(map[string]any)
			ids = append(ids, fmt.Sprint(hm["ref"]))
		}
	}
	return ids
}

// Criterion 1: an intent no file holds every word of finds the files holding
// some of them, the one holding more first, in either wire form.
func TestIssue2838_ALexicalSearchFindsTheFilesHoldingAnyWordOfTheIntent(t *testing.T) {
	c := connect(t)
	word, both, one := issue2838Seed(t, c)
	intent := word + " outbound zyxwquartz"
	// Across every source the display shows each source's best hit (the
	// balanced set); narrowed to resources it shows both files.
	for name, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"no sources":        {map[string]any{"intent": intent, "purpose": issue2838Purpose}, []string{both}},
		"sources as a list": {map[string]any{"intent": intent, "sources": []string{"resources"}, "purpose": issue2838Purpose}, []string{both, one}},
	} {
		var ids []string
		var out map[string]any
		waitFor(t, name+": the run's files in a lexical search", func() (bool, string) {
			out = c.call("search", tc.args)
			ids = issue2838Resources(out)
			return len(ids) >= len(tc.want) && ids[0] == both, fmt.Sprint(ids)
		})
		if out["ranking"] != "lexical" {
			t.Fatalf("%s: ranking %v, want lexical on a stack with no embedder", name, out["ranking"])
		}
		if fmt.Sprint(ids[:len(tc.want)]) != fmt.Sprint(tc.want) {
			t.Fatalf("%s: resources hits %v, want %v first (%s holds two words of the intent, %s one)", name, ids, tc.want, both, one)
		}
	}
}

// Criterion 2: a lexical answer says it is lexical, why, and that each result
// holds at least one word of the intent, so a thin answer is not read as
// "nothing exists"; an entity search carries no such note.
func TestIssue2838_ALexicalAnswerSaysWhatItMatched(t *testing.T) {
	c := connect(t)
	out := c.call("search", map[string]any{"intent": "supply chain orders zyxwquartz", "sources": []string{"resources"}, "purpose": issue2838Purpose})
	note, _ := out["ranking_note"].(string)
	for _, want := range []string{"lexical", "no embedding provider is configured", "at least one of the intent's 4 words"} {
		if !strings.Contains(note, want) {
			t.Fatalf("ranking_note %q lacks %q", note, want)
		}
	}
	entity := c.call("search", map[string]any{
		"entity_urns": []string{"mcp:dataset:(trino,acme-trino,iceberg.inventory.supply_chain_orders)"}, "purpose": issue2838Purpose,
	})
	if _, ok := entity["ranking_note"]; ok || entity["ranking"] != "entity" {
		t.Fatalf("an entity search carries ranking %v and note %v; want entity and no note", entity["ranking"], entity["ranking_note"])
	}
}
