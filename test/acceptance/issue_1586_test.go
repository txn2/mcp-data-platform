//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1586: platform_info is the mandatory first call of every session and
// carried the whole prompt library, every prompt with its full body. On the ACME
// demo running 1.128.0 the response was 75,198 characters, 57,307 of them prompt
// bodies; the client refused it and spilled it to a file, so the session's first
// act was a failed tool call and a recovery round trip.
//
// The listing predates show_prompts and manage_prompt's use command. A prompt is
// now resolved from whatever handle the user says, and the human-facing library
// is an app, so orientation has no reason to carry the library at all.
//
// What these hold: platform_info returns no prompt listing and no prompt body;
// the response is a fraction of what was refused; every way a prompt is actually
// reached still works (resolve by handle, browse, open the library, dereference
// a reference); and the agent is still told how to reach one, so removing the
// listing does not leave it guessing.
//
// Wire forms: platform_info is registered through the typed mcp.AddTool form
// over an empty input struct, so its params admit an empty object and an absent
// object and nothing else; both are sent below as literal tools/call params.
// Every other parameter these calls touch is `{"type":"string"}` in its tool's
// schema -- manage_prompt's `command`, `name`, `content`, `description`,
// `display_name` and `scope`, show_prompts' `search`, and fetch's `reference`
// and `purpose` -- so each admits exactly one JSON form and is sent as a JSON
// string. The typed registration validates arguments against those schemas, so
// no second form reaches a handler.

// infoView1586 is the part of a platform_info response these criteria are about,
// with the raw text kept so absence can be asserted on the wire rather than on a
// struct that never declared the field.
type infoView1586 struct {
	raw               string
	AgentInstructions string `json:"agent_instructions"`
}

// readInfo1586 calls platform_info in one of the two forms its schema admits and
// returns the response with its raw text. args nil sends absent params; a
// non-nil empty map sends an empty object.
func readInfo1586(t *testing.T, c *client, args map[string]any) infoView1586 {
	t.Helper()
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: args})
	if err != nil {
		t.Fatalf("platform_info: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("platform_info: tool error: %s", firstText(res))
	}
	text := firstText(res)
	view := infoView1586{raw: text}
	if err := json.Unmarshal([]byte(text), &view); err != nil {
		t.Fatalf("platform_info: result is not a JSON object: %v\n%s", err, text)
	}
	return view
}

// TestIssue1586_NoPromptListing is the change itself: orientation carries no
// prompt library, in either form its schema admits.
func TestIssue1586_NoPromptListing(t *testing.T) {
	c := connect(t)

	// A prompt with a body distinctive enough that its presence anywhere in the
	// response would be unmistakable.
	const body = "Acceptance 1586 body: summarize {topic} and name the source table."
	p := createPrompt1586(t, c, "acceptance-1586-listing", body)

	for _, form := range []struct {
		name string
		args map[string]any
	}{
		{name: "absent params", args: nil},
		{name: "empty object params", args: map[string]any{}},
	} {
		t.Run(form.name, func(t *testing.T) {
			// A fresh session, since the criterion is about what a session is
			// handed at its first call.
			info := readInfo1586(t, connect(t), form.args)

			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(info.raw), &envelope); err != nil {
				t.Fatalf("platform_info: %v", err)
			}
			if _, ok := envelope["prompts"]; ok {
				t.Fatalf("platform_info still carries a prompts field: %s", info.raw)
			}
			if strings.Contains(info.raw, "name the source table") {
				t.Fatalf("platform_info carried a prompt body: %s", info.raw)
			}
			if strings.Contains(info.raw, p.name) {
				t.Fatalf("platform_info named a prompt from the library: %s", info.raw)
			}
			// The response that was refused was 75,198 characters, 57,307 of
			// them the library. What is left is orientation, and it is an order
			// of magnitude smaller.
			if len(info.raw) >= 20_000 {
				t.Fatalf("platform_info returned %d characters; without the library it is a fraction of the 75,198 that was refused", len(info.raw))
			}
		})
	}
}

