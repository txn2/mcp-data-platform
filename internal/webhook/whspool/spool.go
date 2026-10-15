// Package whspool is a webhook receiver's local write-ahead spool (#2121): the
// events of an acknowledged request are appended to a file on this replica's
// disk and synced before the request is answered, then drained to object
// storage in the background.
//
// Without it a request is answered only once its events are in object
// storage, so a sender's wait is object storage's latency at that moment, and
// a slow object store fails deliveries at the sender while the platform is
// healthy. With it the wait is a local fsync.
//
// The spool holds one directory per source. Each source has at most one
// active file, which requests append to, and any number of sealed files
// waiting to be drained, oldest first. A file is named for the process that
// created it and a sequence, which is also the name its segments are written
// under in object storage: draining a file a second time -- after a crash
// between its write and its removal -- overwrites the same objects instead of
// adding to them, so every acknowledged event lands once.
//
// A record is its length, its CRC-32C and its JSON. A record whose sync did
// not finish was never acknowledged, so the torn tail a crash leaves is cut
// off when the spool is opened.
package whspool

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
)

// Defaults for the bounds past which a spool refuses new requests.
const (
	DefaultMaxBytes int64 = 1 << 30
	DefaultMaxAge         = time.Hour
)

// fileExt names a spool file.
const fileExt = ".wal"

// lockName is the file a process holds its lock on the spool through.
const lockName = ".lock"

// headerBytes is a record's length and checksum.
const headerBytes = 8

// maxRecordBytes bounds a record read back. A larger length is a damaged
// header, read as the end of the file.
const maxRecordBytes = 256 << 20

// File and directory modes: the spool is the platform's own data.
const (
	dirMode  = 0o750
	fileMode = 0o640
)

// seqWidth is the digits a file's sequence is written in, in base decimal,
// as a seqBits-bit number.
const (
	seqWidth = 10
	decimal  = 10
	seqBits  = 64
)

// errReading is how a file that could not be read is reported.
const errReading = "whspool: reading %s: %w"

// writerBase is the base the process start time is written in.
const writerBase = 36

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// ErrFull is an append refused because the spool holds as much as it may: its
// files' bytes, or the age of its oldest event, are at their bound. The
// request is answered 503 so its sender retries.
var ErrFull = errors.New("the receiver's spool is full")

// ErrClosed is an append after the spool was closed.
var ErrClosed = errors.New("the receiver's spool is closed")

// Options open a spool.
type Options struct {
	// Dir is the spool's directory, created when it does not exist.
	Dir string
	// MaxBytes bounds the bytes of every file in the spool. Zero takes
	// DefaultMaxBytes.
	MaxBytes int64
	// MaxAge bounds the age of the oldest event not yet drained. Zero takes
	// DefaultMaxAge.
	MaxAge time.Duration
	// Replica is this process's identity, which the files it creates are
	// named for.
	Replica string
	Now     func() time.Time
}

// Record is one acknowledged request: the SHA-256 of its body and its events.
type Record struct {
	Hash   string          `json:"hash"`
	Events []whevent.Event `json:"events"`
}

// Segment is one sealed spool file waiting to be drained.
type Segment struct {
	Source string
	// Writer and Seq name the file, and the objects it is drained to.
	Writer string
	Seq    uint64
	Bytes  int64
	Events int
	// Oldest is when the file's first event was received.
	Oldest time.Time
	path   string
}

// Stats is what a spool holds: every file, active and sealed.
type Stats struct {
	Bytes  int64
	Events int64
	Files  int
	// Oldest is when the oldest event not yet drained was received; zero
	// when the spool is empty.
	Oldest time.Time
}

// active is a source's file that requests append to.
type active struct {
	seg Segment
	f   *os.File

	// syncMu serializes syncs, so concurrent appends share one: an append
	// whose bytes a sync already covered returns without another.
	syncMu sync.Mutex
	synced int64
	broken error
	// retired is set, under mu, once a broken file is taken out of service.
	retired bool
}

