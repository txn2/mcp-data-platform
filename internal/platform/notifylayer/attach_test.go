package notifylayer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// memResources serves managed resources by id.
type memResources map[string]*resource.Resource

func (m memResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := m[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

// opsChannel is an immediate chat channel the analyst persona reaches.
func opsChannel() notification.Channel {
	return notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "chat", Target: "T",
	}
}

// attachHandle is a tool that can read one asset and two resources, one of
// which lives in a library the analyst is not in.
func attachHandle(t *testing.T, access Entitlement) (*Handle, *memQueue) {
	t.Helper()
	return testHandle(t, []notification.Channel{opsChannel()}, []string{"chat"}, func(c *Config) {
		c.Assets = &memAssets{asset: &portaldomain.Asset{ID: "a1", Name: "Weekly revenue", ContentType: "text/markdown", CurrentVersion: 4}}
		c.Access = access
		c.Resources = memResources{
			"r1":     {ID: "r1", Filename: "raw.csv", Scope: resource.ScopeGlobal},
			"secret": {ID: "secret", Filename: "s.csv", Scope: resource.ScopePersona, ScopeID: "finance"},
		}
	})
}

func TestParseAttachments_BothWireForms(t *testing.T) {
	got, err := parseAttachments([]any{
		"a1",
		"https://portal.example.com/portal/assets/a1?tab=content",
		"mcp:resource:r1",
		map[string]any{"asset_id": "a1", "name": " chart.md "},
		map[string]any{"resource_id": "mcp:resource:r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []attachmentInput{
		{AssetID: "a1", ResourceID: "a1"},
		{AssetID: "a1", ResourceID: "a1"},
		{ResourceID: "r1"},
		{AssetID: "a1", Name: "chart.md"},
		{ResourceID: "r1"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseAttachments_Refusals(t *testing.T) {
	six := []any{"a", "b", "c", "d", "e", "f"}
	cases := map[string][]any{
		"empty string": {"  "},
		"both ids":     {map[string]any{"asset_id": "a", "resource_id": "r"}},
		"neither id":   {map[string]any{"name": "x"}},
		"a number":     {42.0},
		"too many":     six,
	}
	for name, raw := range cases {
		if _, err := parseAttachments(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestResolveAttachments_PinsTheVersionAndTheLink(t *testing.T) {
	h, _ := attachHandle(t, allowAll{})
	refs, _ := parseAttachments([]any{"a1", "r1"})
	got, err := h.resolveAttachments(asAnalyst(t), refs, h.resolveCaller(asAnalyst(t)))
	if err != nil {
		t.Fatal(err)
	}
	want := []notification.Attachment{
		{AssetID: "a1", Version: 4, Name: "Weekly revenue.md", Link: "https://portal.example.com/portal/assets/a1"},
		{ResourceID: "r1", Name: "raw.csv", Link: "https://portal.example.com/portal/resources/r1"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attachment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestResolveAttachments_WhatTheCallerCannotReadIsNotFound(t *testing.T) {
	h, _ := attachHandle(t, denyAll{})
	for _, ref := range []any{"a1", "secret", "nope", map[string]any{"asset_id": "a1"}} {
		refs, err := parseAttachments([]any{ref})
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.resolveAttachments(asAnalyst(t), refs, h.resolveCaller(asAnalyst(t)))
		if err == nil || !strings.Contains(err.Error(), "is available to this session") {
			t.Errorf("%v: err = %v, want the not-found refusal", ref, err)
		}
	}
}

func TestAttach_UnavailableWithoutStores(t *testing.T) {
	h, _ := testHandle(t, []notification.Channel{opsChannel()}, []string{"chat"})
	doc := notification.Document{}
	if err := h.attach(asAnalyst(t), &doc, []any{"a1"}, caller{}); !errors.Is(err, errNoAttachments) {
		t.Errorf("err = %v, want errNoAttachments", err)
	}
	if err := h.attach(asAnalyst(t), &doc, nil, caller{}); err != nil || doc.Attachments != nil {
		t.Errorf("no attachments: err %v, doc %+v", err, doc.Attachments)
	}
}

// callNotify sends one tools/call with literal JSON arguments through a real
// MCP client and server, so the SDK's schema validation sees each wire form as
// a client sends it.
func callNotify(t *testing.T, h *Handle, args string) *mcp.CallToolResult {
	t.Helper()
	ctx := asAnalyst(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	h.RegisterTool(server)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolName, Arguments: raw})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestNotifySend_AttachmentsAsStringsAndAsObjectsQueueTheSameDocument(t *testing.T) {
	// The schema admits a list of strings and a list of objects; a client
	// sending either must get the same document queued.
	docs := make([]notification.Document, 0, 2)
	for _, args := range []string{
		`{"action":"send","channel":"ops","title":"Weekly","attachments":["a1","mcp:resource:r1"]}`,
		`{"action":"send","channel":"ops","title":"Weekly","attachments":[{"asset_id":"a1"},{"resource_id":"r1"}]}`,
	} {
		h, queue := attachHandle(t, allowAll{})
		res := callNotify(t, h, args)
		if res.IsError {
			t.Fatalf("%s: %s", args, resultText(t, res))
		}
		if len(queue.rows) != 1 {
			t.Fatalf("%s: queued %d rows", args, len(queue.rows))
		}
		docs = append(docs, *queue.rows[0].Payload.Document)
	}
	a, b := docs[0].Attachments, docs[1].Attachments
	if len(a) != 2 || len(b) != 2 || a[0] != b[0] || a[1] != b[1] {
		t.Errorf("string form %+v, object form %+v; want the same attachments", a, b)
	}
}

func TestNotifySend_KeyAndRepeatAfterReachTheGate(t *testing.T) {
	h, queue := attachHandle(t, allowAll{})
	res := callNotify(t, h, `{"action":"send","channel":"ops","title":"Feed stale","key":"feed:orders","repeat_after":"6h"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if len(queue.gates) != 1 || queue.gates[0].Key != "feed:orders" || queue.gates[0].Window != 6*time.Hour {
		t.Errorf("gate = %+v, want the key with a six-hour window", queue.gates)
	}
}

func TestNotifySend_ASuppressedSendSaysSo(t *testing.T) {
	h, queue := attachHandle(t, allowAll{})
	queue.suppress = true
	out := resultJSON(t, callNotify(t, h, `{"action":"send","channel":"ops","title":"Feed stale","key":"k"}`))
	detail, _ := out["detail"].(string)
	if suppressed, _ := out["suppressed"].(bool); !suppressed || out["queued"] != 0.0 || !strings.Contains(detail, "repeat window") {
		t.Errorf("result = %v, want a suppressed send explained", out)
	}
}

func TestNotifySend_RefusesABadRepeatAfter(t *testing.T) {
	h, queue := attachHandle(t, allowAll{})
	for _, v := range []string{"soon", "-1h", "200h"} {
		res := callNotify(t, h, `{"action":"send","channel":"ops","title":"t","key":"k","repeat_after":"`+v+`"}`)
		if !res.IsError || !strings.Contains(resultText(t, res), "repeat_after") {
			t.Errorf("repeat_after %q: %s", v, resultText(t, res))
		}
	}
	if len(queue.rows) != 0 {
		t.Error("a send with a bad repeat_after was queued")
	}
}

func TestNotifySend_ARefusedAttachmentQueuesNothing(t *testing.T) {
	h, queue := attachHandle(t, allowAll{})
	res := callNotify(t, h, `{"action":"send","channel":"ops","title":"t","attachments":["secret"]}`)
	if !res.IsError || len(queue.rows) != 0 {
		t.Errorf("an attachment the caller cannot read was sent: %s", resultText(t, res))
	}
}

func TestNotifyPublish_ACSVAssetIsATableWithAPreview(t *testing.T) {
	csv := "region,revenue\nWest,1|204\nEast,988\n"
	h, queue := testHandle(t, []notification.Channel{opsChannel()}, []string{"chat"}, func(c *Config) {
		c.Assets = &memAssets{
			asset: &portaldomain.Asset{
				ID: "a9", Name: "Revenue", ContentType: "text/csv; charset=utf-8",
				CurrentVersion: 2, ThumbnailS3Key: "assets/a9/.thumbnail.png",
			},
			content: []byte(csv),
		}
		c.Access = allowAll{}
	})
	res := callNotify(t, h, `{"action":"publish","channel":"ops","asset":"a9","key":"weekly"}`)
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	doc := queue.rows[0].Payload.Document
	want := "| region | revenue |\n| --- | --- |\n| West | 1\\|204 |\n| East | 988 |"
	if doc.Body != want {
		t.Errorf("body = %q, want %q", doc.Body, want)
	}
	if len(doc.Attachments) != 1 || !doc.Attachments[0].Preview || doc.Attachments[0].Version != 2 || doc.Attachments[0].Name != "Revenue.png" {
		t.Errorf("attachments = %+v, want the preview tile at version 2", doc.Attachments)
	}
	if doc.Key != "weekly" {
		t.Errorf("key = %q", doc.Key)
	}
}

func TestCSVTable(t *testing.T) {
	var b strings.Builder
	_, _ = b.WriteString("n\n")
	for i := range csvTableRows + 5 {
		_, _ = b.WriteString(strings.Repeat("x", i%3+1) + "\n")
	}
	out := csvTable([]byte(b.String()))
	if !strings.HasSuffix(out, "\n5 more rows in the asset.\n") {
		t.Errorf("table does not say how many rows it left out: %q", out[len(out)-60:])
	}
	if got := strings.Count(out, "\n| "); got != csvTableRows+1 {
		t.Errorf("table has %d data and separator rows, want %d", got, csvTableRows+1)
	}
	if bad := csvTable([]byte("a,\"b\n")); bad != "a,\"b\n" {
		t.Errorf("an unparseable file = %q, want it as written", bad)
	}
}

func TestPreviewOf(t *testing.T) {
	if got := previewOf(&portaldomain.Asset{ID: "a"}); got != nil {
		t.Errorf("an asset with no tile got a preview: %+v", got)
	}
	if got := previewName(" "); got != "preview.png" {
		t.Errorf("previewName(blank) = %q", got)
	}
}

func TestFileNameOf(t *testing.T) {
	cases := []struct {
		asset portaldomain.Asset
		want  string
	}{
		{portaldomain.Asset{Name: "Report", ContentType: "text/html"}, "Report.html"},
		{portaldomain.Asset{Name: "data.json", ContentType: "application/json"}, "data.json"},
		{portaldomain.Asset{ID: "a7", ContentType: "application/x-unknown"}, "a7"},
	}
	for _, tc := range cases {
		if got := fileNameOf(&tc.asset); got != tc.want {
			t.Errorf("fileNameOf(%+v) = %q, want %q", tc.asset, got, tc.want)
		}
	}
	for ct, ext := range map[string]string{
		"text/markdown": ".md", "text/csv": ".csv", "image/png": ".png", "image/jpeg": ".jpg",
		"image/svg+xml": ".svg", "application/pdf": ".pdf", "text/plain": ".txt",
	} {
		if got := extensionFor(ct); got != ext {
			t.Errorf("extensionFor(%s) = %q", ct, got)
		}
	}
}