// TestIssue1586_PromptsStillReachable is the other half: every way a prompt is
// actually reached still works, which is what makes the listing removable.
func TestIssue1586_PromptsStillReachable(t *testing.T) {
	c := connect(t)
	const body = "Acceptance 1586 reachable: reconcile {ledger} against the warehouse."
	p := createPrompt1586(t, c, "acceptance-1586-reachable", body)

	// Resolve by handle, which is how an agent runs a prompt the user names.
	used := c.call("manage_prompt", map[string]any{"command": "use", "name": p.name})
	if !strings.Contains(fmt.Sprint(used), "reconcile {ledger} against the warehouse") {
		t.Fatalf("manage_prompt use did not return the prompt body: %v", used)
	}

	// Browse, which is how an agent reads prompt data for its own reasoning.
	listed := c.call("manage_prompt", map[string]any{"command": "list"})
	if !strings.Contains(fmt.Sprint(listed), p.name) {
		t.Fatalf("manage_prompt list did not carry the prompt: %v", listed)
	}

	// Open the library, which is how the human sees their prompts. The result
	// is a confirmation and carries no prompt data of its own -- that is the
	// reason it can replace the listing without moving the cost.
	shown := c.call("show_prompts", map[string]any{})
	if shown["shown"] != true {
		t.Fatalf("show_prompts did not open the library: %v", shown)
	}
	if strings.Contains(fmt.Sprint(shown), body) {
		t.Fatalf("show_prompts carried a prompt body: %v", shown)
	}

	// Dereference a reference, which is how a prompt named elsewhere is read.
	fetched := c.call("fetch", map[string]any{
		"reference": "mcp:prompt:" + p.id,
		"purpose":   "Confirming a prompt reference still dereferences to the full body.",
	})
	if !strings.Contains(fmt.Sprint(fetched), "reconcile {ledger} against the warehouse") {
		t.Fatalf("fetch on the prompt reference did not return the body: %v", fetched)
	}
}

// TestIssue1586_AgentIsToldHowToReachAPrompt: with no listing in front of it, an
// agent has to be told that named procedures are prompts and which command
// resolves one. The instruction baseline says so, and it is in the same response
// the listing left.
func TestIssue1586_AgentIsToldHowToReachAPrompt(t *testing.T) {
	info := readInfo1586(t, connect(t), nil)
	for _, want := range []string{"Named procedures are prompts", "`manage_prompt`", "use"} {
		if !strings.Contains(info.AgentInstructions, want) {
			t.Fatalf("the agent instructions do not say %q, so an agent with no listing cannot find a prompt: %s", want, info.AgentInstructions)
		}
	}
}

// TestIssue1586_BaselinePageReferencesResolve: the baseline is now an index, so
// every reference it hands an agent has to resolve on the deployment the agent
// is talking to. A slug is a string on both sides -- the shipped page carries
// one, the instruction text names one -- and each side's unit tests assert its
// own literal, so only a live fetch proves they agree and that the page has
// content behind it.
func TestIssue1586_BaselinePageReferencesResolve(t *testing.T) {
	c := connect(t)
	info := readInfo1586(t, c, nil)

	refs := knowledgePageRefs1586(info.AgentInstructions)
	if len(refs) == 0 {
		t.Fatalf("the baseline names no knowledge pages; the index has gone missing: %s", info.AgentInstructions)
	}

	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			out := c.call("fetch", map[string]any{
				"reference": ref,
				"purpose":   "Confirming the instruction baseline's page index resolves.",
			})
			body := fmt.Sprint(out)
			if len(body) < 500 {
				t.Fatalf("%s resolved to %d characters; the baseline points at an empty or missing page: %v", ref, len(body), out)
			}
		})
	}
}

// knowledgePageRefs1586 extracts the `mcp:knowledge_page:<slug>` references an
// instruction text names.
func knowledgePageRefs1586(text string) []string {
	const marker = "mcp:knowledge_page:"
	var refs []string
	for rest := text; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return refs
		}
		rest = rest[i+len(marker):]
		end := strings.IndexAny(rest, "` \n")
		if end < 0 {
			end = len(rest)
		}
		refs = append(refs, marker+rest[:end])
	}
}

// prompt1586 is a prompt the test created, by the two handles it is addressed
// by: the name manage_prompt takes and the id a reference is built from.
type prompt1586 struct {
	name string
	id   string
}

// createPrompt1586 files a global prompt and removes it when the test ends.
// Global rather than personal so it is a prompt orientation would once have
// listed: registerDatabasePrompt tracked global and persona scopes only. The
// suite's default identity is an administrator, which is who may create one.
func createPrompt1586(t *testing.T, c *client, name, content string) prompt1586 {
	t.Helper()
	out := c.call("manage_prompt", map[string]any{
		"command":      "create",
		"name":         name,
		"display_name": "Acceptance 1586 " + name,
		"description":  "Acceptance 1586 fixture for " + name,
		"content":      content,
		"scope":        "global",
	})
	id := promptID1586(out)
	if id == "" {
		t.Fatalf("manage_prompt create returned no prompt id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_prompt", map[string]any{"command": "delete", "name": name})
	})
	return prompt1586{name: name, id: id}
}

// promptID1586 reads the created prompt's id, which manage_prompt returns either
// at the top level or under a prompt object depending on the command.
func promptID1586(out map[string]any) string {
	if id, ok := out["id"].(string); ok && id != "" {
		return id
	}
	if pr, ok := out["prompt"].(map[string]any); ok {
		if id, ok := pr["id"].(string); ok {
			return id
		}
	}
	return ""
}