// Spool is one replica's spool directory.
type Spool struct {
	dir      string
	maxBytes int64
	maxAge   time.Duration
	writer   string
	now      func() time.Time
	lock     *os.File

	mu     sync.Mutex
	seq    uint64
	closed bool
	active map[string]*active
	sealed map[string][]Segment
}

// Open opens the spool at o.Dir, taking this process's lock on it, and reads
// what an earlier process left there: every file is sealed, a torn tail is
// cut off, and a file with no whole record is removed.
func Open(o Options) (*Spool, error) {
	if strings.TrimSpace(o.Dir) == "" {
		return nil, errors.New("whspool: no spool directory")
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxAge <= 0 {
		o.MaxAge = DefaultMaxAge
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("whspool: creating %s: %w", o.Dir, err)
	}
	lock, err := lockDir(filepath.Join(o.Dir, lockName))
	if err != nil {
		return nil, fmt.Errorf("whspool: %s: %w", o.Dir, err)
	}
	s := &Spool{
		dir: o.Dir, maxBytes: o.MaxBytes, maxAge: o.MaxAge, now: o.Now, lock: lock,
		writer: whlayout.ReplicaSlug(o.Replica) + "-" + strconv.FormatInt(o.Now().UnixNano(), writerBase) + "s",
		active: map[string]*active{}, sealed: map[string][]Segment{},
	}
	if err := s.recover(); err != nil {
		_ = unlockDir(lock)
		return nil, err
	}
	return s, nil
}

// Dir is the spool's directory.
func (s *Spool) Dir() string { return s.dir }

// recover seals every file an earlier process left.
func (s *Spool) recover() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf(errReading, s.dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.recoverSource(e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Spool) recoverSource(source string) error {
	dir := filepath.Join(s.dir, source)
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf(errReading, dir, err)
	}
	for _, f := range files {
		writer, seq, ok := parseName(f.Name())
		if f.IsDir() || !ok {
			continue
		}
		seg := Segment{Source: source, Writer: writer, Seq: seq, path: filepath.Join(dir, f.Name())}
		if err := scan(&seg); err != nil {
			return err
		}
		if seg.Events == 0 {
			if err := os.Remove(seg.path); err != nil {
				return fmt.Errorf("whspool: removing the empty %s: %w", seg.path, err)
			}
			continue
		}
		s.sealed[source] = append(s.sealed[source], seg)
	}
	sort.Slice(s.sealed[source], func(i, j int) bool { return before(s.sealed[source][i], s.sealed[source][j]) })
	return nil
}

// before orders a source's files: oldest first, then by name.
func before(a, b Segment) bool {
	if !a.Oldest.Equal(b.Oldest) {
		return a.Oldest.Before(b.Oldest)
	}
	if a.Writer != b.Writer {
		return a.Writer < b.Writer
	}
	return a.Seq < b.Seq
}

// fileName is a file's name in its source's directory.
func fileName(writer string, seq uint64) string {
	return fmt.Sprintf("%s-%0*d%s", writer, seqWidth, seq, fileExt)
}

// parseName reads a file's writer and sequence from its name.
func parseName(name string) (writer string, seq uint64, ok bool) {
	base, found := strings.CutSuffix(name, fileExt)
	if !found {
		return "", 0, false
	}
	i := strings.LastIndexByte(base, '-')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.ParseUint(base[i+1:], decimal, seqBits)
	if err != nil {
		return "", 0, false
	}
	return base[:i], n, true
}

// Append writes rec to the source's active file and syncs it. Once it returns
// nil the record survives a crash, and the request may be answered. It
// returns ErrFull when the spool is at a bound and ErrClosed once closed, and
// how long the write and the sync took.
func (s *Spool) Append(source string, rec Record) (time.Duration, error) {
	began := s.now()
	data, err := encode(rec)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	if err := s.roomFor(int64(len(data))); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	a, err := s.activeFor(source)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if _, err := a.f.Write(data); err != nil {
		// No other append may land after a partial one: the file leaves
		// service now, and is cut back to what was synced once no sync is
		// under way.
		delete(s.active, source)
		s.mu.Unlock()
		err = fmt.Errorf("whspool: appending to %s: %w", a.seg.path, err)
		s.retire(a, err)
		return 0, err
	}
	if a.seg.Events == 0 {
		a.seg.Oldest = oldestOf(rec, began)
	}
	a.seg.Bytes += int64(len(data))
	a.seg.Events += len(rec.Events)
	target := a.seg.Bytes
	s.mu.Unlock()
	if err := s.syncTo(a, target); err != nil {
		return 0, err
	}
	return s.now().Sub(began), nil
}

// oldestOf is when a record's first event was received, or at when it has
// none.
func oldestOf(rec Record, at time.Time) time.Time {
	if len(rec.Events) > 0 && !rec.Events[0].ReceivedAt.IsZero() {
		return rec.Events[0].ReceivedAt
	}
	return at
}

// roomFor refuses a record the spool has no room for. The caller holds mu.
func (s *Spool) roomFor(n int64) error {
	st := s.statsLocked()
	if st.Bytes+n > s.maxBytes {
		return fmt.Errorf("whspool: %w: it holds %d bytes of a %d byte bound", ErrFull, st.Bytes, s.maxBytes)
	}
	if !st.Oldest.IsZero() && s.now().Sub(st.Oldest) > s.maxAge {
		return fmt.Errorf("whspool: %w: its oldest event is older than %s and has not been drained", ErrFull, s.maxAge)
	}
	return nil
}

// activeFor returns the source's active file, creating it -- and making its
// directory entry durable -- when there is none. The caller holds mu.
func (s *Spool) activeFor(source string) (*active, error) {
	if a := s.active[source]; a != nil {
		return a, nil
	}
	dir := filepath.Join(s.dir, source)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("whspool: creating %s: %w", dir, err)
	}
	seg, f, err := s.create(source, dir)
	if err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		_ = f.Close()
		_ = os.Remove(seg.path)
		return nil, err
	}
	a := &active{seg: seg, f: f}
	s.active[source] = a
	return a, nil
}

