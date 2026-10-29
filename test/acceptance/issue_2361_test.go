//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/scripttoken"
)

// Acceptance for #2361: a run's recording holds no token platform.mint_token
// issued. A draft mints a token and verifies it, and the recording
// manage_script reads back keeps both calls with the stand-in in place of the
// token.
//
// Wire forms: manage_script run_draft and recording take literal tools/call
// params, run_id a string.

func TestIssue2361_ARecordingHoldsNoMintedToken(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{"admin"})
	drafted := admin.call("manage_script", map[string]any{
		"command": "run_draft", "name": endpointName("acc-2361"), "source": issue2175Handler(secret),
	})
	runID, _ := drafted["recording"].(string)
	if runID == "" {
		t.Fatalf("the draft carried no recording: %v", drafted)
	}
	result, _ := drafted["result"].(map[string]any)
	if result == nil {
		t.Fatalf("the draft did not verify what it minted: %v", drafted)
	}

	rec := admin.call("manage_script", map[string]any{"command": "recording", "run_id": runID})
	calls, _ := rec["calls"].([]any)
	minted, verified := 0, 0
	for _, raw := range calls {
		call, _ := raw.(map[string]any)
		args, _ := call["args"].(map[string]any)
		out, _ := call["out"].(map[string]any)
		switch call["tool"] {
		case scripttoken.CapabilityMint:
			minted++
			if out["token"] != scripttoken.RecordedToken {
				t.Errorf("the recorded mint answers token %v, want %q", out["token"], scripttoken.RecordedToken)
			}
		case scripttoken.CapabilityVerify:
			verified++
			if args["token"] != scripttoken.RecordedToken {
				t.Errorf("the recorded verify was sent token %v, want %q", args["token"], scripttoken.RecordedToken)
			}
		}
	}
	if minted == 0 || verified == 0 {
		raw, _ := json.Marshal(calls)
		t.Fatalf("the recording holds %d mints and %d verifies, want both: %s", minted, verified, raw)
	}
	raw, _ := json.Marshal(rec)
	if strings.Count(string(raw), "eyJ") > 0 {
		t.Errorf("the recording holds something shaped like a token: %s", raw)
	}
}
