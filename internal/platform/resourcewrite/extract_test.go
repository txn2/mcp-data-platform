package resourcewrite_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// GetObjectRange serves a range of a stored object, as the real blob client
// does, so an archive is read where it is stored.
func (b *memBlobs) GetObjectRange(
	_ context.Context, bucket, key string, offset, length int64,
) (body []byte, size int64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[bucket+"/"+key]
	if !ok {
		return nil, 0, errors.New("NoSuchKey")
	}
	end := min(offset+length, int64(len(data)))
	return append([]byte(nil), data[offset:end]...), int64(len(data)), nil
}

type extractFixture struct {
	*landFixture
	extractor *resourcewrite.Extractor
	archives  int
}

func newExtractFixture(t *testing.T, lim unarchive.Limits, uploadCeiling int64) *extractFixture {
	t.Helper()
	lf := &landFixture{fixture: newFixture(t)}
	lf.lander = resourcewrite.NewLander(resourcewrite.LanderDeps{
		Writer: lf.writer, MaxUploadBytes: uploadCeiling,
		Claims: func(context.Context) resource.Claims { return analyst() },
	})
	lf.lander.SetTableFollower(func(_ context.Context, _ string, version int) []string {
		lf.followed = append(lf.followed, version)
		return lf.tables
	})
	x := resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{Lander: lf.lander, Ranges: lf.blobs, Limits: lim})
	require.NotNil(t, x)
	return &extractFixture{landFixture: lf, extractor: x}
}

type member struct {
	name string
	body string
}