// maxNameTries bounds the names create tries past ones already taken.
const maxNameTries = 1000

// create makes the source's next file. A name already taken -- a file an
// earlier process with the same name and start left, which its contents are
// still waiting to be drained from -- is passed over, never reused, since a
// drained file's name is also the name of the objects it was written to.
// The caller holds mu.
func (s *Spool) create(source, dir string) (Segment, *os.File, error) {
	for range maxNameTries {
		s.seq++
		seg := Segment{Source: source, Writer: s.writer, Seq: s.seq, path: filepath.Join(dir, fileName(s.writer, s.seq))}
		f, err := os.OpenFile(seg.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, fileMode) // #nosec G304 -- the path is the spool's own, built from a validated source name
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Segment{}, nil, fmt.Errorf("whspool: creating %s: %w", seg.path, err)
		}
		return seg, f, nil
	}
	return Segment{}, nil, fmt.Errorf("whspool: no free file name in %s", dir)
}

// syncTo syncs a's file until it holds at least target bytes on disk. A sync
// that fails breaks the file: what it did not cover was never acknowledged
// and is cut off, and the file is sealed at what was.
func (s *Spool) syncTo(a *active, target int64) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.broken != nil {
		return a.broken
	}
	if a.synced >= target {
		return nil
	}
	s.mu.Lock()
	end := a.seg.Bytes
	s.mu.Unlock()
	if err := a.f.Sync(); err != nil {
		a.broken = fmt.Errorf("whspool: syncing %s: %w", a.seg.path, err)
		s.mu.Lock()
		s.retireLocked(a)
		s.mu.Unlock()
		return a.broken
	}
	a.synced = end
	return nil
}

