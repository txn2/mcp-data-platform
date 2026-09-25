package unarchive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Zip structure the entry count is read from before the directory is parsed.
const (
	eocdLen         = 22
	eocdMaxComment  = math.MaxUint16
	zip64LocatorLen = 20
	zip64EOCDLen    = 56
	// methodAES is the method id WinZip's AES encryption records in place of
	// the real one.
	methodAES = 99
	// flagEncrypted is the general-purpose bit a traditionally encrypted
	// member sets.
	flagEncrypted = 0x1
)

var (
	zipMagic          = []byte("PK\x03\x04")
	zipEmptyMagic     = []byte("PK\x05\x06")
	zip64LocatorMagic = []byte("PK\x06\x07")
	zip64EOCDMagic    = []byte("PK\x06\x06")
)

func isZip(head []byte) bool {
	return bytes.HasPrefix(head, zipMagic) || bytes.HasPrefix(head, zipEmptyMagic)
}

// openZip reads the central directory and checks every entry before any member
// is extracted. The entry count is read from the end record first, because the
// standard reader holds the whole directory in memory: an archive declaring a
// million entries is refused before it costs that.
func openZip(r io.ReaderAt, src Source, sel selection, lim Limits) (*Archive, error) {
	entries, err := zipEntryCount(r, src.Size)
	if err != nil {
		return nil, err
	}
	if clampInt64(entries) > int64(lim.MaxMembers) {
		return nil, &LimitError{Limit: LimitMembers, Max: int64(lim.MaxMembers), Got: clampInt64(entries)}
	}
	zr, err := zip.NewReader(r, src.Size)
	// ErrInsecurePath comes with a usable reader; splitName is the check that
	// decides, and it names the member.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("the zip directory cannot be read: %w: %w", ErrCorrupt, err)
	}
	a := &Archive{format: FormatZip}
	files, seen, err := selectZipFiles(zr.File, a, sel, lim)
	if err != nil {
		return nil, err
	}
	if len(a.members) == 0 {
		return nil, noMembers(sel, seen)
	}
	a.extract = func(ctx context.Context, fn MemberFunc) error {
		return extractZip(ctx, files, a.members, lim, fn)
	}
	return a, nil
}

// selectZipFiles checks every directory entry, records the selected members on
// a, and returns their files beside the paths of every file the archive holds.
func selectZipFiles(entries []*zip.File, a *Archive, sel selection, lim Limits) (files []*zip.File, seen []string, err error) {
	b := budget{lim: lim}
	for _, f := range entries {
		m, ok, err := zipMember(f, a)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		seen = append(seen, m.Path())
		if !sel.matches(m) {
			continue
		}
		if err := checkZipMethod(f); err != nil {
			return nil, nil, err
		}
		if err := b.admit(m, clampInt64(f.CompressedSize64)); err != nil {
			return nil, nil, err
		}
		a.members = append(a.members, m)
		files = append(files, f)
	}
	return files, seen, nil
}

// zipMember reads one directory entry. ok is false for a directory, and for a
// link or device, which is recorded as skipped.
func zipMember(f *zip.File, a *Archive) (Member, bool, error) {
	mode := f.Mode()
	if mode.IsDir() {
		return Member{}, false, nil
	}
	dirs, base, err := splitName(f.Name)
	if err != nil {
		return Member{}, false, err
	}
	if !mode.IsRegular() {
		a.skipped = append(a.skipped, f.Name)
		return Member{}, false, nil
	}
	return Member{Name: f.Name, Dirs: dirs, Base: base, Size: clampInt64(f.UncompressedSize64)}, true, nil
}

// checkZipMethod refuses an encrypted member and one compressed by a method the
// standard library does not decompress.
func checkZipMethod(f *zip.File) error {
	if f.Flags&flagEncrypted != 0 || f.Method == methodAES {
		return fmt.Errorf("member %q is encrypted; extract it with its password elsewhere and upload the "+
			"result: %w", f.Name, ErrEncrypted)
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return fmt.Errorf("member %q is compressed with method %d; only stored and deflate members are read: %w",
			f.Name, f.Method, ErrUnsupported)
	}
	return nil
}

func extractZip(ctx context.Context, files []*zip.File, members []Member, lim Limits, fn MemberFunc) error {
	var total int64
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extraction stopped: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("opening member %q: %w: %w", f.Name, ErrCorrupt, err)
		}
		g := &guard{r: rc, member: f.Name, max: lim.MaxMemberBytes, total: &total, maxTotal: lim.MaxTotalBytes}
		err = fn(members[i], g)
		_ = rc.Close() // a read-only decompressor; nothing to flush
		if err := g.cause(err); err != nil {
			return err
		}
	}
	return nil
}

