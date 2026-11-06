package notifydelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/feedbackapi"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/threadreach"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// The assembled path of #2512 and #2514: the real feedback routes, the real
// access decision (threadreach over internal/portal/access), the real mention
// resolver, the real notifier and enqueuer. Only the stores are in memory.
// Each test is one acceptance sentence of the tickets.

const (
	itOwner   = "owner@acme.io"
	itPersona = "persona-b@acme.io" // reaches the asset only through a persona share
	itReplier = "replier-c@acme.io" // reaches it through the same persona share
	itOutside = "outside-d@acme.io" // reaches nothing
)

// memThreads is an in-memory thread store with the message edit/delete half.
type memThreads struct {
	threads.ThreadStore
	mu      sync.Mutex
	thread  map[string]*threads.Thread
	events  []threads.ThreadEvent
	deleted map[string]bool
}

func newMemThreads() *memThreads {
	return &memThreads{thread: map[string]*threads.Thread{}, deleted: map[string]bool{}}
}

func (m *memThreads) CreateThread(_ context.Context, t threads.Thread, first threads.ThreadEvent) (*threads.Thread, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.thread[t.ID] = &t
	m.events = append(m.events, first)
	return &t, nil
}

func (m *memThreads) GetThread(_ context.Context, id string) (*threads.Thread, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.thread[id]
	if !ok {
		return nil, threads.ErrEventNotFound
	}
	clone := *t
	return &clone, nil
}

func (m *memThreads) AppendEvent(_ context.Context, e threads.ThreadEvent) (*threads.ThreadEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return &e, nil
}

func (m *memThreads) ListEvents(_ context.Context, threadID string) ([]threads.ThreadEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []threads.ThreadEvent{}
	for _, e := range m.events {
		if e.ThreadID != threadID {
			continue
		}
		if m.deleted[e.ID] {
			now := time.Now()
			e.Body, e.Metadata, e.DeletedAt = "", nil, &now
		}
		out = append(out, e)
	}
	return out, nil
}

func (m *memThreads) GetEvent(ctx context.Context, threadID, eventID string) (*threads.ThreadEvent, error) {
	events, _ := m.ListEvents(ctx, threadID)
	for i := range events {
		if events[i].ID == eventID {
			return &events[i], nil
		}
	}
	return nil, threads.ErrEventNotFound
}

func (m *memThreads) EditEvent(_ context.Context, e threads.EventEdit) (*threads.ThreadEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.events {
		if m.events[i].ID == e.EventID {
			now := time.Now()
			m.events[i].Body, m.events[i].Metadata, m.events[i].EditedAt = e.Body, e.Metadata, &now
			out := m.events[i]
			return &out, nil
		}
	}
	return nil, threads.ErrEventNotFound
}

func (m *memThreads) DeleteEvent(_ context.Context, _, eventID, _ string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted[eventID] = true
	return false, nil
}

// Participants mirrors the SQL: authors of live messages and the people they
// mention.
func (m *memThreads) Participants(_ context.Context, threadID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	add := func(a string) {
		a = strings.ToLower(a)
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, e := range m.events {
		if e.ThreadID != threadID || m.deleted[e.ID] {
			continue
		}
		add(e.AuthorEmail)
		for _, who := range mention.FromMetadata(e.Metadata) {
			add(who)
		}
	}
	return out, nil
}

// personaShares grants the asset to the "analyst" persona only.
type personaShares struct {
	portaldomain.ShareStore
}

func (personaShares) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	return []portaldomain.Share{{
		ID: "s1", AssetID: "a1", SharedWithPersona: "analyst", Permission: portaldomain.PermissionViewer,
	}}, nil
}

