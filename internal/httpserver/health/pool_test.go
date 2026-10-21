package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// starvedPool is a pool of one whose connection is taken.
func starvedPool(t *testing.T) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return db, conn
}

func readyz(hc *Checker) (code int, status string) {
	rec := httptest.NewRecorder()
	hc.ReadinessHandler()(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", http.NoBody))
	var body struct{ Status string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Status
}

// #2169: a pool that hands out no connection for longer than starvedFor takes
// a ready replica out of service, and it comes back once a connection frees.
func TestReadiness_AStarvedPoolIsNotReady(t *testing.T) {
	db, conn := starvedPool(t)
	clock := time.Unix(1_000, 0)
	probe := NewPoolProbe(db, 10*time.Millisecond, 30*time.Second)
	probe.now = func() time.Time { return clock }
	hc := NewChecker()
	hc.SetReady()
	hc.WatchPool(probe)

	if code, status := readyz(hc); code != http.StatusOK || status != stateNameReady {
		t.Fatalf("first starved probe: %d %s; a pool is given starvedFor before it counts", code, status)
	}
	clock = clock.Add(29 * time.Second)
	if code, _ := readyz(hc); code != http.StatusOK {
		t.Fatalf("starved for 29s: %d", code)
	}
	clock = clock.Add(time.Second)
	if code, status := readyz(hc); code != http.StatusServiceUnavailable || status != statusPoolStarved {
		t.Fatalf("starved for 30s: %d %s", code, status)
	}

	_ = conn.Close()
	if code, _ := readyz(hc); code != http.StatusOK {
		t.Fatalf("a freed connection: %d", code)
	}
	clock = clock.Add(time.Minute)
	if _, err := db.Conn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _ := readyz(hc); code != http.StatusOK {
		t.Fatalf("starvation counts again from its own start: %d", code)
	}
}

// A pool with room, an unbounded pool and no pool at all are never starved;
// a starved pool does not make a draining replica report anything else.
func TestPoolProbe_NotStarved(t *testing.T) {
	ctx := context.Background()
	var nilProbe *PoolProbe
	if nilProbe.Starved(ctx) || NewPoolProbe(nil, time.Millisecond, 0).Starved(ctx) {
		t.Error("no pool is starved")
	}
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if NewPoolProbe(db, time.Millisecond, 0).Starved(ctx) {
		t.Error("an unbounded pool is starved")
	}
	db.SetMaxOpenConns(2)
	if NewPoolProbe(db, time.Millisecond, 0).Starved(ctx) {
		t.Error("a pool with room is starved")
	}

	starved, _ := starvedPool(t)
	hc := NewChecker()
	hc.SetDraining()
	hc.WatchPool(NewPoolProbe(starved, time.Millisecond, 0))
	if code, status := readyz(hc); code != http.StatusServiceUnavailable || status != stateNameDraining {
		t.Errorf("draining with a starved pool: %d %s", code, status)
	}
}

// A pool every connection of which is in use, but which hands one out within
// the wait, is busy rather than starved.
func TestPoolProbe_ABusyPoolIsNotStarved(t *testing.T) {
	db, conn := starvedPool(t)
	probe := NewPoolProbe(db, 2*time.Second, 0)
	freed := make(chan struct{})
	go func() {
		defer close(freed)
		time.Sleep(20 * time.Millisecond)
		_ = conn.Close()
	}()
	if probe.Starved(context.Background()) {
		t.Error("a pool that handed out a connection within the wait read as starved")
	}
	<-freed
}