// zipEntryCount reads the number of directory entries the end record declares,
// following the zip64 locator when the 16-bit field is saturated.
func zipEntryCount(r io.ReaderAt, size int64) (uint64, error) {
	tailLen := min(size, int64(eocdLen+eocdMaxComment))
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, size-tailLen); err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("reading the end of the zip: %w", err)
	}
	at := bytes.LastIndex(tail, zipEmptyMagic)
	if at < 0 || len(tail)-at < eocdLen {
		return 0, fmt.Errorf("the zip has no end-of-directory record; it is truncated: %w", ErrCorrupt)
	}
	entries := uint64(binary.LittleEndian.Uint16(tail[at+10:]))
	if entries != math.MaxUint16 {
		return entries, nil
	}
	return zip64EntryCount(r, size, size-tailLen+int64(at))
}

// zip64EntryCount reads the entry count from the zip64 end record, found
// through the locator just before the end record at eocdAt.
func zip64EntryCount(r io.ReaderAt, size, eocdAt int64) (uint64, error) {
	locAt := eocdAt - zip64LocatorLen
	loc := make([]byte, zip64LocatorLen)
	if locAt < 0 {
		return 0, fmt.Errorf("the zip64 locator is missing: %w", ErrCorrupt)
	}
	if _, err := r.ReadAt(loc, locAt); err != nil || !bytes.HasPrefix(loc, zip64LocatorMagic) {
		return 0, fmt.Errorf("the zip64 locator is missing: %w", ErrCorrupt)
	}
	recAt := clampInt64(binary.LittleEndian.Uint64(loc[8:]))
	if recAt > size {
		return 0, fmt.Errorf("the zip64 end record is outside the file: %w", ErrCorrupt)
	}
	rec := make([]byte, zip64EOCDLen)
	if _, err := r.ReadAt(rec, recAt); err != nil || !bytes.HasPrefix(rec, zip64EOCDMagic) {
		return 0, fmt.Errorf("the zip64 end record is missing: %w", ErrCorrupt)
	}
	return binary.LittleEndian.Uint64(rec[32:]), nil
}

func clampInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// guard is a member's stream, bounded by the member and total limits whatever
// the archive declared, and remembering the first failure of its own so it is
// reported rather than whatever the consumer wrapped it in.
//
// A negative max or maxTotal is unbounded. compressed, when set, is the count
// of compressed bytes consumed beneath r, and the stream is refused once it
// expands past maxRatio times that.
type guard struct {
	r          io.Reader
	member     string
	n          int64
	max        int64
	total      *int64
	maxTotal   int64
	compressed *counter
	maxRatio   int64
	err        error
}

func (g *guard) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.r.Read(p)
	g.n += int64(n)
	*g.total += int64(n)
	switch {
	case g.max >= 0 && g.n > g.max:
		g.err = &LimitError{Limit: LimitMemberBytes, Max: g.max, Got: g.n, Member: g.member}
	case g.maxTotal >= 0 && *g.total > g.maxTotal:
		g.err = &LimitError{Limit: LimitTotalBytes, Max: g.maxTotal, Got: *g.total}
	case g.overRatio():
		g.err = &LimitError{Limit: LimitRatio, Max: g.maxRatio, Got: ratioOf(g.n, g.compressed.n), Member: g.member}
	case err != nil && !errors.Is(err, io.EOF):
		g.err = readFailure(g.member, err)
	}
	if g.err != nil {
		return n, g.err
	}
	return n, err //nolint:wrapcheck // io.EOF is returned as itself
}

// overRatio reports a stream that has expanded past its ratio, once it is past
// the size a ratio is worth checking at.
func (g *guard) overRatio() bool {
	return g.compressed != nil && g.n > ratioFloor && g.n/max(g.compressed.n, 1) > g.maxRatio
}

// cause settles what a member's extraction failed with: the guard's own
// failure when it had one, since that is the archive's and the consumer's error
// is only its echo; otherwise the consumer's.
func (g *guard) cause(consumer error) error {
	if g.err != nil {
		return g.err
	}
	return consumer
}

// readFailure names a decompression failure as corruption when it is one.
func readFailure(member string, err error) error {
	var le *LimitError
	if errors.As(err, &le) {
		return err
	}
	var flateErr flate.CorruptInputError
	if errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &flateErr) || isGzipCorrupt(err) {
		return fmt.Errorf("member %q: %w: %w", member, ErrCorrupt, err)
	}
	return fmt.Errorf("reading member %q: %w", member, err)
}
