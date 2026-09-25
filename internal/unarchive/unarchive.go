// Package unarchive reads the members of a zip, gzip or gzipped tar archive as
// streams, so a caller can write each one somewhere without holding it (#1879).
//
// It knows nothing about where members go. It answers three questions: which
// members an archive holds and whether their names are safe to file, whether the
// archive is inside the limits an operator set, and what the bytes of each
// selected member are. Everything it refuses, it refuses before the first member
// is handed over, wherever the format lets it know in advance: a zip's central
// directory declares every name, size and flag up front, and a tar is read
// through once without writing anything before it is read again for its bytes.
// A gzip holds one member whose size is not known until it has been read, so
// its limits are enforced as it streams, and a refusal there leaves nothing
// behind because the one write it feeds is abandoned.
//
// The archive is read through a range function rather than an io.Reader, which
// is what lets a zip -- whose directory is at its end -- be opened in place in
// object storage without being downloaded first.
package unarchive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Format is the kind of archive a source is.
type Format string

// The formats Open reads.
const (
	FormatZip   Format = "zip"
	FormatGzip  Format = "gzip"
	FormatTarGz Format = "tar.gz"
)

// Default limits. They are sized for the case the feature exists for, an
// upstream delivering one CSV of about a gigabyte in a zip, with room to spare,
// while still stopping an archive built to expand without bound.
const (
	// DefaultMaxMemberBytes is the largest one member may be uncompressed.
	DefaultMaxMemberBytes = 2 << 30
	// DefaultMaxTotalBytes is the most the selected members may add up to.
	DefaultMaxTotalBytes = 4 << 30
	// DefaultMaxMembers is how many entries an archive may hold, directories
	// included. A zip's directory is parsed whole, so this is also what bounds
	// the memory opening one takes.
	DefaultMaxMembers = 10000
	// DefaultMaxRatio is the largest uncompressed-to-compressed ratio a member
	// may have. A CSV compresses around ten to one and a very repetitive one
	// a few hundred to one; an archive built to expand runs to millions.
	DefaultMaxRatio = 500
)

// Binary units a limit is described in.
const (
	kib = 1 << 10
	mib = kib << 10
	gib = mib << 10
)

// ratioFloor is the uncompressed size below which the ratio is not checked. A
// small file of repeated bytes compresses past any sensible ratio and is
// harmless; the ratio exists to stop expansion that costs something.
const ratioFloor = 1 << 20

// Limits bound what one extraction may expand to. A non-positive field takes
// its default. It is the resources.managed.extract configuration section as
// well, so the key a LimitError names is the key an operator sets.
type Limits struct {
	MaxMemberBytes int64 `yaml:"max_member_bytes"`
	MaxTotalBytes  int64 `yaml:"max_total_bytes"`
	MaxMembers     int   `yaml:"max_members"`
	MaxRatio       int64 `yaml:"max_ratio"`
}