func zipOf(t *testing.T, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, m := range members {
		f, err := w.Create(m.name)
		require.NoError(t, err)
		_, err = f.Write([]byte(m.body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// storeArchive files an archive as a managed resource in the analyst's library.
func (xf *extractFixture) storeArchive(t *testing.T, data []byte) *resource.Resource {
	t.Helper()
	xf.archives++
	res, err := xf.writer.Create(context.Background(), resource.NewResource{
		Scope: resource.ScopeUser, ScopeID: authorSub, Path: "pipelines",
		Filename:    fmt.Sprintf("delivery-%d.zip", xf.archives),
		DisplayName: "Delivery", Description: "Monthly delivery", Tags: []string{},
		Content: bytes.NewReader(data), MIMEType: "application/zip",
	}, analyst())
	require.NoError(t, err)
	return res
}

func (xf *extractFixture) extract(t *testing.T, req resourcewrite.Extraction) (*resourcewrite.Extracted, error) {
	t.Helper()
	return xf.extractor.ExtractArchive(context.Background(), req, analyst()) //nolint:wrapcheck // the test reads the extractor's own error
}

func (xf *extractFixture) contentAt(t *testing.T, uri string) string {
	t.Helper()
	res, err := xf.store.GetByURI(context.Background(), uri)
	require.NoError(t, err)
	body, _, err := xf.blobs.GetObject(context.Background(), testBucket, res.S3Key)
	require.NoError(t, err)
	return string(body)
}

func TestExtractWritesEachMemberUnderItsFolders(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	archive := xf.storeArchive(t, zipOf(t,
		member{"feed.csv", "id,name\n1,a\n"},
		member{"2026/Notes File.csv", "x\n1\n"},
	))
	out, err := xf.extract(t, resourcewrite.Extraction{ArchiveID: archive.ID, Path: "pipelines/staging"})
	require.NoError(t, err)
	assert.Equal(t, "zip", out.Format)
	require.Len(t, out.Members, 2)

	first := out.Members[0]
	assert.Equal(t, "feed.csv", first.Member)
	assert.Equal(t, "pipelines/staging", first.Landing.Path)
	assert.Equal(t, "text/csv", first.Landing.ContentType, "a .csv member is stored as CSV, as a create would store it")
	assert.Equal(t, int64(len("id,name\n1,a\n")), first.Landing.SizeBytes)
	assert.True(t, first.Landing.Created)
	assert.Equal(t, 1, first.Landing.Version)
	assert.Equal(t, "id,name\n1,a\n", xf.contentAt(t, first.Landing.URI))

	second := out.Members[1]
	assert.Equal(t, "pipelines/staging/f-2026", second.Landing.Path, "a year folder is filed the way the bulk uploader files it")
	assert.Equal(t, "notes-file.csv", second.Landing.Filename)
	res, err := xf.store.GetByURI(context.Background(), second.Landing.URI)
	require.NoError(t, err)
	assert.Equal(t, "Notes File.csv", res.DisplayName)
	assert.Contains(t, res.Description, archive.URI, "a created member says which archive it came from")
}

// TestARollingExtractionRecordsTheNextVersion is the contract a monthly
// delivery rests on: a fixed filename and replace record the next version of
// one file, and a table following it moves.
func TestARollingExtractionRecordsTheNextVersion(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	req := func(id string) resourcewrite.Extraction {
		return resourcewrite.Extraction{
			ArchiveID: id, Path: "pipelines/staging", Members: "*.csv", Filename: "delivery.csv", Replace: true,
		}
	}
	september := xf.storeArchive(t, zipOf(t, member{"export_2026_09.csv", "month\n9\n"}, member{"readme.txt", "r"}))
	out, err := xf.extract(t, req(september.ID))
	require.NoError(t, err)
	require.Len(t, out.Members, 1)
	first := out.Members[0]
	assert.Equal(t, "delivery.csv", first.Landing.Filename)
	assert.True(t, first.Landing.Created)

	xf.tables = []string{"Table acme.delivery followed onto version 2."}
	october := xf.storeArchive(t, zipOf(t, member{"export_2026_10.csv", "month\n10\n"}))
	out, err = xf.extract(t, req(october.ID))
	require.NoError(t, err)
	second := out.Members[0]
	assert.Equal(t, first.Landing.ResourceID, second.Landing.ResourceID, "the same file, not a second one")
	assert.Equal(t, first.Landing.URI, second.Landing.URI)
	assert.False(t, second.Landing.Created)
	assert.Equal(t, 2, second.Landing.Version)
	assert.Equal(t, []int{2}, xf.followed, "the tables over the file were moved to the new version")
	assert.Equal(t, xf.tables, second.Landing.TableChanges)
	assert.Equal(t, "month\n10\n", xf.contentAt(t, second.Landing.URI))
}

func TestExtractRefusesBeforeWritingAnything(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{MaxRatio: 500}, 0)
	taken := xf.storeArchive(t, zipOf(t, member{"a.csv", "1"}))
	_, err := xf.extract(t, resourcewrite.Extraction{ArchiveID: taken.ID, Path: "staging"})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		archive []byte
		req     resourcewrite.Extraction
		want    string
	}{
		"an address already taken": {
			zipOf(t, member{"b.csv", "2"}, member{"a.csv", "1"}),
			resourcewrite.Extraction{Path: "staging"},
			"already holds a file; pass if_exists=replace",
		},
		"filename with several members": {
			zipOf(t, member{"b.csv", "2"}, member{"c.csv", "3"}),
			resourcewrite.Extraction{Path: "fresh", Filename: "one.csv"},
			"the selection matched 2 members (b.csv, c.csv)",
		},
		"a zip-slip name": {
			zipOf(t, member{"b.csv", "2"}, member{"../../escape.csv", "x"}),
			resourcewrite.Extraction{Path: "fresh"},
			`climbs out of its folder`,
		},
		"a ratio past the limit": {
			zipOf(t, member{"b.csv", "2"}, member{"zeros.csv", strings.Repeat("0", 4<<20)}),
			resourcewrite.Extraction{Path: "fresh"},
			"resources.managed.extract.max_ratio",
		},
		"two members at one address": {
			zipOf(t, member{"x/b.csv", "2"}, member{"X/B.csv", "3"}),
			resourcewrite.Extraction{Path: "fresh"},
			"would both be filed at",
		},
		"a folder with nothing to name it": {
			zipOf(t, member{"b.csv", "2"}, member{"日本/c.csv", "3"}),
			resourcewrite.Extraction{Path: "fresh"},
			"no letters or digits",
		},
		"a folder chain too deep": {
			zipOf(t, member{"b.csv", "2"}, member{"a/b/c/d/e/f/g/h.csv", "3"}),
			resourcewrite.Extraction{Path: "fresh/deeper"},
			"extract to a shallower path",
		},
		"a denied extension": {
			zipOf(t, member{"b.csv", "2"}, member{"setup.exe", "3"}),
			resourcewrite.Extraction{Path: "fresh"},
			`is not allowed`,
		},
		"a pattern matching nothing": {
			zipOf(t, member{"b.csv", "2"}),
			resourcewrite.Extraction{Path: "fresh", Members: "*.parquet"},
			`members "*.parquet" match nothing; the archive holds b.csv`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			archive := xf.storeArchive(t, tc.archive)
			t.Cleanup(func() { _, _ = xf.writer.Delete(context.Background(), archive.ID, analyst()) })
			before := len(xf.store.resources)
			tc.req.ArchiveID = archive.ID
			out, err := xf.extract(t, tc.req)
			require.Error(t, err)
			assert.Nil(t, out)
			assert.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
			assert.Contains(t, err.Error(), tc.want)
			assert.Len(t, xf.store.resources, before, "nothing was written")
		})
	}
}

// TestAFailureMidStreamReportsWhatWasWritten damages the second member's
// bytes: the first is written, the second's write is abandoned, and the
// result says which is which.
func TestAFailureMidStreamReportsWhatWasWritten(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	second := strings.Repeat("second,member\n", 200)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, m := range []member{{"a.csv", "first\n"}, {"b.csv", second}} {
		f, err := w.CreateHeader(&zip.FileHeader{Name: m.name, Method: zip.Store})
		require.NoError(t, err)
		_, err = f.Write([]byte(m.body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	data := buf.Bytes()
	data[bytes.Index(data, []byte(second))+50] ^= 0xff
	archive := xf.storeArchive(t, data)

	out, err := xf.extract(t, resourcewrite.Extraction{ArchiveID: archive.ID, Path: "staging"})
	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
	assert.ErrorIs(t, err, unarchive.ErrCorrupt)
	assert.Contains(t, err.Error(), `member "b.csv"`)
	require.NotNil(t, out)
	require.Len(t, out.Members, 1, "the member written before the failure is reported")
	assert.Equal(t, "a.csv", out.Members[0].Member)
	_, err = xf.store.GetByURI(context.Background(), strings.Replace(out.Members[0].Landing.URI, "a.csv", "b.csv", 1))
	assert.Error(t, err, "the damaged member was not stored")
}

// TestMembersAreBoundByTheExtractLimitsNotTheUploadCeiling holds the decision
// the ticket turns on: a member far past the upload form's ceiling extracts,
// because extraction is bounded by its own limits.
func TestMembersAreBoundByTheExtractLimitsNotTheUploadCeiling(t *testing.T) {
	body := strings.Repeat("id,value,label\n", 100)
	xf := newExtractFixture(t, unarchive.Limits{}, 64)
	archive := xf.storeArchive(t, zipOf(t, member{"big.csv", body}))
	out, err := xf.extract(t, resourcewrite.Extraction{ArchiveID: archive.ID, Path: "staging"})
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), out.Members[0].Landing.SizeBytes)

	tight := newExtractFixture(t, unarchive.Limits{MaxMemberBytes: 64}, 0)
	archive = tight.storeArchive(t, zipOf(t, member{"big.csv", body}))
	_, err = tight.extract(t, resourcewrite.Extraction{ArchiveID: archive.ID, Path: "staging"})
	require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
	assert.Contains(t, err.Error(), "resources.managed.extract.max_member_bytes")
}

func TestExtractNeedsAnArchiveTheCallerCanSee(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	archive := xf.storeArchive(t, zipOf(t, member{"a.csv", "1"}))
	stranger := resource.Claims{Sub: "someone-else", Email: "else@example.com"}
	_, err := xf.extractor.ExtractArchive(context.Background(),
		resourcewrite.Extraction{ArchiveID: archive.ID, Path: "staging"}, stranger)
	assert.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)

	notArchive, err := xf.writer.Create(context.Background(), resource.NewResource{
		Scope: resource.ScopeUser, ScopeID: authorSub, Path: "pipelines", Filename: "plain.csv",
		DisplayName: "Plain", Description: "Not an archive", Tags: []string{},
		Content: strings.NewReader("a,b\n1,2\n"), MIMEType: "text/csv",
	}, analyst())
	require.NoError(t, err)
	_, err = xf.extract(t, resourcewrite.Extraction{ArchiveID: notArchive.ID, Path: "staging"})
	require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
	assert.Contains(t, err.Error(), "not a zip, gzip or gzipped tar archive")
}

func TestExtractReportsAStorageFailureAsItself(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	archive := xf.storeArchive(t, zipOf(t, member{"a.csv", "1"}))
	xf.blobs.putErr = errors.New("storage refused the object")
	out, err := xf.extract(t, resourcewrite.Extraction{ArchiveID: archive.ID, Path: "staging"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, resourcewrite.ErrExtractRefused, "a storage fault is not the caller's to fix")
	assert.Contains(t, err.Error(), "storage backend did not accept the file")
	assert.Empty(t, out.Members)
}

func TestNewExtractorNeedsALanderAndRanges(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	assert.Nil(t, resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{Ranges: xf.blobs}))
	assert.Nil(t, resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{Lander: xf.lander}))
}

