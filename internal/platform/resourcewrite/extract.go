package resourcewrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// defaultExtractSummary labels the revision an extraction records when its
// caller named no change summary.
const defaultExtractSummary = "Content replaced by an archive extraction"

// ErrExtractRefused is an archive the extraction will not write: a name that
// leaves its folder, an encrypted member, a limit passed, a selection that
// matches nothing, an address already taken. Nothing has been written when it
// is returned from the checks, and the message says what to change.
var ErrExtractRefused = errors.New("archive extraction refused")

// RangeReader reads part of a stored object and reports its whole size. The
// managed-resource blob client implements it; an extraction reads the archive
// through it so a zip is opened where it is stored rather than downloaded.
type RangeReader interface {
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) (body []byte, size int64, err error)
}

// Extractor writes the members of an archive stored as a managed resource out
// as managed resources of their own (#1879).
//
// Each member lands through the Lander, so a member is exactly what an export
// landing at that address would have written: a create the first time, the next
// version after, and the tables registered over the file moved onto it. What it
// adds is the archive: which members, under which names, inside which limits.
type Extractor struct {
	lander *Lander
	ranges RangeReader
	limits unarchive.Limits
}

// ExtractorDeps is what an extractor is assembled from.
type ExtractorDeps struct {
	Lander *Lander
	Ranges RangeReader
	// Limits are resources.managed.extract; a non-positive field takes its
	// default.
	Limits unarchive.Limits
}

// NewExtractor builds the extractor, or nil when there is no lander to write
// through or no way to read part of an object.
func NewExtractor(d ExtractorDeps) *Extractor {
	if d.Lander == nil || d.Ranges == nil {
		return nil
	}
	return &Extractor{lander: d.Lander, ranges: d.Ranges, limits: d.Limits.Normalized()}
}

// Extraction asks for the members of an archive already stored as a managed
// resource to be written out as managed resources of their own (#1879).
type Extraction struct {
	// ArchiveID is the managed resource holding the archive.
	ArchiveID string
	// Scope, ScopeID and Path are the library and folder the members are filed
	// under; a member's own directories become folders beneath Path.
	Scope   string
	ScopeID string
	Path    string
	// Members is a glob choosing members. Empty takes every file.
	Members string
	// Filename, when set, is the name the one selected member is filed under
	// directly in Path, which gives a rolling delivery a stable address.
	Filename string
	// Replace records the next version of a file already at a member's
	// address, where false refuses the extraction before anything is written.
	Replace bool
	// Description and Tags label the files a create makes; a replacement
	// leaves the labels it finds. ChangeSummary is what a revision records.
	Description   string
	Tags          []string
	ChangeSummary string
}

// ExtractedMember is one member written: its name in the archive, and the
// resource it landed as.
type ExtractedMember struct {
	Member  string
	Landing toolkit.ResourceLanding
}

// Extracted is what an extraction wrote.
type Extracted struct {
	// Format is zip, gzip or tar.gz.
	Format string
	// Members are the members written, in archive order. On a failure part
	// way through they are the ones written before it.
	Members []ExtractedMember
	// Skipped are entries that are not regular files (links, devices), for
	// which nothing is written.
	Skipped []string
}

// memberPlan is one selected member and the address it will be written to.
type memberPlan struct {
	member unarchive.Member
	dest   toolkit.ResourceDestination
	plan   landing
}