// retire takes a file whose append failed out of service.
func (s *Spool) retire(a *active, cause error) {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.broken == nil {
		a.broken = cause
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireLocked(a)
}

// retireLocked takes a file that failed a write or a sync out of service,
// once: it is cut back to what was synced -- nothing past that was
// acknowledged -- and sealed when that holds a record. The caller holds
// a.syncMu and mu.
func (s *Spool) retireLocked(a *active) {
	if a.retired {
		return
	}
	a.retired = true
	if s.active[a.seg.Source] == a {
		delete(s.active, a.seg.Source)
	}
	// Cut by the handle, and by the path when the handle is the thing that
	// failed: what follows the synced length was never acknowledged, and
	// left in place it would be drained as if it had been.
	if a.f.Truncate(a.synced) != nil {
		_ = os.Truncate(a.seg.path, a.synced)
	}
	_ = a.f.Close()
	seg := a.seg
	if err := scan(&seg); err != nil || seg.Events == 0 {
		_ = os.Remove(seg.path)
		return
	}
	s.sealed[seg.Source] = append(s.sealed[seg.Source], seg)
}

// Active reports what the source's active file holds, as the Stats of that
// one file, and false when it has none.
func (s *Spool) Active(source string) (Stats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.active[source]
	if a == nil {
		return Stats{}, false
	}
	return Stats{Bytes: a.seg.Bytes, Events: int64(a.seg.Events), Files: 1, Oldest: a.seg.Oldest}, true
}

// Seal closes the source's active file, synced, and queues it to be drained.
// The next append opens a new one.
func (s *Spool) Seal(source string) error {
	s.mu.Lock()
	a := s.active[source]
	delete(s.active, source)
	s.mu.Unlock()
	if a == nil {
		return nil
	}
	return s.sealFile(a)
}

// sealFile syncs and closes a, and queues it to be drained.
func (s *Spool) sealFile(a *active) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.broken != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.synced < a.seg.Bytes {
		if err := a.f.Sync(); err != nil {
			a.broken = fmt.Errorf("whspool: syncing %s: %w", a.seg.path, err)
			s.retireLocked(a)
			return a.broken
		}
		a.synced = a.seg.Bytes
	}
	if err := a.f.Close(); err != nil {
		return fmt.Errorf("whspool: closing %s: %w", a.seg.path, err)
	}
	s.sealed[a.seg.Source] = append(s.sealed[a.seg.Source], a.seg)
	return nil
}

// Sealed returns the source's sealed files, oldest first.
func (s *Spool) Sealed(source string) []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Segment(nil), s.sealed[source]...)
}

// Sources returns every source the spool holds a file for, sorted.
func (s *Spool) Sources() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for src := range s.active {
		seen[src] = true
	}
	for src, segs := range s.sealed {
		if len(segs) > 0 {
			seen[src] = true
		}
	}
	out := make([]string, 0, len(seen))
	for src := range seen {
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

// Read returns a sealed file's records.
func (*Spool) Read(seg Segment) ([]Record, error) {
	data, err := os.ReadFile(seg.path) // #nosec G304 -- the path is the spool's own
	if err != nil {
		return nil, fmt.Errorf("whspool: reading %s: %w", seg.path, err)
	}
	var out []Record
	for off := 0; ; {
		payload, next, ok := nextRecord(data, off)
		if !ok {
			return out, nil
		}
		var rec Record
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil, fmt.Errorf("whspool: %s holds a record that does not decode: %w", seg.path, err)
		}
		out = append(out, rec)
		off = next
	}
}

// Remove deletes a drained file.
func (s *Spool) Remove(seg Segment) error {
	if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("whspool: removing %s: %w", seg.path, err)
	}
	s.mu.Lock()
	kept := s.sealed[seg.Source][:0]
	for _, o := range s.sealed[seg.Source] {
		if o.path != seg.path {
			kept = append(kept, o)
		}
	}
	if len(kept) == 0 {
		delete(s.sealed, seg.Source)
	} else {
		s.sealed[seg.Source] = kept
	}
	s.mu.Unlock()
	return syncDir(filepath.Dir(seg.path))
}

// Stats reports what the spool holds.
func (s *Spool) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statsLocked()
}