func (personaShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

type itDirectory map[string][]string

func (d itDirectory) Get(_ context.Context, email string) (*user.User, error) {
	roles, ok := d[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &user.User{Email: email, Roles: roles}, nil
}

func personaOf(roles []string) string {
	if slices.Contains(roles, "analyst") {
		return "analyst"
	}
	return ""
}

type itRetractQueue struct {
	*recordQueue
	retracted []string
}

func (q *itRetractQueue) RetractEvent(_ context.Context, id string) error {
	q.retracted = append(q.retracted, id)
	return nil
}

type assembled struct {
	mux   *http.ServeMux
	queue *itRetractQueue
	store *memThreads
}

func assemble(t *testing.T) *assembled {
	t.Helper()
	assets := &fakeAssets{asset: &portaldomain.Asset{ID: "a1", Name: "Report", OwnerID: "owner-sub", OwnerEmail: itOwner}}
	shares := personaShares{}
	checker := access.New(access.Config{Assets: assets, Shares: shares, AdminRoles: []string{"admin"}})
	reach := threadreach.New(threadreach.Config{
		Access: checker, Assets: assets, PersonaName: personaOf,
		Directory: itDirectory{itOwner: {}, itPersona: {"analyst"}, itReplier: {"analyst"}, itOutside: {"other"}},
	})
	queue := &itRetractQueue{recordQueue: &recordQueue{}}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	store := newMemThreads()
	notifier := NewPortalNotifier(enq, PortalStores{
		Assets: assets, Participants: store, Reach: reach,
	}, "https://portal.acme.io")
	notifier.retract = queue

	mux := http.NewServeMux()
	feedbackapi.New(feedbackapi.Config{
		Threads: store, Assets: assets, Shares: shares, Access: checker, Reach: reach,
		Mentions: mention.NewService(reach), Notifier: notifier, PersonaName: personaOf,
	}).Register(mux)
	return &assembled{mux: mux, queue: queue, store: store}
}

// as sends a request as a signed-in person, carrying the persona their roles
// resolve to, as the portal's gate does.
func (a *assembled) as(t *testing.T, who, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string][]string{itPersona: {"analyst"}, itReplier: {"analyst"}}[who]
	u := &access.User{UserID: who + "-sub", Email: who, Roles: roles}
	ctx := portaldomain.WithCallerPersona(access.ContextWithUser(context.Background(), u), personaOf(roles))
	req := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec
}

func (a *assembled) openThread(t *testing.T, who, body string) string {
	t.Helper()
	rec := a.as(t, who, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": "correction", "target_type": "asset", "asset_id": "a1", "body": body,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("open thread as %s: %d %s", who, rec.Code, rec.Body.String())
	}
	var th threads.Thread
	_ = json.Unmarshal(rec.Body.Bytes(), &th)
	return th.ID
}

func (a *assembled) reply(t *testing.T, who, threadID, body string) threads.ThreadEvent {
	t.Helper()
	rec := a.as(t, who, http.MethodPost, "/api/v1/portal/threads/"+threadID+"/events", map[string]any{"body": body})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply as %s: %d %s", who, rec.Code, rec.Body.String())
	}
	var e threads.ThreadEvent
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e
}

func rowsFor(rows []notification.Notification, recipient, category string) []notification.Notification {
	var out []notification.Notification
	for _, r := range rows {
		if r.Recipient == recipient && (category == "" || r.Category == category) {
			out = append(out, r)
		}
	}
	return out
}

// B opens A only through a persona share; the owner mentions B: one mention
// notification is queued for B and the mention is recorded (renders as a chip).
func TestAssembled_MentionReachesAPersonaShareRecipient(t *testing.T) {
	a := assemble(t)
	id := a.openThread(t, itOwner, "Draft is up")
	e := a.reply(t, itOwner, id, "@persona-b(acme.io) please check the totals")

	if got := mention.FromMetadata(e.Metadata); len(got) != 1 || got[0] != itPersona {
		t.Fatalf("mention recorded on the event = %v; want B", got)
	}
	if got := rowsFor(a.queue.snapshot(), itPersona, notification.CategoryMention); len(got) != 1 {
		t.Fatalf("mention rows for B = %d; want 1", len(got))
	}
}

