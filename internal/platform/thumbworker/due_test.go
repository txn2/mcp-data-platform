package thumbworker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// dueWork is the due-set half of a fake store (#2084): what its sweep and
// clear return, and how often each was asked.
type dueWork struct {
	dueMu    sync.Mutex
	sweepN   int64
	sweepErr error
	clears   []int64
	clearErr error
	swept    int
	cleared  int
}

func (d *dueWork) sweep() (int64, error) {
	d.dueMu.Lock()
	defer d.dueMu.Unlock()
	d.swept++
	return d.sweepN, d.sweepErr
}

func (d *dueWork) clear() (int64, error) {
	d.dueMu.Lock()
	defer d.dueMu.Unlock()
	d.cleared++
	if d.clearErr != nil {
		return 0, d.clearErr
	}
	if len(d.clears) == 0 {
		return 0, nil
	}
	n := d.clears[0]
	d.clears = d.clears[1:]
	return n, nil
}

func (d *dueWork) counts() (swept, cleared int) {
	d.dueMu.Lock()
	defer d.dueMu.Unlock()
	return d.swept, d.cleared
}

func (d *dueWork) SweepThumbnailWork(context.Context, int) (int64, error) { return d.sweep() }
func (d *dueWork) ClearThumbnailWork(context.Context, int, int) (int64, error) {
	return d.clear()
}
func (d *dueWork) SweepCollectionThumbnailWork(context.Context) (int64, error) { return d.sweep() }
func (d *dueWork) ClearCollectionThumbnailWork(context.Context, int) (int64, error) {
	return d.clear()
}

// A pass clears every kind's settled rows before it claims, and a full clear
// batch counts as work found, so the worker comes straight back for the rest
// rather than waiting a poll per thousand rows (#2084).
func TestPass_AFullClearBatchIsWorkFound(t *testing.T) {
	assets := &fakeAssets{dueWork: dueWork{clears: []int64{clearBatch, 3}}}
	res := &fakeResources{dueWork: dueWork{clears: []int64{5}}}
	colls := &fakeCollections{dueWork: dueWork{clearErr: errors.New("db down")}}
	w := New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Assets: assets, Resources: res, Collections: colls})

	if !w.pass(context.Background()) {
		t.Error("a pass that cleared a full batch reported no work")
	}
	if w.pass(context.Background()) {
		t.Error("a pass that cleared less than a batch and claimed nothing reported work")
	}
	for name, d := range map[string]*dueWork{"assets": &assets.dueWork, "resources": &res.dueWork, "collections": &colls.dueWork} {
		if _, cleared := d.counts(); cleared != 2 {
			t.Errorf("%s were cleared %d times in two passes, want 2", name, cleared)
		}
	}
}

// Each kind is swept once when the worker starts, before its first pass, and
// a sweep that fails is logged rather than stopping the worker.
func TestWorker_SweepsEachKindOnceAtStart(t *testing.T) {
	claimed := make(chan struct{}, 4)
	assets := &fakeAssets{dueWork: dueWork{sweepN: 12}}
	res := &fakeResources{dueWork: dueWork{sweepErr: errors.New("db down")}}
	colls := &signalingCollections{fakeCollections: fakeCollections{}, claimed: claimed}
	w := New(Tuning{Poll: time.Hour}, Deps{Drawer: &fakeDrawer{}, Assets: assets, Resources: res, Collections: colls})
	w.Start(context.Background())
	awaitClaim(t, claimed)
	w.Stop()
	for name, d := range map[string]*dueWork{"assets": &assets.dueWork, "resources": &res.dueWork, "collections": &colls.dueWork} {
		if swept, _ := d.counts(); swept != 1 {
			t.Errorf("%s were swept %d times, want once", name, swept)
		}
	}
}

// A row becoming due wakes an idle worker for a pass at once, however long
// its poll (#2084).
func TestWorker_NotifyWakesAnIdleWorker(t *testing.T) {
	claimed := make(chan struct{}, 4)
	colls := &signalingCollections{claimed: claimed}
	w := New(Tuning{Poll: time.Hour}, Deps{Drawer: &fakeDrawer{}, Assets: &fakeAssets{}, Collections: colls})
	w.Start(context.Background())
	defer w.Stop()
	awaitClaim(t, claimed) // the first pass runs at start
	w.Notify()
	w.Notify() // a second signal while one is pending is dropped, not blocked on
	awaitClaim(t, claimed)
}

// An idle worker's wait doubles from the configured poll to a minute, or to
// the poll when that is longer.
func TestNextIdle(t *testing.T) {
	for _, tc := range []struct{ wait, poll, want time.Duration }{
		{5 * time.Second, 5 * time.Second, 10 * time.Second},
		{40 * time.Second, 5 * time.Second, time.Minute},
		{time.Minute, 5 * time.Second, time.Minute},
		{2 * time.Minute, 2 * time.Minute, 2 * time.Minute},
	} {
		if got := nextIdle(tc.wait, tc.poll); got != tc.want {
			t.Errorf("nextIdle(%v, %v) = %v, want %v", tc.wait, tc.poll, got, tc.want)
		}
	}
}

// signalingCollections reports every claim on claimed.
type signalingCollections struct {
	fakeCollections
	claimed chan struct{}
}

func (s *signalingCollections) ClaimCollectionThumbnailWork(context.Context, time.Duration, int) ([]portaldomain.CollectionThumbnailWork, error) {
	s.claimed <- struct{}{}
	return nil, nil
}

func awaitClaim(t *testing.T, claimed <-chan struct{}) {
	t.Helper()
	select {
	case <-claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker made no pass")
	}
}

// A second signal while one is waiting is dropped rather than blocked on.
func TestNotify_ASecondSignalIsDropped(t *testing.T) {
	w := New(Tuning{}, Deps{Drawer: &fakeDrawer{}})
	w.Notify()
	w.Notify()
	if len(w.wake) != 1 {
		t.Errorf("%d signals waiting, want one", len(w.wake))
	}
}

// A worker given a database it cannot listen on still works, polling.
func TestWorker_AListenerThatCannotConnectLeavesThePoll(t *testing.T) {
	claimed := make(chan struct{}, 4)
	w := New(Tuning{Poll: time.Hour}, Deps{
		Drawer: &fakeDrawer{}, Collections: &signalingCollections{claimed: claimed},
		DSN: "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
	})
	w.Start(context.Background())
	awaitClaim(t, claimed)
	w.Stop()
}
