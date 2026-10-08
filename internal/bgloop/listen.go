package bgloop

import (
	"context"

	"github.com/lib/pq"
)

// Listen reports one LISTEN connection's state on the pg_listen_* series:
// whether it is connected, how often it reconnected, and how long since it
// last delivered a notification (#1897). Each adapter that opens a
// pq.Listener feeds its event callback and its notifications through one.
//
// A half-open connection is not detected by a ping: pq.Listener.Ping holds
// the listener's lock for the round trip, so a ping on a connection whose
// peer is gone would hold Close until the operating system gave up on the
// socket. The last-notification age is the signal for a connection that
// stays "connected" and delivers nothing. A nil *Listen records nothing.
type Listen struct {
	name string
}

// NewListen returns the state reporter for the LISTEN connection named name
// (a Listen* constant in names.go).
func NewListen(name string) *Listen { return &Listen{name: name} }

// Name is the loop name the connection reports under.
func (l *Listen) Name() string {
	if l == nil {
		return ""
	}
	return l.name
}

// Event records a pq.Listener lifecycle event: connected and reconnected set
// the gauge to 1 (a reconnect also counts one), a drop or a failed attempt
// sets it to 0.
func (l *Listen) Event(ev pq.ListenerEventType) {
	if l == nil {
		return
	}
	m := Metrics()
	ctx := context.Background()
	switch ev {
	case pq.ListenerEventConnected:
		m.RecordListenConnected(ctx, l.name, true, false)
	case pq.ListenerEventReconnected:
		m.RecordListenConnected(ctx, l.name, true, true)
	case pq.ListenerEventDisconnected, pq.ListenerEventConnectionAttemptFailed:
		m.RecordListenConnected(ctx, l.name, false, false)
	}
}

// Notified records a notification delivered on the connection.
func (l *Listen) Notified() {
	if l == nil {
		return
	}
	Metrics().RecordListenNotification(l.name)
}
