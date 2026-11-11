package resourcewrite

// Extracting a bucket prefix (#2593): the objects under a prefix of a bucket on
// an S3 connection written out as managed resources of a folder, each at its
// path under the prefix, so a media folder of tens of thousands of files
// arrives from where it already sits, without passing through tool calls.
//
// Threat model. The boundary is the caller reaching a bucket on one named S3
// connection (internal/bucketsource holds the whole of it: the persona
// connection and tool rules every tool call is authorized under, s3_list and
// s3_object both held, the connection's
// bucket_prefix by the S3 toolkit's own check, the connection always named,
// names no folder can hold refused, and an object store failure reported by
// its code, never by the client's text, so no credential of the connection
// reaches a response, a log, an audit row or an error). A weaker persona can
// make it do nothing beyond s3_list and s3_object get on the same connection.
// What is stored is each object's bytes, written through the lander as the
// caller, which is the authority an archive extraction and an export landing
// write under: the folder must be one the caller may write. Every limit
// (resources.managed.extract) is checked from the listing before the first
// write, and every address is planned first, so a refusal writes nothing. The
// objects are read one at a time, each as a range of at most max_member_bytes
// and one byte, and what was read is added up against max_total_bytes, so an
// object replaced after the listing is refused rather than held whole.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// formatPrefix is what an extraction from a bucket prefix reports as its format.
const formatPrefix = "bucket prefix"

// PrefixExtraction asks for the objects under a bucket prefix to be written out
// as managed resources of a folder (#2593). The fields after Source are an
// archive extraction's.
type PrefixExtraction struct {
	Source        bucketsource.Source
	Scope         string
	ScopeID       string
	Path          string
	Replace       bool
	Description   string
	Tags          []string
	ChangeSummary string
	Index         string
}

// ExtractPrefix lists the prefix, plans an address for every object, and
// writes them in listing order, one object in memory at a time. On a failure
// part way through the result carries the objects written before it.
func (e *Extractor) ExtractPrefix(ctx context.Context, req PrefixExtraction, claims resource.Claims) (*Extracted, error) {
	ctx, op := opsobs.Start(ctx, opsobs.OpResourceExtract)
	out, err := e.landPrefix(ctx, req, claims)
	op.End(ctx, err)
	recordExtraction(ctx, out, err)
	return out, err
}

func (e *Extractor) landPrefix(ctx context.Context, req PrefixExtraction, claims resource.Claims) (*Extracted, error) {
	if e.buckets == nil {
		return nil, fmt.Errorf("this deployment cannot read a bucket: it has no S3 connections: %w", ErrExtractRefused)
	}
	b, err := e.buckets.Open(ctx, req.Source)
	if err != nil {
		return nil, sourceRefusal(err)
	}
	objects, err := b.List(ctx, bucketsource.Limits{
		MaxObjects: e.limits.MaxMembers, MaxObjectBytes: e.limits.MaxMemberBytes, MaxTotalBytes: e.limits.MaxTotalBytes,
		ObjectsKey:     "resources.managed.extract." + unarchive.LimitMembers,
		ObjectBytesKey: "resources.managed.extract." + unarchive.LimitMemberBytes,
		TotalBytesKey:  "resources.managed.extract." + unarchive.LimitTotalBytes,
	})
	if err != nil {
		return nil, sourceRefusal(err)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("there are no objects under %s: %w", b.Source(), ErrExtractRefused)
	}
	origin := &resource.Resource{URI: b.Source().String()}
	plans, err := e.planMembers(ctx, origin, membersOf(objects), req.asExtraction(), claims)
	if err != nil {
		return nil, err
	}
	out := &Extracted{Format: formatPrefix, Members: []ExtractedMember{}, Skipped: []string{}}
	for i, p := range plans {
		body, stored, err := b.Read(ctx, objects[i])
		if err != nil {
			return out, fmt.Errorf("extracting %s: %w", b.Source(), sourceRefusal(err))
		}
		declared, content, err := contenttype.DetectStream(stored, bytes.NewReader(body))
		if err != nil {
			return out, fmt.Errorf("reading %s: %w", objects[i].Key, err)
		}
		landed, err := e.lander.landPlanned(ctx, p.plan, landInput{dest: p.dest, content: content, contentType: declared, claims: claims})
		if err != nil {
			return out, fmt.Errorf("extracting %s: %w", b.Source(), err)
		}
		out.Members = append(out.Members, ExtractedMember{Member: objects[i].Path, Landing: *landed})
	}
	return out, nil
}

// asExtraction is the archive extraction the addresses are planned as.
func (req PrefixExtraction) asExtraction() Extraction {
	return Extraction{
		Scope: req.Scope, ScopeID: req.ScopeID, Path: req.Path, Replace: req.Replace,
		Description: req.Description, Tags: req.Tags, ChangeSummary: req.ChangeSummary, Index: req.Index,
	}
}

// membersOf is each object as the member an archive extraction plans: its
// directories under the prefix as folders beneath the target, and its name.
func membersOf(objects []bucketsource.Object) []unarchive.Member {
	out := make([]unarchive.Member, 0, len(objects))
	for _, o := range objects {
		segs := strings.Split(o.Path, "/")
		out = append(out, unarchive.Member{Name: o.Path, Dirs: segs[:len(segs)-1], Base: segs[len(segs)-1], Size: o.Size})
	}
	return out
}

// sourceRefusal marks a bucket source's refusal as the caller's to act on.
func sourceRefusal(err error) error {
	if errors.Is(err, bucketsource.ErrRefused) {
		return refused{err: err}
	}
	return err
}