func (s *Spool) statsLocked() Stats {
	var st Stats
	add := func(seg Segment) {
		st.Bytes += seg.Bytes
		st.Events += int64(seg.Events)
		st.Files++
		if seg.Events > 0 && (st.Oldest.IsZero() || seg.Oldest.Before(st.Oldest)) {
			st.Oldest = seg.Oldest
		}
	}
	for _, a := range s.active {
		add(a.seg)
	}
	for _, segs := range s.sealed {
		for _, seg := range segs {
			add(seg)
		}
	}
	return st
}

// Close seals every active file and releases the lock. Nothing is lost: the
// next process to open the spool drains what it holds.
func (s *Spool) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	actives := make([]*active, 0, len(s.active))
	for _, a := range s.active {
		actives = append(actives, a)
	}
	s.active = map[string]*active{}
	s.mu.Unlock()
	var errs []error
	for _, a := range actives {
		if err := s.sealFile(a); err != nil {
			errs = append(errs, err)
		}
	}
	if err := unlockDir(s.lock); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("whspool: closing %s: %w", s.dir, err)
	}
	return nil
}

// encode is one record as it is written: its length, its checksum, its JSON.
func encode(rec Record) ([]byte, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("whspool: encoding a record: %w", err)
	}
	// A record longer than a read accepts would be acknowledged and then
	// read back as a damaged tail, so it is refused before it is written.
	if len(payload) > maxRecordBytes {
		return nil, fmt.Errorf("whspool: a record of %d bytes is larger than the %d a spool file holds", len(payload), maxRecordBytes)
	}
	var header [headerBytes]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(payload))) // #nosec G115 -- bounded by maxRecordBytes above
	binary.BigEndian.PutUint32(header[4:8], crc32.Checksum(payload, crcTable))
	return append(header[:], payload...), nil
}

// nextRecord reads the record at off: its payload and where the next one
// starts. ok is false at the end of the data, and at a record that is torn or
// damaged, which a sync never covered.
func nextRecord(data []byte, off int) (payload []byte, next int, ok bool) {
	if len(data)-off < headerBytes {
		return nil, off, false
	}
	n := int(binary.BigEndian.Uint32(data[off : off+4]))
	sum := binary.BigEndian.Uint32(data[off+4 : off+8])
	end := off + headerBytes + n
	if n > maxRecordBytes || end > len(data) {
		return nil, off, false
	}
	payload = data[off+headerBytes : end]
	if crc32.Checksum(payload, crcTable) != sum {
		return nil, off, false
	}
	return payload, end, true
}

// scan reads a file's whole records into seg -- its bytes, events and
// oldest event -- and cuts off whatever follows the last of them.
func scan(seg *Segment) error {
	data, err := os.ReadFile(seg.path) // #nosec G304 -- the path is the spool's own
	if err != nil {
		return fmt.Errorf(errReading, seg.path, err)
	}
	seg.Events, seg.Bytes, seg.Oldest = 0, 0, time.Time{}
	off := 0
	for {
		payload, next, ok := nextRecord(data, off)
		if !ok {
			break
		}
		var rec Record
		if json.Unmarshal(payload, &rec) != nil {
			break
		}
		if seg.Events == 0 && len(rec.Events) > 0 {
			seg.Oldest = rec.Events[0].ReceivedAt
		}
		seg.Events += len(rec.Events)
		off = next
	}
	seg.Bytes = int64(off)
	if off < len(data) {
		if err := os.Truncate(seg.path, int64(off)); err != nil {
			return fmt.Errorf("whspool: cutting the torn tail of %s: %w", seg.path, err)
		}
	}
	return nil
}

// syncDir makes a directory's entries durable: a file created or removed in
// it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- the spool's own directory
	if err != nil {
		return fmt.Errorf("whspool: opening %s: %w", dir, err)
	}
	defer d.Close() //nolint:errcheck // read-only handle
	if err := d.Sync(); err != nil && !syncDirUnsupported(err) {
		return fmt.Errorf("whspool: syncing %s: %w", dir, err)
	}
	return nil
}
