package unarchive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// memSource serves an archive held in memory through the range function, and
// counts the reads so a test can hold the block cache to its purpose.
type memSource struct {
	data  []byte
	reads int
}

func (m *memSource) source(name string) Source {
	return Source{Name: name, Size: int64(len(m.data)), Fetch: func(_ context.Context, off, length int64) ([]byte, error) {
		m.reads++
		return m.data[off : off+length], nil
	}}
}

type zipEntry struct {
	name   string
	body   []byte
	method uint16
	flags  uint16
	mode   string // "" regular, "symlink", "dir"
}

func buildZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.RegisterCompressor(12, func(out io.Writer) (io.WriteCloser, error) { return nopCloser{out}, nil })
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		switch e.mode {
		case "symlink":
			h.SetMode(0o777 | 1<<27) // fs.ModeSymlink
		case "dir":
			h.Name = strings.TrimSuffix(e.name, "/") + "/"
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type tarEntry struct {
	name     string
	body     []byte
	typeflag byte
}

func buildTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: flag, Mode: 0o644, Size: int64(len(e.body)), Format: tar.FormatPAX}
		if flag != tar.TypeReg {
			h.Size = 0
			h.Linkname = "target"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Name = "../inside-name-is-ignored.csv"
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// collect extracts every selected member and returns its bytes by path.
func collect(t *testing.T, a *Archive) (map[string]string, error) {
	t.Helper()
	got := map[string]string{}
	err := a.Extract(context.Background(), func(m Member, r io.Reader) error {
		b, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("consumer wrapped: %s", err.Error()) // deliberately not %w: the guard must win
		}
		got[m.Path()] = string(b)
		return nil
	})
	return got, err
}

func openTest(t *testing.T, data []byte, name, pattern string, lim Limits) (*Archive, error) {
	t.Helper()
	src := &memSource{data: data}
	return Open(context.Background(), src.source(name), pattern, lim)
}

func TestZipExtractsStoredAndDeflatedMembers(t *testing.T) {
	data := buildZip(t,
		zipEntry{name: "feed.csv", body: []byte("a,b\n1,2\n"), method: zip.Deflate},
		zipEntry{name: "reports/2026/notes.txt", body: []byte("stored"), method: zip.Store},
		zipEntry{name: "reports/", mode: "dir"},
		zipEntry{name: "link", body: []byte("target"), mode: "symlink"},
	)
	a, err := openTest(t, data, "delivery.zip", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Format() != FormatZip {
		t.Errorf("format = %s", a.Format())
	}
	got, err := collect(t, a)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"feed.csv": "a,b\n1,2\n", "reports/2026/notes.txt": "stored"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("extracted %v, want %v", got, want)
	}
	if fmt.Sprint(a.Skipped()) != "[link]" {
		t.Errorf("skipped = %v, want the symlink", a.Skipped())
	}
	m := a.Members()[1]
	if fmt.Sprint(m.Dirs) != "[reports 2026]" || m.Base != "notes.txt" || m.Size != 6 {
		t.Errorf("member = %+v", m)
	}
}

func TestSelectionMatchesBaseNameOrWholePath(t *testing.T) {
	data := buildZip(t,
		zipEntry{name: "a/one.csv", body: []byte("1")},
		zipEntry{name: "b/two.csv", body: []byte("2")},
		zipEntry{name: "readme.txt", body: []byte("r")},
	)
	for _, tc := range []struct {
		pattern string
		want    string
	}{
		{"*.csv", "[a/one.csv b/two.csv]"},
		{"b/*.csv", "[b/two.csv]"},
		{"", "[a/one.csv b/two.csv readme.txt]"},
	} {
		a, err := openTest(t, data, "x.zip", tc.pattern, Limits{})
		if err != nil {
			t.Fatalf("%q: %v", tc.pattern, err)
		}
		paths := make([]string, 0, len(a.Members()))
		for _, m := range a.Members() {
			paths = append(paths, m.Path())
		}
		if fmt.Sprint(paths) != tc.want {
			t.Errorf("%q selected %v, want %s", tc.pattern, paths, tc.want)
		}
	}

	_, err := openTest(t, data, "x.zip", "*.parquet", Limits{})
	if !errors.Is(err, ErrNoMembers) || !strings.Contains(err.Error(), `members "*.parquet" match nothing; the archive holds a/one.csv, b/two.csv, readme.txt`) {
		t.Errorf("a pattern matching nothing: %v", err)
	}
	if _, err := openTest(t, data, "x.zip", "[", Limits{}); err == nil || !strings.Contains(err.Error(), "not a valid glob") {
		t.Errorf("a malformed pattern: %v", err)
	}
}

