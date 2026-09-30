// Package listenbridge carries the platform's list-changed notifications to the
// subscriptions/listen streams of clients on MCP protocol revision 2026-07-28
// (SEP-2575, #1967).
//
// A client on 2026-07-28 opens no per-session GET stream. When it registers a
// list-changed handler it sends one subscriptions/listen request, and the
// response to that request is the stream its notifications arrive on, held by
// the replica that received it. The SDK writes a list-changed notification to
// such a stream only when the replica's own server adds or removes a tool,
// prompt or resource. The platform publishes prompt and managed-resource
// changes, and #1946's once-per-build notice, through pkg/session.Broadcaster,
// which only the GET stream of a client on 2025-11-25 or earlier reads.
//
// A Bridge closes that gap on one replica. Its sending middleware sees the
// notifications/subscriptions/acknowledged the SDK sends when it accepts a
// listen, and records the stream: the SDK session, the subscription id the
// client matches notifications by, the notification types the server agreed
// to, and the platform session the request carried. The record is dropped when
// the listen request ends. Start subscribes the Bridge to the broadcaster, and
// every event it receives is written to each recorded stream that agreed to
// its type and, when the event names a session, belongs to that session.
//
// An unaddressed notifications/tools/list_changed is not written. The only
// publisher of one is the gateway toolkit, after its AddTool or RemoveTools on
// the replica's own server, and every replica applies a gateway connection
// change to its own server (the admin write locally, a peer through the reload
// bus), so the SDK has already told each replica's listen streams. Writing the
// broadcaster's copy as well would send one notification per replica that
// applied the change on top of the SDK's.
//
// Prompts and resources are the other way round: the Bridge's copy is the one
// a listen stream is written, and the SDK's is dropped. A managed resource is
// added to the replica's server where it was created and a peer's through the
// reload, and each also publishes on the broadcaster, which reaches every
// replica, the writer included; writing both would tell the writer's listens
// twice. Nothing adds a prompt or resource to the server at run time without
// publishing one.
//
// A client on 2025-11-25 or earlier opens no listen, so the Bridge records
// nothing for it and its GET stream is served exactly as before.
package listenbridge

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	// methodAcknowledged is the notification the SDK sends on a listen stream
	// once it has accepted the subscription.
	methodAcknowledged = "notifications/subscriptions/acknowledged"
	// methodToolsChanged, methodPromptsChanged and methodResourcesChanged are
	// the list-changed notifications a Bridge carries.
	methodToolsChanged     = "notifications/tools/list_changed"
	methodPromptsChanged   = "notifications/prompts/list_changed"
	methodResourcesChanged = "notifications/resources/list_changed"

	// deliverTimeout bounds one write to a listen stream, matching the bound
	// the SDK puts on its own list-changed writes.
	deliverTimeout = 10 * time.Second

	// noticeWindow is how close together two notices addressed to one session
	// are taken for the same notice: the listen request's own announcement and
	// the one made when its listen is acknowledged race each other. Notices
	// further apart are two builds being announced, as in a rolling deploy.
	noticeWindow = 2 * time.Second

	// subscriberName attributes the Bridge's broadcaster subscription in the
	// broadcaster's logs.
	subscriberName = "subscriptions/listen"
)

// Announcer sends #1946's once-per-build notice to a session whose listen was
// just acknowledged, when the build it last heard about is not the running one.
// *session.AwareHandler implements it; the notice is published through the
// broadcaster addressed to the session, and the Bridge writes it to the listen.
type Announcer interface {
	AnnounceOnListen(ctx context.Context, sessionID string)
}

// Bridge delivers broadcaster list-changed events to the listen streams this
// replica holds. Construct with New; the zero value is not usable. All methods
// are safe for concurrent use.
type Bridge struct {
	broadcaster session.Broadcaster
	announcer   Announcer
	logger      *slog.Logger

	mu      sync.Mutex
	listens map[*listen]struct{}

	// now is the clock the notice window is measured on.
	now func() time.Time
}

