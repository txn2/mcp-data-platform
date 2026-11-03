//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The outbox template's migration applies and reverts through manage_schema;
// a captured insert writes a row and notifies the outbox's channel; a drain
// hands the row over and deletes it, and a drain whose write fails leaves it.
func TestOutbox_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, nil)
	ctx := admin()
	schema(t, ctx, tk, schemaInput{
		Command: schemaAdd, Version: 1, Name: "orders",
		Up: "CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)", Down: "DROP TABLE orders",
	}, false)
	added := schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 2, Template: OutboxTemplateName, Capture: []string{"orders"}}, false)
	if got := added["migration"].(map[string]any)["name"]; got != "outbox public.outbox" {
		t.Fatalf("template migration name = %v", got)
	}
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)

	l, err := tk.Listen(context.Background(), "target", []string{OutboxChannel("public", "outbox")})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	execOK(t, tk, Statement{Connection: "target", SQL: "INSERT INTO orders VALUES (7, 12.50)"})
	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := l.Wait(wctx)
	if err != nil {
		t.Fatalf("no notification after a captured insert: %v", err)
	}
	if n.Channel != "public.outbox" {
		t.Fatalf("channel = %q", n.Channel)
	}
	// A wait that times out leaves the connection listening: the listener
	// waits out each poll interval this way.
	short, cancelShort := context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err = l.Wait(short)
	cancelShort()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an idle wait: %v", err)
	}
	execOK(t, tk, Statement{Connection: "target", SQL: "SELECT pg_notify('public.outbox', 'again')"})
	again, err := l.Wait(wctx)
	if err != nil || again.Payload != "again" {
		t.Fatalf("the listener after a timed-out wait: %+v, %v", again, err)
	}

	boom := errors.New("the segment write failed")
	if _, err := tk.DrainOutbox(context.Background(), "target", "public.outbox", 10, func([]OutboxRow) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("a failed write: %v", err)
	}
	var got []OutboxRow
	read, err := tk.DrainOutbox(context.Background(), "target", "public.outbox", 10, func(rows []OutboxRow) error { got = rows; return nil })
	if err != nil || read != 1 || len(got) != 1 {
		t.Fatalf("drain after a failed write: read %d, %v, rows %+v", read, err, got)
	}
	if got[0].Table != "public.orders" || got[0].Op != "INSERT" || !strings.Contains(got[0].Payload, `"total": 12.50`) {
		t.Fatalf("row = %+v", got[0])
	}
	called := false
	read, err = tk.DrainOutbox(context.Background(), "target", "public.outbox", 10, func([]OutboxRow) error { called = true; return nil })
	if err != nil || read != 0 || called {
		t.Fatalf("a drained row is delivered again: read %d, %v, called %v", read, err, called)
	}

	if err := tk.CheckOutbox(context.Background(), "target", "public.outbox"); err != nil {
		t.Fatalf("the outbox the template made: %v", err)
	}
	if err := tk.CheckOutbox(context.Background(), "target", "public.missing"); err == nil || !strings.Contains(err.Error(), "42P01") {
		t.Fatalf("a missing outbox: %v", err)
	}
	if err := tk.CheckOutbox(context.Background(), "nope", "public.outbox"); err == nil {
		t.Fatal("an unknown connection passed the check")
	}

	probe := tk.ProbeConnection(context.Background(), "target")
	if !probe.OK || !strings.Contains(probe.Detail, "LISTEN/NOTIFY delivers") {
		t.Fatalf("probe = %+v", probe)
	}

	schema(t, ctx, tk, schemaInput{Command: schemaDown, ToVersion: new(int)}, false)
	res := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*) FROM pg_proc WHERE proname = 'outbox_capture'"})
	if res.Rows[0][0] != int64(0) {
		t.Fatalf("down left the trigger function: %v", res.Rows)
	}
}
