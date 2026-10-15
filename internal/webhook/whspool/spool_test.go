package whspool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func openSpool(t *testing.T, dir string) *Spool {
	t.Helper()
	s, err := Open(Options{Dir: dir, Replica: "replica-a", Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func rec(id string, at time.Time) Record {
	return Record{Hash: "h-" + id, Events: []whevent.Event{{
		ReceivedAt: at, EventID: id, EventType: "t", Replica: "replica-a", Payload: []byte(`{"id":"` + id + `"}`),
	}}}
}

func TestAppendSealReadRemove(t *testing.T) {
	s := openSpool(t, t.TempDir())
	defer s.Close() //nolint:errcheck // test
	for i := range 3 {
		if _, err := s.Append("orders", rec(fmt.Sprint(i), t0)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	af, ok := s.Active("orders")
	if !ok || af.Events != 3 || af.Bytes == 0 || !af.Oldest.Equal(t0) {
		t.Fatalf("Active = %+v %v", af, ok)
	}
	if err := s.Seal("orders"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Active("orders"); ok {
		t.Fatal("a sealed file is still active")
	}
	segs := s.Sealed("orders")
	if len(segs) != 1 || segs[0].Events != 3 {
		t.Fatalf("Sealed = %+v", segs)
	}
	recs, err := s.Read(segs[0])
	if err != nil || len(recs) != 3 || recs[2].Events[0].EventID != "2" || string(recs[0].Events[0].Payload) != `{"id":"0"}` {
		t.Fatalf("Read = %+v, %v", recs, err)
	}
	if st := s.Stats(); st.Events != 3 || st.Files != 1 {
		t.Fatalf("Stats = %+v", st)
	}
	if err := s.Remove(segs[0]); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(); st.Events != 0 || st.Files != 0 || !st.Oldest.IsZero() {
		t.Fatalf("Stats after remove = %+v", st)
	}
	if got := s.Sources(); len(got) != 0 {
		t.Fatalf("Sources = %v", got)
	}
}

// A process killed mid-append leaves a torn record after the last synced
// one. The next process seals every file it finds, keeps every whole record
// and cuts off the rest, which no sender was ever told was stored.
func TestOpenRecoversWhatAKilledProcessLeft(t *testing.T) {
	dir := t.TempDir()
	s := openSpool(t, dir)
	for i := range 2 {
		if _, err := s.Append("orders", rec(fmt.Sprint(i), t0)); err != nil {
			t.Fatal(err)
		}
	}
	path := s.active["orders"].seg.path
	// Kill: no seal, no close; the lock goes with the process.
	_ = unlockDir(s.lock)
	torn, err := encode(rec("torn", t0))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, fileMode) // #nosec G304 -- the test's own temporary spool
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(torn[:len(torn)-3]); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(dir, "orders", fileName("old", 9)), nil, fileMode); err != nil {
		t.Fatal(err)
	}

	again := openSpool(t, dir)
	defer again.Close() //nolint:errcheck // test
	segs := again.Sealed("orders")
	if len(segs) != 1 || segs[0].Events != 2 {
		t.Fatalf("Sealed = %+v, want one file of the two whole records", segs)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != segs[0].Bytes {
		t.Fatalf("the torn tail was not cut off: size %v, records %d bytes (%v)", info.Size(), segs[0].Bytes, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orders", fileName("old", 9))); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file with no whole record was kept")
	}
	if got := again.Sources(); len(got) != 1 || got[0] != "orders" {
		t.Errorf("Sources = %v", got)
	}
	if _, err := again.Append("orders", rec("2", t0)); err != nil {
		t.Fatal(err)
	}
	if af, _ := again.Active("orders"); af.Events != 1 {
		t.Error("the new process appended to an old file instead of its own")
	}
}

func TestAppendRefusesPastItsBounds(t *testing.T) {
	now := t0
	s, err := Open(Options{Dir: t.TempDir(), Replica: "r", MaxBytes: 400, MaxAge: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test
	if _, err := s.Append("a", rec("1", now)); err != nil {
		t.Fatal(err)
	}
	big := rec("2", now)
	big.Events[0].Payload = make([]byte, 400)
	if _, err := s.Append("a", big); !errors.Is(err, ErrFull) {
		t.Fatalf("an append past the byte bound = %v, want ErrFull", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Append("a", rec("3", now)); !errors.Is(err, ErrFull) {
		t.Fatalf("an append past the age bound = %v, want ErrFull", err)
	}
	if st := s.Stats(); st.Events != 1 {
		t.Errorf("a refused append was kept: %+v", st)
	}
}

func TestOpenRefusesASpoolAnotherProcessHolds(t *testing.T) {
	dir := t.TempDir()
	s := openSpool(t, dir)
	if _, err := Open(Options{Dir: dir, Replica: "b"}); err == nil {
		t.Fatal("a second process opened a spool another holds")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again := openSpool(t, dir)
	_ = again.Close()
}

func TestOpenNeedsADirectory(t *testing.T) {
	if _, err := Open(Options{}); err == nil {
		t.Fatal("a spool opened with no directory")
	}
}

func TestAppendAfterCloseIsRefused(t *testing.T) {
	s := openSpool(t, t.TempDir())
	if _, err := s.Append("a", rec("1", t0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("a", rec("2", t0)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append after Close = %v", err)
	}
	if segs := s.Sealed("a"); len(segs) != 1 {
		t.Fatalf("Close did not seal the active file: %+v", segs)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("a second Close: %v", err)
	}
}

// Appends from many requests at once each return only once their record is
// synced, and every one is read back.
func TestConcurrentAppendsAreAllKept(t *testing.T) {
	s := openSpool(t, t.TempDir())
	defer s.Close() //nolint:errcheck // test
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			if _, err := s.Append("a", rec(fmt.Sprint(i), t0)); err != nil {
				t.Error(err)
			}
			if i%16 == 0 {
				_ = s.Seal("a")
			}
		})
	}
	wg.Wait()
	_ = s.Seal("a")
	seen := map[string]bool{}
	for _, seg := range s.Sealed("a") {
		recs, err := s.Read(seg)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			seen[r.Events[0].EventID] = true
		}
	}
	if len(seen) != 64 {
		t.Fatalf("read back %d of 64 records", len(seen))
	}
}

// A write that fails takes the file out of service at what was synced: the
// records acknowledged before it are kept and sealed, and the next append
// opens a new file.
func TestAFailedWriteKeepsWhatWasAcknowledged(t *testing.T) {
	s := openSpool(t, t.TempDir())
	defer s.Close() //nolint:errcheck // test
	if _, err := s.Append("a", rec("1", t0)); err != nil {
		t.Fatal(err)
	}
	a := s.active["a"]
	_ = a.f.Close() // the next write fails
	if _, err := s.Append("a", rec("2", t0)); err == nil {
		t.Fatal("an append to a file that cannot be written succeeded")
	}
	segs := s.Sealed("a")
	if len(segs) != 1 || segs[0].Events != 1 {
		t.Fatalf("Sealed = %+v, want the acknowledged record kept", segs)
	}
	if _, err := s.Append("a", rec("3", t0)); err != nil {
		t.Fatalf("the append after a failed one: %v", err)
	}
	if s.active["a"] == a {
		t.Error("a broken file is still active")
	}
}

func TestParseName(t *testing.T) {
	w, seq, ok := parseName(fileName("replica-a-k3s", 42))
	if !ok || w != "replica-a-k3s" || seq != 42 {
		t.Fatalf("parseName = %q %d %v", w, seq, ok)
	}
	for _, bad := range []string{"x.txt", "noseq.wal", "-1.wal", "a-b.wal"} {
		if _, _, ok := parseName(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestNextRecordStopsAtDamage(t *testing.T) {
	data, err := encode(rec("1", t0))
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if _, _, ok := nextRecord(data, 0); ok {
		t.Error("a record whose checksum does not match was read")
	}
	if _, _, ok := nextRecord([]byte{0, 0}, 0); ok {
		t.Error("a short header was read")
	}
}

// A record longer than a read accepts is refused before it is written, so it
// is never acknowledged and then read back as a damaged tail.
func TestEncodeRefusesARecordTooLargeToReadBack(t *testing.T) {
	big := rec("1", t0)
	big.Events[0].Payload = make([]byte, maxRecordBytes)
	if _, err := encode(big); err == nil {
		t.Fatal("a record larger than a read accepts was encoded")
	}
}

// A sync that fails takes the file out of service at what was synced before:
// the records acknowledged earlier are sealed, and the failing append is not
// acknowledged.
func TestAFailedSyncKeepsWhatWasSyncedBefore(t *testing.T) {
	s := openSpool(t, t.TempDir())
	defer s.Close() //nolint:errcheck // test
	if _, err := s.Append("a", rec("1", t0)); err != nil {
		t.Fatal(err)
	}
	a := s.active["a"]
	synced := a.synced
	if _, err := s.Append("a", rec("2", t0)); err != nil {
		t.Fatal(err)
	}
	// Pretend the second record was written and not yet synced, and make the
	// sync fail.
	a.synced = synced
	_ = a.f.Close()
	if err := s.syncTo(a, a.seg.Bytes); err == nil {
		t.Fatal("a sync of a file that cannot be synced succeeded")
	}
	if err := s.syncTo(a, a.seg.Bytes); err == nil {
		t.Fatal("a broken file reported a later sync as done")
	}
	segs := s.Sealed("a")
	if len(segs) != 1 || segs[0].Events != 1 {
		t.Fatalf("Sealed = %+v, want the one record synced before the failure", segs)
	}
}

// Sealing a file whose last records are not yet synced syncs them first; a
// sync that fails then is reported and the file is retired at what was synced.
func TestSealSyncsFirstAndReportsAFailedSync(t *testing.T) {
	s := openSpool(t, t.TempDir())
	defer s.Close() //nolint:errcheck // test
	if _, err := s.Append("a", rec("1", t0)); err != nil {
		t.Fatal(err)
	}
	a := s.active["a"]
	a.synced = 0
	_ = a.f.Close()
	if err := s.Seal("a"); err == nil {
		t.Fatal("sealing a file whose sync fails succeeded")
	}
	if err := s.sealFile(a); err != nil {
		t.Fatalf("sealing a retired file again: %v", err)
	}
	if err := s.Seal("nothing"); err != nil {
		t.Fatalf("sealing a source with no active file: %v", err)
	}
}

// What an earlier process left is drained oldest first, and anything in the
// directory that is not a spool file is passed over.
func TestRecoveryOrdersFilesAndSkipsStrangers(t *testing.T) {
	dir := t.TempDir()
	later, earlier := t0.Add(time.Hour), t0
	for i, at := range []time.Time{later, earlier} {
		s, err := Open(Options{Dir: dir, Replica: fmt.Sprintf("r%d", i), Now: func() time.Time { return t0.Add(time.Duration(i)) }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append("a", rec(fmt.Sprint(i), at)); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, stray := range []string{filepath.Join(dir, "a", "notes.txt"), filepath.Join(dir, "README")} {
		if err := os.WriteFile(stray, []byte("x"), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "a", "sub.wal"), dirMode); err != nil {
		t.Fatal(err)
	}
	s := openSpool(t, dir)
	defer s.Close() //nolint:errcheck // test
	segs := s.Sealed("a")
	if len(segs) != 2 || !segs[0].Oldest.Equal(earlier) || !segs[1].Oldest.Equal(later) {
		t.Fatalf("Sealed = %+v, want the earlier file first", segs)
	}
	if s.Dir() != dir {
		t.Errorf("Dir = %q", s.Dir())
	}
	if !before(Segment{Writer: "a", Seq: 1}, Segment{Writer: "b", Seq: 1}) || !before(Segment{Writer: "a", Seq: 1}, Segment{Writer: "a", Seq: 2}) {
		t.Error("files received at the same time are not ordered by name")
	}
}

func TestOldestOf(t *testing.T) {
	at := t0.Add(time.Minute)
	if got := oldestOf(Record{}, at); !got.Equal(at) {
		t.Errorf("a record with no events = %v, want the append time", got)
	}
	if got := oldestOf(Record{Events: []whevent.Event{{}}}, at); !got.Equal(at) {
		t.Errorf("an event with no receive time = %v, want the append time", got)
	}
	if got := oldestOf(rec("1", t0), at); !got.Equal(t0) {
		t.Errorf("= %v, want the event's receive time", got)
	}
}

// A source whose directory cannot be made is an append that fails, not one
// that is acknowledged.
func TestAppendFailsWhenItsFileCannotBeMade(t *testing.T) {
	dir := t.TempDir()
	s := openSpool(t, dir)
	defer s.Close() //nolint:errcheck // test
	if err := os.WriteFile(filepath.Join(dir, "blocked"), []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("blocked", rec("1", t0)); err == nil {
		t.Fatal("an append whose file cannot be made succeeded")
	}
}

func TestRemoveReportsWhatItCannotRemove(t *testing.T) {
	dir := t.TempDir()
	s := openSpool(t, dir)
	defer s.Close() //nolint:errcheck // test
	full := filepath.Join(dir, "full")
	if err := os.MkdirAll(filepath.Join(full, "x"), dirMode); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(Segment{Source: "a", path: full}); err == nil {
		t.Error("removing a directory that is not empty succeeded")
	}
	if err := s.Remove(Segment{Source: "a", path: filepath.Join(dir, "gone", "x.wal")}); err == nil {
		t.Error("removing from a directory that does not exist reported its sync as done")
	}
}
