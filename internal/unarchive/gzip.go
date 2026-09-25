package unarchive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// tarMagicAt is where a POSIX tar header carries its "ustar" magic, which is
// how a gzip is told to hold a tar rather than one file: the name a delivery
// arrives under is not something to trust with that.
const (
	tarMagicAt   = 257
	tarHeaderLen = 512
)

var tarMagic = []byte("ustar")

// openGzip opens a gzip, and reads it as a tar when what it decompresses to
// opens with a tar header.
func openGzip(ctx context.Context, r io.ReaderAt, src Source, sel selection, lim Limits) (*Archive, error) {
	gz, _, err := newGzip(r, src.Size)
	if err != nil {
		return nil, err
	}
	head, err := bufio.NewReaderSize(gz, tarHeaderLen).Peek(tarHeaderLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, readFailure(src.Name, err)
	}
	if len(head) >= tarMagicAt+len(tarMagic) && bytes.Equal(head[tarMagicAt:tarMagicAt+len(tarMagic)], tarMagic) {
		return openTarGz(ctx, r, src, sel, lim)
	}
	return openPlainGzip(r, src, sel, lim)
}

// newGzip opens a decompressor over the whole source, and the count of
// compressed bytes it has consumed, which the ratio is taken against.
func newGzip(r io.ReaderAt, size int64) (*gzip.Reader, *counter, error) {
	c := &counter{r: io.NewSectionReader(r, 0, size)}
	gz, err := gzip.NewReader(c)
	if err != nil {
		return nil, nil, fmt.Errorf("the gzip header cannot be read: %w: %w", ErrCorrupt, err)
	}
	return gz, c, nil
}

// openPlainGzip is a gzip of one file. The member is named after the archive
// without its .gz: the name inside a gzip header is optional, often absent, and
// written by whatever compressed it, where the archive's own name is the one
// its caller chose.
func openPlainGzip(r io.ReaderAt, src Source, sel selection, lim Limits) (*Archive, error) {
	name := gzipMemberName(src.Name)
	dirs, base, err := splitName(name)
	if err != nil {
		return nil, err
	}
	m := Member{Name: name, Dirs: dirs, Base: base, Size: -1}
	if !sel.matches(m) {
		return nil, noMembers(sel, []string{m.Path()})
	}
	a := &Archive{format: FormatGzip, members: []Member{m}}
	a.extract = func(ctx context.Context, fn MemberFunc) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extraction stopped: %w", err)
		}
		gz, c, err := newGzip(r, src.Size)
		if err != nil {
			return err
		}
		var total int64
		g := &guard{
			r: gz, member: name, max: lim.MaxMemberBytes, total: &total, maxTotal: lim.MaxTotalBytes,
			compressed: c, maxRatio: lim.MaxRatio,
		}
		return g.cause(fn(m, g))
	}
	return a, nil
}

func gzipMemberName(archive string) string {
	lower := strings.ToLower(archive)
	for _, ext := range []string{".gzip", ".gz"} {
		if strings.HasSuffix(lower, ext) && len(archive) > len(ext) {
			return archive[:len(archive)-len(ext)]
		}
	}
	return archive
}