// Normalized returns the limits with every non-positive field at its default.
func (l Limits) Normalized() Limits {
	if l.MaxMemberBytes <= 0 {
		l.MaxMemberBytes = DefaultMaxMemberBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if l.MaxMembers <= 0 {
		l.MaxMembers = DefaultMaxMembers
	}
	if l.MaxRatio <= 0 {
		l.MaxRatio = DefaultMaxRatio
	}
	return l
}

// The limit names a LimitError carries. They are the configuration keys an
// operator raises, so a refusal says which knob it was.
const (
	LimitMemberBytes = "max_member_bytes"
	LimitTotalBytes  = "max_total_bytes"
	LimitMembers     = "max_members"
	LimitRatio       = "max_ratio"
)

// LimitError is an archive past one of its limits.
type LimitError struct {
	// Limit is the configuration key that was passed.
	Limit string
	// Max is that limit's value, and Got what the archive reached.
	Max, Got int64
	// Member is the member that passed it, empty for a limit on the whole
	// archive.
	Member string
}

func (e *LimitError) Error() string {
	subject := "the archive"
	if e.Member != "" {
		subject = fmt.Sprintf("member %q", e.Member)
	}
	switch e.Limit {
	case LimitRatio:
		return fmt.Sprintf("%s expands more than %d times its compressed size (%s)", subject, e.Max, e.Limit)
	case LimitMembers:
		return fmt.Sprintf("%s holds %d entries, more than %d (%s)", subject, e.Got, e.Max, e.Limit)
	default:
		return fmt.Sprintf("%s is larger than %s uncompressed (%s)", subject, describeBytes(e.Max), e.Limit)
	}
}

// The refusals that are not limits. Each is wrapped with the member it is
// about.
var (
	// ErrUnsafeName is a member name that would leave the destination: an
	// absolute path, a drive letter, or a ".." segment.
	ErrUnsafeName = errors.New("unsafe member name")
	// ErrEncrypted is an encrypted member. Nothing here can decrypt one.
	ErrEncrypted = errors.New("encrypted member")
	// ErrUnsupported is a source that is none of the formats, or a zip member
	// compressed by a method other than stored or deflate.
	ErrUnsupported = errors.New("unsupported archive")
	// ErrCorrupt is an archive whose structure or bytes cannot be read.
	ErrCorrupt = errors.New("corrupt archive")
	// ErrNoMembers is a selection that matched no file in the archive.
	ErrNoMembers = errors.New("no member matches")
)

// RangeFunc reads length bytes of the source from offset.
type RangeFunc func(ctx context.Context, offset, length int64) ([]byte, error)

// Source is an archive to open: its name, which names a gzip's one member, its
// size, and how to read part of it.
type Source struct {
	Name  string
	Size  int64
	Fetch RangeFunc
}

// Member is one file an archive holds.
type Member struct {
	// Name is the name as the archive records it.
	Name string
	// Dirs are the directories the member is filed under, with empty and "."
	// segments removed, and Base is its file name.
	Dirs []string
	Base string
	// Size is the declared uncompressed size, or -1 where the format declares
	// none ahead of the bytes (gzip).
	Size int64
}

// Path is the member's cleaned slash-separated path.
func (m Member) Path() string {
	return path.Join(append(append([]string{}, m.Dirs...), m.Base)...)
}

// selection chooses members by a glob. A pattern with no slash matches a
// member's file name wherever it is filed, so "*.csv" finds every CSV; one with
// a slash matches the member's whole cleaned path. Empty selects every file.
type selection string

// validate refuses a malformed pattern before anything is read.
func (s selection) validate() error {
	if _, err := path.Match(string(s), ""); err != nil {
		return fmt.Errorf("members pattern %q is not a valid glob: %w", string(s), err)
	}
	return nil
}

func (s selection) matches(m Member) bool {
	if s == "" {
		return true
	}
	subject := m.Base
	if strings.Contains(string(s), separator) {
		subject = m.Path()
	}
	ok, _ := path.Match(string(s), subject) // validated before any member is matched
	return ok
}

// Archive is an opened archive whose selected members passed every check that
// can be made before their bytes are read.
type Archive struct {
	format  Format
	members []Member
	skipped []string
	extract func(ctx context.Context, fn MemberFunc) error
}

// MemberFunc receives one member's bytes. It must read r to its end: a member's
// checksum and size are verified at the end of its stream, and a failure there
// is returned by the read.
type MemberFunc func(m Member, r io.Reader) error

// Format is the kind of archive this is.
func (a *Archive) Format() Format { return a.format }

// Members are the selected members, in archive order.
func (a *Archive) Members() []Member { return a.members }

// Skipped are the entries that are not regular files -- links and devices --
// which nothing is written for. Directories are not listed: a member's path
// already carries them.
func (a *Archive) Skipped() []string { return a.skipped }

// Extract hands each selected member's bytes to fn in archive order. It stops
// at the first failure, which is fn's error or the member's own: a limit passed
// while streaming, a checksum that did not match, a truncated stream.
func (a *Archive) Extract(ctx context.Context, fn MemberFunc) error {
	return a.extract(ctx, fn)
}

// Open detects the source's format, reads what it declares, and refuses it when
// a name is unsafe, a member is encrypted, a limit is passed, or the members
// pattern matches nothing. The pattern is a glob: one with no slash matches a
// member's file name wherever it is filed, so "*.csv" finds every CSV, and one
// with a slash matches the member's whole cleaned path. Empty selects every
// file.
func Open(ctx context.Context, src Source, pattern string, lim Limits) (*Archive, error) {
	sel := selection(pattern)
	if err := sel.validate(); err != nil {
		return nil, err
	}
	lim = lim.Normalized()
	r := newBlockReader(ctx, src)
	head := make([]byte, len(zipMagic))
	if n, err := r.ReadAt(head, 0); n < len(gzipMagic) {
		return nil, fmt.Errorf("the file is %d bytes and holds no archive: %w: %w", src.Size, ErrUnsupported, err)
	}
	switch {
	case isZip(head):
		return openZip(r, src, sel, lim)
	case bytes.HasPrefix(head, gzipMagic):
		return openGzip(ctx, r, src, sel, lim)
	default:
		return nil, fmt.Errorf("the file is not a zip, gzip or gzipped tar archive: %w", ErrUnsupported)
	}
}

// separator divides a member path's segments.
const separator = "/"

// gzipMagic opens every gzip stream.
var gzipMagic = []byte{0x1f, 0x8b}

// splitName cleans a member name into its directories and file name, refusing
// one that would leave the destination. A backslash is taken as a separator,
// which is how archives written on Windows name their folders.
func splitName(name string) (dirs []string, base string, err error) {
	clean := strings.ReplaceAll(name, `\`, separator)
	if strings.HasPrefix(clean, separator) || hasDriveLetter(clean) {
		return nil, "", fmt.Errorf("member %q is an absolute path: %w", name, ErrUnsafeName)
	}
	var segs []string
	for s := range strings.SplitSeq(clean, separator) {
		switch s {
		case "", ".":
			continue
		case "..":
			return nil, "", fmt.Errorf("member %q climbs out of its folder with \"..\": %w", name, ErrUnsafeName)
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return nil, "", fmt.Errorf("member %q names no file: %w", name, ErrUnsafeName)
	}
	return segs[:len(segs)-1], segs[len(segs)-1], nil
}

func hasDriveLetter(name string) bool {
	return len(name) >= 2 && name[1] == ':' &&
		(('a' <= name[0] && name[0] <= 'z') || ('A' <= name[0] && name[0] <= 'Z'))
}

// budget tracks the selected members' running total against the limits.
type budget struct {
	lim   Limits
	total int64
}

// admit charges one selected member's declared size. compressed is the
// member's own compressed size, or negative where the format compresses the
// stream rather than the member (tar), whose ratio is checked on the stream.
func (b *budget) admit(m Member, compressed int64) error {
	if m.Size > b.lim.MaxMemberBytes {
		return &LimitError{Limit: LimitMemberBytes, Max: b.lim.MaxMemberBytes, Got: m.Size, Member: m.Name}
	}
	if compressed >= 0 && m.Size > ratioFloor && (compressed == 0 || m.Size/compressed > b.lim.MaxRatio) {
		return &LimitError{Limit: LimitRatio, Max: b.lim.MaxRatio, Got: ratioOf(m.Size, compressed), Member: m.Name}
	}
	b.total += m.Size
	if b.total > b.lim.MaxTotalBytes {
		return &LimitError{Limit: LimitTotalBytes, Max: b.lim.MaxTotalBytes, Got: b.total}
	}
	return nil
}

func ratioOf(uncompressed, compressed int64) int64 {
	if compressed <= 0 {
		return uncompressed
	}
	return uncompressed / compressed
}

// noMembers is the refusal of a selection that matched nothing, naming what the
// archive does hold so the caller can correct the pattern.
func noMembers(sel selection, seen []string) error {
	const shown = 20
	listing := seen
	if len(listing) > shown {
		listing = listing[:shown]
	}
	holds := "no files"
	if len(listing) > 0 {
		holds = strings.Join(listing, ", ")
		if len(seen) > shown {
			holds += fmt.Sprintf(" and %d more", len(seen)-shown)
		}
	}
	if sel == "" {
		return fmt.Errorf("the archive holds %s: %w", holds, ErrNoMembers)
	}
	return fmt.Errorf("members %q match nothing; the archive holds %s: %w", string(sel), holds, ErrNoMembers)
}

// describeBytes renders a limit in the largest whole binary unit.
func describeBytes(n int64) string {
	units := []struct {
		size int64
		name string
	}{{gib, "GiB"}, {mib, "MiB"}, {kib, "KiB"}}
	for _, u := range units {
		if n >= u.size && n%u.size == 0 {
			return fmt.Sprintf("%d %s", n/u.size, u.name)
		}
	}
	return fmt.Sprintf("%d bytes", n)
}