// B posts feedback on A; the owner replies: B is notified. This is the case
// #2512 reports; the assembled path queues the row for the thread's author.
func TestAssembled_OwnerReplyReachesTheThreadAuthor(t *testing.T) {
	a := assemble(t)
	id := a.openThread(t, itPersona, "The Q4 total looks off")
	reply := a.reply(t, itOwner, id, "Good catch, fixed")

	got := rowsFor(a.queue.snapshot(), itPersona, notification.CategoryComment)
	if len(got) != 1 {
		t.Fatalf("rows for the thread author after the owner's reply = %d; want 1", len(got))
	}
	if got[0].Payload.EventID != reply.ID || got[0].Payload.Message != "Good catch, fixed" {
		t.Errorf("row = %+v; want it to name the reply", got[0].Payload)
	}
}

// C replied earlier and holds no email share; a later reply notifies C.
func TestAssembled_EarlierReplierIsNotified(t *testing.T) {
	a := assemble(t)
	id := a.openThread(t, itOwner, "Draft is up")
	a.reply(t, itReplier, id, "Looks right to me")
	before := len(rowsFor(a.queue.snapshot(), itReplier, ""))
	a.reply(t, itOwner, id, "Thanks, publishing")
	if got := len(rowsFor(a.queue.snapshot(), itReplier, notification.CategoryComment)) - before; got != 1 {
		t.Fatalf("rows for the earlier replier = %d; want 1", got)
	}
}

// D, mentioned but unable to open A, gets nothing and the mention stays plain
// text. The author of a message is never notified of it.
func TestAssembled_OutsiderAndAuthorGetNothing(t *testing.T) {
	a := assemble(t)
	id := a.openThread(t, itOwner, "Draft is up")
	e := a.reply(t, itOwner, id, "@outside-d(acme.io) have a look")
	if got := mention.FromMetadata(e.Metadata); len(got) != 0 {
		t.Errorf("a person who cannot open the target is not recorded as mentioned: %v", got)
	}
	rows := a.queue.snapshot()
	if got := rowsFor(rows, itOutside, ""); len(got) != 0 {
		t.Errorf("rows for D = %+v; want none", got)
	}
	if got := rowsFor(rows, itOwner, ""); len(got) != 0 {
		t.Errorf("rows for the author of every message = %+v; want none", got)
	}
}

// Editing a reply to add @B queues one mention for B; saving it again
// unchanged queues none. Deleting it takes its text out of the queue and out
// of the thread's events.
func TestAssembled_EditAndDelete(t *testing.T) {
	a := assemble(t)
	id := a.openThread(t, itOwner, "Draft is up")
	e := a.reply(t, itOwner, id, "please check the totals")
	path := "/api/v1/portal/threads/" + id + "/events/" + e.ID

	for range 2 {
		if rec := a.as(t, itOwner, http.MethodPatch, path, map[string]any{"body": "@persona-b(acme.io) please check the totals"}); rec.Code != http.StatusOK {
			t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
		}
	}
	if got := rowsFor(a.queue.snapshot(), itPersona, notification.CategoryMention); len(got) != 1 {
		t.Fatalf("mention rows for B after two identical edits = %d; want 1", len(got))
	}
	if rec := a.as(t, itPersona, http.MethodPatch, path, map[string]any{"body": "hijack"}); rec.Code != http.StatusForbidden {
		t.Errorf("another participant's edit = %d; want 403", rec.Code)
	}
	if rec := a.as(t, itPersona, http.MethodDelete, path, nil); rec.Code != http.StatusForbidden {
		t.Errorf("another participant's delete = %d; want 403", rec.Code)
	}
	if rec := a.as(t, itOwner, http.MethodDelete, path, nil); rec.Code != http.StatusOK {
		t.Fatalf("author's delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(a.queue.retracted) != 1 || a.queue.retracted[0] != e.ID {
		t.Errorf("retracted = %v; want the deleted message", a.queue.retracted)
	}
	rec := a.as(t, itPersona, http.MethodGet, "/api/v1/portal/threads/"+id+"/events", nil)
	if strings.Contains(rec.Body.String(), "please check the totals") {
		t.Errorf("the thread's events still carry the deleted text: %s", rec.Body.String())
	}
}