// ExtractArchive reads the archive, plans an address for every selected member, and
// writes them in archive order.
//
// Everything that can be refused is refused before the first write: the
// caller's authority over the archive and over each destination, an address
// already taken without Replace, a member name no folder can hold, and every
// check the archive format allows ahead of its bytes. A failure while a member
// streams -- a checksum, a truncated stream, a gzip past its limit -- abandons
// that member's write, so nothing partial is stored, and the result carries the
// members written before it alongside the error.
func (e *Extractor) ExtractArchive(
	ctx context.Context, req Extraction, claims resource.Claims,
) (*Extracted, error) {
	archive, err := e.lander.w.Get(ctx, req.ArchiveID, claims)
	if err != nil {
		return nil, err
	}
	src := unarchive.Source{Name: archive.Filename, Size: archive.SizeBytes, Fetch: e.fetch(archive.S3Key)}
	a, err := unarchive.Open(ctx, src, strings.TrimSpace(req.Members), e.limits)
	if err != nil {
		return nil, refusal(archive, err)
	}
	plans, err := e.planMembers(ctx, archive, a.Members(), req, claims)
	if err != nil {
		return nil, err
	}
	out := &Extracted{
		Format: string(a.Format()), Members: []ExtractedMember{}, Skipped: a.Skipped(),
	}
	next := 0
	err = a.Extract(ctx, func(_ unarchive.Member, r io.Reader) error {
		p := plans[next]
		next++
		declared, body, err := contenttype.DetectStream("", r)
		if err != nil {
			return err //nolint:wrapcheck // the archive reader's own failure, which it reports itself
		}
		landed, err := e.lander.landPlanned(ctx, p.plan, landInput{
			dest: p.dest, content: body, contentType: declared, claims: claims,
		})
		if err != nil {
			return err
		}
		out.Members = append(out.Members, ExtractedMember{Member: p.member.Name, Landing: *landed})
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("extracting %s: %w", archive.URI, markRefusal(err))
	}
	return out, nil
}

// fetch reads the archive's object through the range reader.
func (e *Extractor) fetch(key string) unarchive.RangeFunc {
	bucket := e.lander.w.deps.S3Bucket
	return func(ctx context.Context, offset, length int64) ([]byte, error) {
		body, _, err := e.ranges.GetObjectRange(ctx, bucket, key, offset, length)
		return body, err //nolint:wrapcheck // the block reader names the offset it was reading
	}
}

// planMembers resolves every selected member's address and checks it can be
// written, before anything is.
func (e *Extractor) planMembers(
	ctx context.Context, archive *resource.Resource, members []unarchive.Member,
	req Extraction, claims resource.Claims,
) ([]memberPlan, error) {
	if strings.TrimSpace(req.Filename) != "" && len(members) != 1 {
		return nil, fmt.Errorf("filename names one file, and the selection matched %d members (%s); narrow "+
			"members to one, or omit filename to file each under its own name: %w",
			len(members), memberList(members), ErrExtractRefused)
	}
	plans := make([]memberPlan, 0, len(members))
	byURI := map[string]string{}
	for _, m := range members {
		dest, err := memberDestination(archive, m, req)
		if err != nil {
			return nil, err
		}
		p, err := e.lander.plan(ctx, dest, claims)
		if err != nil {
			return nil, fmt.Errorf("member %q: %w", m.Name, err)
		}
		if other, dup := byURI[p.uri]; dup {
			return nil, fmt.Errorf("members %q and %q would both be filed at %s: %w", other, m.Name, p.uri,
				ErrExtractRefused)
		}
		byURI[p.uri] = m.Name
		if p.existing != nil && !req.Replace {
			return nil, fmt.Errorf("member %q would be filed at %s, which already holds a file; pass "+
				"if_exists=replace to record it as that file's next version: %w", m.Name, p.uri, ErrExtractRefused)
		}
		plans = append(plans, memberPlan{member: m, dest: dest, plan: p})
	}
	return plans, nil
}

// memberDestination is the address and labels one member is written with: its
// directories as folders beneath the requested path, or the requested filename
// directly in that path.
func memberDestination(
	archive *resource.Resource, m unarchive.Member, req Extraction,
) (toolkit.ResourceDestination, error) {
	folders := []string{strings.TrimSpace(req.Path)}
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = m.Base
		for _, d := range m.Dirs {
			seg, ok := resource.FolderSegment(d)
			if !ok {
				return toolkit.ResourceDestination{}, fmt.Errorf("member %q is in a folder named %q, which has no "+
					"letters or digits to file it under; select members outside it, or pass filename for a "+
					"single member: %w", m.Name, d, ErrExtractRefused)
			}
			folders = append(folders, seg)
		}
	}
	folder := path.Join(folders...)
	if err := resource.ValidatePath(folder); err != nil {
		return toolkit.ResourceDestination{}, fmt.Errorf("member %q would be filed under %q: %w; extract to a "+
			"shallower path, or select members less deeply nested: %w", m.Name, folder, err, ErrExtractRefused)
	}
	if _, err := resource.SanitizeFilename(filename); err != nil {
		return toolkit.ResourceDestination{}, fmt.Errorf("member %q cannot be filed: %w; select members "+
			"without it: %w", m.Name, err, ErrExtractRefused)
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = fmt.Sprintf("Extracted from %s (member %s).", archive.URI, m.Name)
	}
	summary := strings.TrimSpace(req.ChangeSummary)
	if summary == "" {
		summary = fmt.Sprintf("%s: member %s of %s", defaultExtractSummary, m.Name, archive.URI)
	}
	return toolkit.ResourceDestination{
		Scope: req.Scope, ScopeID: req.ScopeID, Path: folder, Filename: filename,
		ChangeSummary: summary,
		DisplayName:   displayName(m.Base),
		Description:   description,
		Tags:          req.Tags,
	}, nil
}

// displayName is what a created member is listed under: its file name as the
// archive wrote it, within the length a display name may be.
func displayName(base string) string {
	r := []rune(base)
	if len(r) > resource.MaxDisplayNameLen {
		r = r[:resource.MaxDisplayNameLen]
	}
	return string(r)
}

// memberList names the selected members, the first few of them.
func memberList(members []unarchive.Member) string {
	const shown = 10
	names := make([]string, 0, shown)
	for i, m := range members {
		if i == shown {
			names = append(names, fmt.Sprintf("and %d more", len(members)-shown))
			break
		}
		names = append(names, m.Path())
	}
	return strings.Join(names, ", ")
}

// refusal names the archive in a refusal the archive reader made, and marks it
// as the caller's to act on.
func refusal(archive *resource.Resource, err error) error {
	return fmt.Errorf("archive %s cannot be extracted: %w", archive.URI, markRefusal(err))
}

// markRefusal wraps an archive's own refusal in ErrExtractRefused, leaving a
// storage or database failure as the fault it is.
func markRefusal(err error) error {
	var le *unarchive.LimitError
	for _, sentinel := range []error{
		unarchive.ErrUnsafeName, unarchive.ErrEncrypted, unarchive.ErrUnsupported,
		unarchive.ErrCorrupt, unarchive.ErrNoMembers,
	} {
		if errors.Is(err, sentinel) {
			return refused{err: err}
		}
	}
	if errors.As(err, &le) {
		return refused{err: err, hint: "raise resources.managed.extract." + le.Limit + " to allow it"}
	}
	return err
}

// refused marks an archive's own refusal as ErrExtractRefused without changing
// what it says, adding the knob that lifts it when there is one.
type refused struct {
	err  error
	hint string
}

func (r refused) Error() string {
	if r.hint == "" {
		return r.err.Error()
	}
	return r.err.Error() + "; " + r.hint
}

func (r refused) Unwrap() []error { return []error{ErrExtractRefused, r.err} }