// listen is one acknowledged subscriptions/listen stream.
type listen struct {
	// send writes a notification through the sending middleware below the
	// Bridge's, which ends at the SDK's transport write.
	send    mcp.MethodHandler
	session *mcp.ServerSession
	// subscriptionID is the listen request's JSON-RPC id, which the client
	// matches each notification's _meta subscription id against.
	subscriptionID any
	// sessionID is the platform session the listen request carried
	// (Mcp-Session-Id, through session.AwareHandler); empty when it had none.
	sessionID string
	agreed    mcp.NotificationSubscriptions
	// noticedAt is when a notice addressed to the session was last written,
	// in Unix nanoseconds, so the notice published by the listen request and
	// the one published when the listen was acknowledged reach it once.
	noticedAt atomic.Int64
}

// New builds a Bridge over broadcaster. announcer may be nil, which turns off
// the once-per-build notice on a listen. A nil logger falls back to
// slog.Default().
func New(broadcaster session.Broadcaster, announcer Announcer, logger *slog.Logger) *Bridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bridge{
		broadcaster: broadcaster,
		announcer:   announcer,
		logger:      logger,
		listens:     make(map[*listen]struct{}),
		now:         time.Now,
	}
}

// Middleware is the sending middleware that records each acknowledged listen.
// Install it with (*mcp.Server).AddSendingMiddleware. It passes every message
// through unchanged but the SDK's own prompts or resources list_changed to a
// listen stream, which the Bridge writes instead (see the package
// documentation). The Bridge's own writes go to the handler below it and never
// pass through here.
func (b *Bridge) Middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if (method == methodPromptsChanged || method == methodResourcesChanged) && b.listening(req.GetSession()) {
			return nil, nil //nolint:nilnil // a notification has no result, as with the SDK's own sending handler
		}
		res, err := next(ctx, method, req)
		if err == nil && method == methodAcknowledged {
			b.register(ctx, next, req)
		}
		return res, err
	}
}

// register records the listen whose acknowledgment req was. ctx is the listen
// request's own context, so the record is dropped when the listen ends.
func (b *Bridge) register(ctx context.Context, send mcp.MethodHandler, req mcp.Request) {
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok {
		return
	}
	params, ok := req.GetParams().(*mcp.SubscriptionsAcknowledgedParams)
	if !ok || params == nil || !listChanges(params.Notifications) {
		return
	}
	id := params.Meta[mcp.MetaKeySubscriptionID]
	if id == nil {
		return
	}
	l := &listen{
		send:           send,
		session:        ss,
		subscriptionID: id,
		sessionID:      session.AwareSessionID(ctx),
		agreed:         params.Notifications,
	}
	b.mu.Lock()
	b.listens[l] = struct{}{}
	b.mu.Unlock()
	context.AfterFunc(ctx, func() { b.remove(l) })

	if b.announcer != nil && l.sessionID != "" && l.agreed.ToolsListChanged {
		b.announcer.AnnounceOnListen(ctx, l.sessionID)
	}
}

// listening reports whether ss holds a listen the Bridge recorded.
func (b *Bridge) listening(ss mcp.Session) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for l := range b.listens {
		if mcp.Session(l.session) == ss {
			return true
		}
	}
	return false
}

// listChanges reports whether a subscription agreed to any list-changed type.
func listChanges(n mcp.NotificationSubscriptions) bool {
	return n.ToolsListChanged || n.PromptsListChanged || n.ResourcesListChanged
}

// remove drops a listen record.
func (b *Bridge) remove(l *listen) {
	b.mu.Lock()
	delete(b.listens, l)
	b.mu.Unlock()
}

// Wire installs a Bridge on server and delivers b's events to the listens it
// records until ctx ends. announcer may be nil. Nothing is wired without a
// server or a broadcaster.
func Wire(ctx context.Context, server *mcp.Server, b session.Broadcaster, announcer Announcer) {
	if server == nil || b == nil {
		return
	}
	bridge := New(b, announcer, nil)
	server.AddSendingMiddleware(bridge.Middleware)
	bridge.Start(ctx)
}