func TestExtractLabelsAndSummaries(t *testing.T) {
	xf := newExtractFixture(t, unarchive.Limits{}, 0)
	archive := xf.storeArchive(t, zipOf(t, member{"a.csv", "1"}))
	out, err := xf.extract(t, resourcewrite.Extraction{
		ArchiveID: archive.ID, Path: "staging", Description: "Monthly feed", Tags: []string{"feed"},
	})
	require.NoError(t, err)
	res, err := xf.store.GetByURI(context.Background(), out.Members[0].Landing.URI)
	require.NoError(t, err)
	assert.Equal(t, "Monthly feed", res.Description)
	assert.Equal(t, []string{"feed"}, res.Tags)

	again := xf.storeArchive(t, zipOf(t, member{"a.csv", "2"}))
	_, err = xf.extract(t, resourcewrite.Extraction{ArchiveID: again.ID, Path: "staging", Replace: true})
	require.NoError(t, err)
	versions, err := xf.store.ListVersions(context.Background(), out.Members[0].Landing.ResourceID)
	require.NoError(t, err)
	summaries := make([]string, 0, len(versions))
	for _, v := range versions {
		summaries = append(summaries, v.ChangeSummary)
	}
	assert.Contains(t, fmt.Sprint(summaries), "member a.csv of "+again.URI)
}