func TestUnsafeNamesRefuseTheArchive(t *testing.T) {
	for _, name := range []string{"../escape.csv", "a/../../escape.csv", "/etc/passwd", `C:\evil.csv`, `a\..\..\x.csv`} {
		data := buildZip(t, zipEntry{name: "ok.csv", body: []byte("1")}, zipEntry{name: name, body: []byte("2")})
		_, err := openTest(t, data, "x.zip", "ok.csv", Limits{})
		if !errors.Is(err, ErrUnsafeName) {
			t.Errorf("%q: got %v, want ErrUnsafeName even though it is not selected", name, err)
		}
	}
	tgz := buildTarGz(t, tarEntry{name: "../escape.csv", body: []byte("x")})
	if _, err := openTest(t, tgz, "x.tgz", "", Limits{}); !errors.Is(err, ErrUnsafeName) {
		t.Errorf("a tar member climbing out: %v", err)
	}
}

func TestBackslashNamesAreFolders(t *testing.T) {
	data := buildZip(t, zipEntry{name: `exports\2026\feed.csv`, body: []byte("1")})
	a, err := openTest(t, data, "x.zip", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Members()[0].Path(); got != "exports/2026/feed.csv" {
		t.Errorf("path = %q", got)
	}
}

func TestEncryptedAndUnsupportedMembersAreRefused(t *testing.T) {
	enc := buildZip(t, zipEntry{name: "secret.csv", body: []byte("x"), method: zip.Store, flags: 0x1})
	if _, err := openTest(t, enc, "x.zip", "", Limits{}); !errors.Is(err, ErrEncrypted) || !strings.Contains(err.Error(), "secret.csv") {
		t.Errorf("an encrypted member: %v", err)
	}
	odd := buildZip(t, zipEntry{name: "bz.csv", body: []byte("x"), method: 12})
	if _, err := openTest(t, odd, "x.zip", "", Limits{}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "method 12") {
		t.Errorf("an unsupported method: %v", err)
	}
	if _, err := openTest(t, []byte("not an archive at all"), "x.zip", "", Limits{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a file that is no archive: %v", err)
	}
}

func TestLimitsRefuseBeforeExtraction(t *testing.T) {
	zeros := make([]byte, 4<<20)
	bomb := buildZip(t, zipEntry{name: "zeros.csv", body: zeros, method: zip.Deflate})
	_, err := openTest(t, bomb, "x.zip", "", Limits{})
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitRatio || le.Member != "zeros.csv" {
		t.Errorf("4 MiB of zeros deflated: %v, want the ratio limit", err)
	}

	two := buildZip(t, zipEntry{name: "a.csv", body: []byte("12345")}, zipEntry{name: "b.csv", body: []byte("12345")})
	if _, err := openTest(t, two, "x.zip", "", Limits{MaxMemberBytes: 4}); !errors.As(err, &le) || le.Limit != LimitMemberBytes {
		t.Errorf("a member past max_member_bytes: %v", err)
	}
	if _, err := openTest(t, two, "x.zip", "", Limits{MaxTotalBytes: 8}); !errors.As(err, &le) || le.Limit != LimitTotalBytes {
		t.Errorf("members past max_total_bytes: %v", err)
	}
	if _, err := openTest(t, two, "x.zip", "", Limits{MaxMembers: 1}); !errors.As(err, &le) || le.Limit != LimitMembers {
		t.Errorf("entries past max_members: %v", err)
	}
	// The unselected member does not count against the total.
	if _, err := openTest(t, two, "x.zip", "a.csv", Limits{MaxTotalBytes: 5}); err != nil {
		t.Errorf("one selected member under the total: %v", err)
	}

	tgz := buildTarGz(t, tarEntry{name: "a.csv", body: []byte("12345")}, tarEntry{name: "b.csv", body: []byte("12345")})
	if _, err := openTest(t, tgz, "x.tgz", "", Limits{MaxMembers: 1}); !errors.As(err, &le) || le.Limit != LimitMembers {
		t.Errorf("tar entries past max_members: %v", err)
	}
	if _, err := openTest(t, tgz, "x.tgz", "", Limits{MaxTotalBytes: 8}); !errors.As(err, &le) || le.Limit != LimitTotalBytes {
		t.Errorf("tar members past max_total_bytes: %v", err)
	}
	tarBomb := buildTarGz(t, tarEntry{name: "zeros.csv", body: zeros})
	if _, err := openTest(t, tarBomb, "x.tgz", "", Limits{}); !errors.As(err, &le) || le.Limit != LimitRatio {
		t.Errorf("a tar that expands past the ratio: %v", err)
	}
}

func TestLimitErrorsNameTheKnob(t *testing.T) {
	for _, tc := range []struct {
		err  LimitError
		want string
	}{
		{LimitError{Limit: LimitMemberBytes, Max: 2 << 30, Member: "big.csv"}, `member "big.csv" is larger than 2 GiB uncompressed (max_member_bytes)`},
		{LimitError{Limit: LimitTotalBytes, Max: 1000}, "the archive is larger than 1000 bytes uncompressed (max_total_bytes)"},
		{LimitError{Limit: LimitMembers, Max: 10, Got: 11}, "the archive holds 11 entries, more than 10 (max_members)"},
		{LimitError{Limit: LimitRatio, Max: 500, Member: "z"}, `member "z" expands more than 500 times its compressed size (max_ratio)`},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestCorruptArchivesFailExplicitly(t *testing.T) {
	body := bytes.Repeat([]byte("row,value\n"), 1000)
	data := buildZip(t, zipEntry{name: "a.csv", body: body, method: zip.Deflate})
	if _, err := openTest(t, data[:len(data)-10], "x.zip", "", Limits{}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a truncated zip: %v", err)
	}

	// Damage the stored member's bytes: the directory still reads, the CRC
	// does not match at the end of the stream.
	stored := buildZip(t, zipEntry{name: "a.csv", body: body, method: zip.Store})
	i := bytes.Index(stored, body)
	stored[i+100] ^= 0xff
	a, err := openTest(t, stored, "x.zip", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, a); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a member whose checksum does not match: %v, want ErrCorrupt", err)
	}

	tgz := buildTarGz(t, tarEntry{name: "a.csv", body: body})
	if _, err := openTest(t, tgz[:len(tgz)/2], "x.tgz", "", Limits{}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a truncated tar.gz: %v", err)
	}
	gz := buildGzip(t, body)
	a, err = openTest(t, gz[:len(gz)-6], "x.csv.gz", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, a); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a truncated gzip: %v", err)
	}
}

func TestGzipIsOneMemberNamedAfterTheArchive(t *testing.T) {
	a, err := openTest(t, buildGzip(t, []byte("a,b\n")), "delivery.csv.gz", "*.csv", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Format() != FormatGzip || a.Members()[0].Path() != "delivery.csv" || a.Members()[0].Size != -1 {
		t.Errorf("gzip opened as %s with %+v", a.Format(), a.Members())
	}
	got, err := collect(t, a)
	if err != nil || got["delivery.csv"] != "a,b\n" {
		t.Errorf("extracted %v, %v", got, err)
	}
	if _, err := openTest(t, buildGzip(t, []byte("x")), "delivery.csv.gz", "*.json", Limits{}); !errors.Is(err, ErrNoMembers) {
		t.Errorf("a gzip whose one member is not selected: %v", err)
	}
}

func TestGzipLimitsAreEnforcedWhileStreaming(t *testing.T) {
	var le *LimitError
	a, err := openTest(t, buildGzip(t, []byte("123456789")), "x.csv.gz", "", Limits{MaxMemberBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, a); !errors.As(err, &le) || le.Limit != LimitMemberBytes {
		t.Errorf("a gzip past max_member_bytes: %v", err)
	}
	a, err = openTest(t, buildGzip(t, make([]byte, 4<<20)), "x.csv.gz", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, a); !errors.As(err, &le) || le.Limit != LimitRatio {
		t.Errorf("a gzip of zeros: %v, want the ratio limit", err)
	}
}

func TestTarGzExtractsRegularFilesAndSkipsLinks(t *testing.T) {
	data := buildTarGz(t,
		tarEntry{name: "exports/", typeflag: tar.TypeDir},
		tarEntry{name: "exports/a.csv", body: []byte("a")},
		tarEntry{name: "exports/link.csv", typeflag: tar.TypeSymlink},
		tarEntry{name: "exports/b.csv", body: []byte("bb")},
		tarEntry{name: "notes.txt", body: []byte("n")},
	)
	a, err := openTest(t, data, "delivery.tar.gz", "*.csv", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Format() != FormatTarGz {
		t.Errorf("format = %s", a.Format())
	}
	got, err := collect(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "map[exports/a.csv:a exports/b.csv:bb]" {
		t.Errorf("extracted %v", got)
	}
	if fmt.Sprint(a.Skipped()) != "[exports/link.csv]" {
		t.Errorf("skipped = %v", a.Skipped())
	}
}

// TestAGlobalPAXHeaderIsNotReportedAsSkipped: `git archive` opens every
// tarball with one, and it is metadata rather than a link or device.
func TestAGlobalPAXHeaderIsNotReportedAsSkipped(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	require(tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header",
		PAXRecords: map[string]string{"comment": "abc123"}, Format: tar.FormatPAX,
	}))
	require(tw.WriteHeader(&tar.Header{Name: "a.csv", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}))
	_, err := tw.Write([]byte("1"))
	require(err)
	require(tw.Close())
	require(gz.Close())
	a, err := openTest(t, buf.Bytes(), "repo.tar.gz", "", Limits{})
	require(err)
	if len(a.Skipped()) != 0 || len(a.Members()) != 1 {
		t.Errorf("skipped %v, members %+v; want a.csv alone and nothing skipped", a.Skipped(), a.Members())
	}
}

func TestExtractStopsAtTheConsumersError(t *testing.T) {
	data := buildZip(t, zipEntry{name: "a.csv", body: []byte("1")}, zipEntry{name: "b.csv", body: []byte("2")})
	a, err := openTest(t, data, "x.zip", "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("destination refused")
	calls := 0
	err = a.Extract(context.Background(), func(Member, io.Reader) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("err = %v after %d calls, want the consumer's error after one", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Extract(ctx, func(Member, io.Reader) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled extraction: %v", err)
	}
}

// TestBlockReaderFetchesBlocksNotReads holds the cache to its purpose: a member
// many blocks long is read in about one range read per block, however small
// the decompressor's own reads are.
func TestBlockReaderFetchesBlocksNotReads(t *testing.T) {
	body := make([]byte, 3*blockSize)
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789,\n"
	for i := range body {
		body[i] = alphabet[(i*7919)%len(alphabet)]
	}
	src := &memSource{data: buildZip(t, zipEntry{name: "big.bin", body: body, method: zip.Store})}
	a, err := Open(context.Background(), src.source("x.zip"), "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collect(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["big.bin"]) != len(body) {
		t.Fatalf("read %d bytes, want %d", len(got["big.bin"]), len(body))
	}
	if src.reads > 8 {
		t.Errorf("%d range reads for a %d-block archive; the cache is not serving the decompressor", src.reads, 4)
	}
}

func TestBlockReaderRefusesAShortAnswer(t *testing.T) {
	src := Source{Name: "x", Size: 100, Fetch: func(context.Context, int64, int64) ([]byte, error) {
		return []byte("short"), nil
	}}
	r := newBlockReader(context.Background(), src)
	if _, err := r.ReadAt(make([]byte, 10), 0); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a short range answer: %v", err)
	}
	if _, err := r.ReadAt(make([]byte, 1), -1); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a negative offset: %v", err)
	}
	failing := Source{Name: "x", Size: 100, Fetch: func(context.Context, int64, int64) ([]byte, error) {
		return nil, errors.New("storage down")
	}}
	if _, err := newBlockReader(context.Background(), failing).ReadAt(make([]byte, 1), 0); err == nil ||
		!strings.Contains(err.Error(), "storage down") {
		t.Errorf("a failing fetch: %v", err)
	}
}

// TestZip64EntryCountIsRead builds an archive past the 16-bit entry count, so
// the count is read from the zip64 end record, and refuses it on max_members
// before the directory is parsed.
func TestZip64EntryCountIsRead(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := range 1 << 16 {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("f%d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	src := &memSource{data: buf.Bytes()}
	count, err := zipEntryCount(newBlockReader(context.Background(), src.source("x.zip")), int64(len(src.data)))
	if err != nil || count != 1<<16 {
		t.Fatalf("count = %d, %v", count, err)
	}
	var le *LimitError
	if _, err := Open(context.Background(), src.source("x.zip"), "", Limits{}); !errors.As(err, &le) ||
		le.Limit != LimitMembers || le.Got != 1<<16 {
		t.Errorf("an archive of 65536 entries under the 10000 default: %v", err)
	}
}

func TestNormalizedFillsDefaults(t *testing.T) {
	got := Limits{MaxMembers: 3}.Normalized()
	want := Limits{MaxMemberBytes: DefaultMaxMemberBytes, MaxTotalBytes: DefaultMaxTotalBytes, MaxMembers: 3, MaxRatio: DefaultMaxRatio}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