// Start subscribes the Bridge to its broadcaster and delivers every event it
// receives until ctx ends or the broadcaster closes. The subscription is in
// place when Start returns. The returned channel is closed once delivery has
// stopped.
func (b *Bridge) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	sub := b.broadcaster.Subscribe(ctx, subscriberName)
	go func() {
		defer close(done)
		defer sub.Close()
		for ev := range sub.Events() {
			b.Deliver(ev)
		}
	}()
	return done
}

// Deliver writes ev to every listen stream it is for: the stream agreed to
// ev's type and, when ev names a session, belongs to it. An unaddressed
// tools/list_changed is not written (see the package documentation), and two
// addressed ones within noticeWindow are written once. The streams are written
// at once, so one that stalls holds up none of the others, and Deliver returns
// when every write has finished or timed out.
func (b *Bridge) Deliver(ev session.Event) {
	if !carried(ev) {
		return
	}
	var wg sync.WaitGroup
	for _, l := range b.subscribed(ev) {
		if ev.Method == methodToolsChanged && !b.notice(l) {
			continue
		}
		wg.Go(func() { b.write(l, ev.Method) })
	}
	wg.Wait()
}

// notice reports whether an addressed notice may be written to l now, and
// records that it was.
func (b *Bridge) notice(l *listen) bool {
	now := b.now().UnixNano()
	last := l.noticedAt.Load()
	if last != 0 && now-last < int64(noticeWindow) {
		return false
	}
	return l.noticedAt.CompareAndSwap(last, now)
}

// carried reports whether an event is one a Bridge writes to a listen.
func carried(ev session.Event) bool {
	switch ev.Method {
	case methodPromptsChanged, methodResourcesChanged:
		return true
	case methodToolsChanged:
		return ev.SessionID != ""
	default:
		return false
	}
}

// subscribed returns the listens ev is for.
func (b *Bridge) subscribed(ev session.Event) []*listen {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*listen, 0, len(b.listens))
	for l := range b.listens {
		if ev.SessionID != "" && ev.SessionID != l.sessionID {
			continue
		}
		if agreedTo(l.agreed, ev.Method) {
			out = append(out, l)
		}
	}
	return out
}

// agreedTo reports whether a subscription agreed to method, one of the three
// list-changed methods carried admits.
func agreedTo(n mcp.NotificationSubscriptions, method string) bool {
	switch method {
	case methodToolsChanged:
		return n.ToolsListChanged
	case methodPromptsChanged:
		return n.PromptsListChanged
	default:
		return n.ResourcesListChanged
	}
}

// write sends method on one listen stream, stamped with its subscription id.
// A stream that cannot be written has ended, and its record is dropped.
func (b *Bridge) write(l *listen, method string) {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	if _, err := l.send(ctx, method, notification(l, method)); err != nil {
		b.logger.Debug("listenbridge: writing to a listen stream failed",
			"method", method,
			"session_id", logsan.SanitizeForLog(l.sessionID),
			"error", logsan.SanitizeForLog(err.Error()))
		b.remove(l)
	}
}

// notification builds the list-changed notification method names for l's
// session, its _meta carrying the subscription id.
func notification(l *listen, method string) mcp.Request {
	meta := mcp.Meta{mcp.MetaKeySubscriptionID: l.subscriptionID}
	switch method {
	case methodPromptsChanged:
		return &mcp.ServerRequest[*mcp.PromptListChangedParams]{Session: l.session, Params: &mcp.PromptListChangedParams{Meta: meta}}
	case methodResourcesChanged:
		return &mcp.ServerRequest[*mcp.ResourceListChangedParams]{Session: l.session, Params: &mcp.ResourceListChangedParams{Meta: meta}}
	default:
		return &mcp.ServerRequest[*mcp.ToolListChangedParams]{Session: l.session, Params: &mcp.ToolListChangedParams{Meta: meta}}
	}
}
