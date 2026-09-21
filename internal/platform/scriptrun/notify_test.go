package scriptrun

import (
	"context"
	"strings"
	"testing"
)

// notifyCaller answers a notify call the way the tool does, and records what
// it was asked.
type notifyCaller struct {
	recordingCaller
	result map[string]any
}

func (c *notifyCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, recordedCall{name: name, args: args})
	if c.err != nil {
		return nil, c.err
	}
	if c.result != nil {
		return c.result, nil
	}
	return map[string]any{
		"channel": args["channel"], "queued": float64(1),
		"delivered": false, "detail": "accepted for delivery",
	}, nil
}

// ranWith executes source against caller, with the run's own page configured.
func ranWith(t *testing.T, source string, caller Caller) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "monitor", RunID: "run_1", FireTime: fireTime,
		Caller: caller, RunURL: "https://portal.example.com/portal/scripts/s1/runs/run_1",
	})
}

// onlyNotifyCall returns the single notify call the run made.
func onlyNotifyCall(t *testing.T, caller *notifyCaller) recordedCall {
	t.Helper()
	if len(caller.calls) != 1 {
		t.Fatalf("made %d calls, want 1: %+v", len(caller.calls), caller.calls)
	}
	if caller.calls[0].name != toolNotify {
		t.Fatalf("called %q, want %q", caller.calls[0].name, toolNotify)
	}
	return caller.calls[0]
}

func TestPlatformNotify_IssuesASendThroughTheTool(t *testing.T) {
	caller := &notifyCaller{}
	_, err := ranWith(t, `
platform.notify(channel="ops", title="Threshold crossed", body="queue depth 812")
`, caller)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	call := onlyNotifyCall(t, caller)
	if call.args["action"] != notifyActionSend {
		t.Errorf("action = %v, want send", call.args["action"])
	}
	if call.args["channel"] != "ops" || call.args["title"] != "Threshold crossed" {
		t.Errorf("args = %+v", call.args)
	}
	// The link defaults to the run's own page: a monitor that posts a number
	// without saying where it came from is the thing people mute.
	if call.args["link"] != "https://portal.example.com/portal/scripts/s1/runs/run_1" {
		t.Errorf("link = %v, want the run's page", call.args["link"])
	}
}

func TestPlatformNotify_AScriptsOwnLinkWins(t *testing.T) {
	caller := &notifyCaller{}
	if _, err := ranWith(t, `
platform.notify(channel="ops", title="t", body="b", link="https://example.com/dashboard")
`, caller); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := onlyNotifyCall(t, caller).args["link"]; got != "https://example.com/dashboard" {
		t.Errorf("link = %v, want the script's own", got)
	}
}

func TestPlatformNotify_ReturnsTheToolsAnswerToTheScript(t *testing.T) {
	caller := &notifyCaller{result: map[string]any{"queued": float64(3), "detail": "ok"}}
	res, err := ranWith(t, `
out = platform.notify(channel="ops", title="t")
print("queued: %d" % out["queued"])
`, caller)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Log, "queued: 3") {
		t.Errorf("the tool's answer did not reach the script; log = %q", res.Log)
	}
}

func TestPlatformNotify_RecordsThePostInTheRunLog(t *testing.T) {
	// A scheduled run's history has to say where its message landed whether
	// or not the script printed the result it was handed.
	caller := &notifyCaller{}
	res, err := ranWith(t, `platform.notify(channel="ops", title="t")`, caller)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Log, "notify: send to ops") {
		t.Errorf("the run log does not record the post; log = %q", res.Log)
	}
}

func TestPlatformPublish_IssuesAPublishNamingTheAsset(t *testing.T) {
	caller := &notifyCaller{}
	if _, err := ranWith(t, `
platform.publish(channel="ops", name="Weekly report", message="This week")
`, caller); err != nil {
		t.Fatalf("run: %v", err)
	}
	call := onlyNotifyCall(t, caller)
	if call.args["action"] != notifyActionPublish {
		t.Errorf("action = %v, want publish", call.args["action"])
	}
	// The asset is named the way platform.export named it, so a script says
	// the name twice rather than carrying an id between the calls.
	if call.args["asset"] != "Weekly report" {
		t.Errorf("asset = %v, want the export's name", call.args["asset"])
	}
	if call.args["message"] != "This week" {
		t.Errorf("message = %v", call.args["message"])
	}
}

func TestPlatformNotify_RefusesTheIncompleteCall(t *testing.T) {
	tests := []struct {
		name   string
		source string
		wants  string
	}{
		{"no channel", `platform.notify(channel="", title="t")`, "channel is empty"},
		{"no title", `platform.notify(channel="ops", title="")`, "title is empty"},
		{"publish with no channel", `platform.publish(channel="", name="r")`, "channel is empty"},
		{"publish with no name", `platform.publish(channel="ops", name="")`, "name is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caller := &notifyCaller{}
			_, err := ranWith(t, tc.source, caller)
			if err == nil {
				t.Fatal("the call was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
			if len(caller.calls) != 0 {
				t.Error("a refused call still reached the tool")
			}
		})
	}
}

func TestPlatformNotify_RefusedInADraftWithoutAllowWrites(t *testing.T) {
	// A message in somebody else's chat client is not something a dry run
	// may leave behind.
	for _, source := range []string{
		`platform.notify(channel="ops", title="t")`,
		`platform.publish(channel="ops", name="r")`,
	} {
		caller := &notifyCaller{}
		_, err := Run(context.Background(), Options{
			Source: source, Name: "monitor", RunID: "run_1", FireTime: fireTime,
			Caller: caller, Writes: WritesRefused,
		})
		if err == nil {
			t.Fatalf("%s: a draft posted to a channel", source)
		}
		if len(caller.calls) != 0 {
			t.Errorf("%s: a refused draft call still reached the tool", source)
		}
	}
}

func TestPlatformNotify_IsUnavailableWithoutACaller(t *testing.T) {
	// A syntax-only execution predeclares the platform module but can call
	// nothing on it.
	_, err := Run(context.Background(), Options{
		Source: `platform.notify(channel="ops", title="t")`, Name: "monitor",
		RunID: "run_1", FireTime: fireTime,
	})
	if err == nil || !strings.Contains(err.Error(), "not available in this context") {
		t.Errorf("err = %v, want the binding to report itself unavailable", err)
	}
}

func TestValidate_ReportsTheTwoBindings(t *testing.T) {
	// Static validation reads the capabilities out of the source, which is
	// how a reader learns what a script reaches without reading the Starlark.
	report := Validate(`
platform.notify(channel="ops", title="t")
platform.publish(channel="ops", name="r")
`)
	found := map[string]bool{}
	for _, c := range report.Capabilities {
		found[c] = true
	}
	if !found[CapabilityNotify] || !found[CapabilityPublish] {
		t.Errorf("capabilities = %v, want both bindings reported", report.Capabilities)
	}
}

func TestValidate_RefusesAMisspelledBinding(t *testing.T) {
	report := Validate(`platform.notifyy(channel="ops", title="t")`)
	if report.OK {
		t.Fatal("a misspelled member was accepted")
	}
	var said strings.Builder
	for _, f := range report.Findings {
		_, _ = said.WriteString(f.Message + " " + f.Hint)
	}
	if !strings.Contains(said.String(), "platform.notify") {
		t.Errorf("the refusal does not offer the real member: %+v", report.Findings)
	}
}