// openTarGz reads a gzipped tar through once, checking every entry and the
// stream's expansion, before anything is extracted. A tar declares nothing up
// front, so this first pass is what gives it the guarantee a zip's directory
// gives: an archive that is going to be refused is refused before its first
// member is written. It costs a second decompression, which is cheap beside a
// partial extraction the caller has to discover.
func openTarGz(ctx context.Context, r io.ReaderAt, src Source, sel selection, lim Limits) (*Archive, error) {
	gz, c, err := newGzip(r, src.Size)
	if err != nil {
		return nil, err
	}
	var scanned int64
	stream := &guard{
		r: gz, member: src.Name, max: -1, total: &scanned, maxTotal: -1,
		compressed: c, maxRatio: lim.MaxRatio,
	}
	sc := &tarScan{
		archive: &Archive{format: FormatTarGz}, sel: sel, lim: lim,
		budget: budget{lim: lim}, selected: map[int]bool{},
	}
	tr := tar.NewReader(stream)
	for entry := 0; ; entry++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("extraction stopped: %w", err)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, stream.cause(readFailure(src.Name, err))
		}
		if err := sc.entry(entry, hdr); err != nil {
			return nil, err
		}
		// Bounded by the size the header declared, which tar enforces; the
		// stream beneath it is bounded by the ratio.
		if _, err := io.CopyN(io.Discard, tr, hdr.Size); err != nil {
			return nil, stream.cause(readFailure(hdr.Name, err))
		}
	}
	a := sc.archive
	if len(a.members) == 0 {
		return nil, noMembers(sel, sc.seen)
	}
	plan := tarPlan{r: r, src: src, members: a.members, selected: sc.selected, lim: lim}
	a.extract = plan.extract
	return a, nil
}

// tarScan is the first pass over a tar: what it holds, what is selected, and
// the running total the selection is charged against.
type tarScan struct {
	archive  *Archive
	sel      selection
	lim      Limits
	budget   budget
	selected map[int]bool
	seen     []string
}

// entry checks one header, recording it when it is selected.
func (sc *tarScan) entry(index int, hdr *tar.Header) error {
	if index+1 > sc.lim.MaxMembers {
		return &LimitError{Limit: LimitMembers, Max: int64(sc.lim.MaxMembers), Got: int64(index + 1)}
	}
	m, ok, err := tarMember(hdr, sc.archive)
	if err != nil || !ok {
		return err
	}
	sc.seen = append(sc.seen, m.Path())
	if !sc.sel.matches(m) {
		return nil
	}
	if err := sc.budget.admit(m, -1); err != nil {
		return err
	}
	sc.archive.members = append(sc.archive.members, m)
	sc.selected[index] = true
	return nil
}

// tarMember reads one header. ok is false for a directory and for a global
// PAX header (which `git archive` opens every tarball with), and for a link or
// device, which is recorded as skipped.
func tarMember(hdr *tar.Header, a *Archive) (Member, bool, error) {
	mode := hdr.FileInfo().Mode()
	if mode.IsDir() || hdr.Typeflag == tar.TypeXGlobalHeader {
		return Member{}, false, nil
	}
	dirs, base, err := splitName(hdr.Name)
	if err != nil {
		return Member{}, false, err
	}
	if !mode.IsRegular() {
		a.skipped = append(a.skipped, hdr.Name)
		return Member{}, false, nil
	}
	return Member{Name: hdr.Name, Dirs: dirs, Base: base, Size: hdr.Size}, true, nil
}

// tarPlan is what the second pass needs: the source, and which entries the
// first pass selected.
type tarPlan struct {
	r        io.ReaderAt
	src      Source
	members  []Member
	selected map[int]bool
	lim      Limits
}

func (p tarPlan) extract(ctx context.Context, fn MemberFunc) error {
	gz, _, err := newGzip(p.r, p.src.Size)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	next := 0
	for entry := 0; next < len(p.members); entry++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extraction stopped: %w", err)
		}
		hdr, err := tr.Next()
		if err != nil {
			return readFailure(p.src.Name, err)
		}
		if !p.selected[entry] {
			continue
		}
		g := &guard{r: tr, member: hdr.Name, max: p.lim.MaxMemberBytes, total: &total, maxTotal: p.lim.MaxTotalBytes}
		if err := g.cause(fn(p.members[next], g)); err != nil {
			return err
		}
		next++
	}
	return nil
}

// counter counts the bytes read through it.
type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err //nolint:wrapcheck // transparent pass-through of the wrapped reader's error
}

func isGzipCorrupt(err error) bool {
	return errors.Is(err, gzip.ErrChecksum) || errors.Is(err, gzip.ErrHeader) || errors.Is(err, tar.ErrHeader)
}
