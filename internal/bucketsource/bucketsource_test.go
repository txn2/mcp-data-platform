package bucketsource

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// fakeClient is a bucket of objects listed a page at a time.
type fakeClient struct {
	keys     []string
	sizes    map[string]int64
	bodies   map[string]string
	page     int
	listErr  error
	getErr   error
	listed   []string
	prefixes []string
	lengths  []int64
}

func (f *fakeClient) ListObjects(_ context.Context, _, prefix, _ string, _ int32, token string) (*s3client.ListObjectsOutput, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	f.prefixes = append(f.prefixes, prefix)
	if f.listErr != nil {
		return nil, f.listErr
	}
	start := 0
	if token != "" {
		start, _ = strconv.Atoi(token)
	}
	size := f.page
	if size <= 0 {
		size = len(f.keys)
	}
	end := min(start+size, len(f.keys))
	out := &s3client.ListObjectsOutput{}
	for _, k := range f.keys[start:end] {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		out.Objects = append(out.Objects, s3client.ObjectInfo{Key: k, Size: f.sizes[k]})
		f.listed = append(f.listed, k)
	}
	if end < len(f.keys) {
		out.IsTruncated, out.NextContinueToken = true, strconv.Itoa(end)
	}
	return out, nil
}

// GetObjectRange answers as the store does: at most length bytes from offset,
// and a zero-byte object refused as a range no byte can start.
func (f *fakeClient) GetObjectRange(_ context.Context, _, key string, offset, length int64) (*s3client.ObjectContent, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	f.lengths = append(f.lengths, length)
	if f.getErr != nil {
		return nil, f.getErr
	}
	body := f.bodies[key]
	if body == "" {
		return nil, fmt.Errorf("failed to get object range: %w", s3client.ErrRangeNotSatisfiable)
	}
	end := min(offset+length, int64(len(body)))
	return &s3client.ObjectContent{Key: key, Body: []byte(body[offset:end]), ContentType: "text/html", Size: int64(len(body))}, nil
}

type fakeClients struct {
	client *fakeClient
	err    error
	asked  []string
}

func (f *fakeClients) ReadClient(connection, bucket string) (Client, error) {
	f.asked = append(f.asked, connection+"/"+bucket)
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

// codedError is an object store failure carrying a code, with text that names
// the endpoint the client was configured with.
type codedError struct{}

func (codedError) Error() string {
	return "operation error S3: GetObject, https://key:secret@minio.internal:9000 AccessDenied"
}
func (codedError) ErrorCode() string { return "AccessDenied" }

// withMax is the limits with one bound set.
func withMax(l Limits, set func(*Limits)) Limits {
	set(&l)
	return l
}

func withPersona(name string) context.Context {
	pc := middleware.NewPlatformContext("")
	pc.PersonaName = name
	return middleware.WithPlatformContext(context.Background(), pc)
}

func site() *fakeClient {
	return &fakeClient{
		keys:   []string{"exports/site/", "exports/site/index.html", "exports/site/css/site.css", "exports/sitemap.xml"},
		sizes:  map[string]int64{"exports/site/index.html": 10, "exports/site/css/site.css": 20, "exports/sitemap.xml": 5},
		bodies: map[string]string{"exports/site/index.html": "<h1>hi</h1>", "exports/site/css/site.css": "body{}"},
	}
}

func TestOpen_RefusesWhatTheCallerCannotRead(t *testing.T) {
	clients := &fakeClients{client: site()}
	a := &Access{Clients: clients, Allowed: func(persona, connection string) bool { return persona == "web" && connection == "assets" }}
	ctx := withPersona("web")
	for name, tc := range map[string]struct {
		ctx  context.Context
		src  Source
		want string
	}{
		"no connection":            {ctx, Source{Bucket: "b"}, "name the S3 connection"},
		"no bucket":                {ctx, Source{Connection: "assets"}, "name the bucket"},
		"a parent in the prefix":   {ctx, Source{Connection: "assets", Bucket: "b", Prefix: "a/../b"}, "not a path"},
		"a connection not theirs":  {ctx, Source{Connection: "finance", Bucket: "b"}, "not one your persona may use"},
		"a caller with no persona": {context.Background(), Source{Connection: "assets", Bucket: "b"}, "not one your persona may use"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.Open(tc.ctx, tc.src)
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "bucket source refused", "a refusal is its own sentence")
		})
	}
	assert.Empty(t, clients.asked, "nothing was opened for a refused caller")

	// The tools a read stands in for (#2593): a persona allowed the
	// connection still holds s3_list and s3_object, or reads nothing.
	tools := map[string][]string{"web": {"s3_list", "s3_object"}, "lister": {"s3_list"}}
	a.Allowed = func(string, string) bool { return true }
	a.ToolAllowed = func(persona, tool string) bool { return slices.Contains(tools[persona], tool) }
	_, err := a.Open(withPersona("web"), Source{Connection: "assets", Bucket: "b"})
	require.NoError(t, err)
	clients.asked = nil
	_, err = a.Open(withPersona("lister"), Source{Connection: "assets", Bucket: "b"})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "may not use s3_object")
	_, err = a.Open(withPersona("nobody"), Source{Connection: "assets", Bucket: "b"})
	assert.Contains(t, err.Error(), "may not use s3_list", "the first tool missing is named")
	assert.Empty(t, clients.asked, "nothing was opened for a persona without the tools")
	a.ToolAllowed, a.Allowed = nil, func(persona, connection string) bool { return persona == "web" && connection == "assets" }

	clients.asked = nil
	clients.err = errors.New(`connection "assets" is limited to buckets whose name starts with "web-": bucket "b" is outside it`)
	_, err = a.Open(ctx, Source{Connection: "assets", Bucket: "b"})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "limited to buckets", "the toolkit's own bucket_prefix refusal")

	_, err = (*Access)(nil).Open(ctx, Source{Connection: "assets", Bucket: "b"})
	require.ErrorIs(t, err, ErrRefused)
}

func TestList_ObjectsUnderThePrefixAtTheirPaths(t *testing.T) {
	client := site()
	client.page = 2
	a := &Access{Clients: &fakeClients{client: client}}
	b, err := a.Open(context.Background(), Source{Connection: "assets", Bucket: "b", Prefix: "/exports/site"})
	require.NoError(t, err)
	assert.Equal(t, "exports/site/", b.Source().Prefix, "a prefix is a folder, so it never also matches sitemap.xml")
	got, err := b.List(context.Background(), Limits{})
	require.NoError(t, err)
	assert.Equal(t, []Object{{Key: "exports/site/index.html", Path: "index.html", Size: 10}, {Key: "exports/site/css/site.css", Path: "css/site.css", Size: 20}}, got,
		"the folder marker is passed over, and every page is read")
	assert.Equal(t, "s3://b/exports/site/ on connection assets", b.Source().String())

	body, contentType, err := b.Read(context.Background(), got[0])
	require.NoError(t, err)
	assert.Equal(t, "<h1>hi</h1>", string(body))
	assert.Equal(t, "text/html", contentType, "the type the object was stored with")
	assert.Equal(t, []int64{math.MaxInt64}, client.lengths, "a listing with no limit reads the whole object")
}

// A read is held to the listing's limits whatever the object is now: it asks
// for at most the limit and one byte more, refuses an object that grew past
// it, and refuses once what the reads took adds up past the total.
func TestRead_IsHeldToTheListingsLimits(t *testing.T) {
	client := site()
	b, err := (&Access{Clients: &fakeClients{client: client}}).Open(context.Background(), Source{Connection: "c", Bucket: "b", Prefix: "exports/site/"})
	require.NoError(t, err)
	lim := Limits{MaxObjectBytes: 20, MaxTotalBytes: 30, ObjectBytesKey: "x.file_bytes", TotalBytesKey: "x.total"}
	got, err := b.List(context.Background(), lim)
	require.NoError(t, err)

	body, _, err := b.Read(context.Background(), got[0])
	require.NoError(t, err)
	assert.Equal(t, "<h1>hi</h1>", string(body))
	assert.Equal(t, []int64{21}, client.lengths, "the limit and one byte, never the whole object")

	client.bodies["exports/site/css/site.css"] = strings.Repeat("a", 4096)
	_, _, err = b.Read(context.Background(), got[1])
	require.ErrorIs(t, err, ErrRefused, "an object replaced by a larger one after the listing")
	assert.Contains(t, err.Error(), "more than the 20 bytes x.file_bytes allows")

	client.bodies["exports/site/css/site.css"] = strings.Repeat("b", 20)
	_, _, err = b.Read(context.Background(), got[1])
	require.ErrorIs(t, err, ErrRefused, "11 and 20 bytes pass the 30 the reads may add up to, though each is inside its own limit")
	assert.Contains(t, err.Error(), "more than the 30 bytes x.total allows")
}

// A zero-byte object has no byte a range can start at; it reads as empty.
func TestRead_AZeroByteObjectIsEmpty(t *testing.T) {
	client := &fakeClient{keys: []string{"p/empty.txt"}, sizes: map[string]int64{}, bodies: map[string]string{}}
	b, err := (&Access{Clients: &fakeClients{client: client}}).Open(context.Background(), Source{Connection: "c", Bucket: "b", Prefix: "p"})
	require.NoError(t, err)
	got, err := b.List(context.Background(), Limits{MaxObjectBytes: 10})
	require.NoError(t, err)
	body, contentType, err := b.Read(context.Background(), got[0])
	require.NoError(t, err)
	assert.Empty(t, body)
	assert.NotNil(t, body)
	assert.Empty(t, contentType)
}

func TestList_Limits(t *testing.T) {
	keys := Limits{ObjectsKey: "x.files", ObjectBytesKey: "x.file_bytes", TotalBytesKey: "x.total"}
	for name, tc := range map[string]struct {
		lim  Limits
		want string
	}{
		"too many":    {withMax(keys, func(l *Limits) { l.MaxObjects = 1 }), "x.files"},
		"one too big": {withMax(keys, func(l *Limits) { l.MaxObjectBytes = 15 }), "x.file_bytes"},
		"too much":    {withMax(keys, func(l *Limits) { l.MaxTotalBytes = 25 }), "x.total"},
	} {
		t.Run(name, func(t *testing.T) {
			b, err := (&Access{Clients: &fakeClients{client: site()}}).Open(context.Background(), Source{Connection: "c", Bucket: "b", Prefix: "exports/site/"})
			require.NoError(t, err)
			_, err = b.List(context.Background(), tc.lim)
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestList_RefusesANameNoFolderCanHold(t *testing.T) {
	for _, key := range []string{"p/a/../b.txt", "p/a//b.txt", "p/./b.txt", "p/a\\b.txt", "p/a\x01b.txt"} {
		client := &fakeClient{keys: []string{key}, sizes: map[string]int64{}}
		b, err := (&Access{Clients: &fakeClients{client: client}}).Open(context.Background(), Source{Connection: "c", Bucket: "b", Prefix: "p"})
		require.NoError(t, err)
		_, err = b.List(context.Background(), Limits{})
		require.ErrorIs(t, err, ErrRefused, key)
	}
}

func TestStoreFailures_AreReportedByTheirCodeAlone(t *testing.T) {
	client := site()
	client.listErr = codedError{}
	b, err := (&Access{Clients: &fakeClients{client: client}}).Open(context.Background(), Source{Connection: "c", Bucket: "b"})
	require.NoError(t, err)
	_, err = b.List(context.Background(), Limits{})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.NotContains(t, err.Error(), "secret", "the client's own text, which names its endpoint, never reaches the caller")
	assert.NotContains(t, err.Error(), "minio.internal")

	client.listErr, client.getErr = nil, errors.New("connection reset by https://key:secret@host")
	_, _, err = b.Read(context.Background(), Object{Key: "k"})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "an error")
	assert.NotContains(t, err.Error(), "secret")
}

func TestFromToolkits(t *testing.T) {
	a := FromToolkits(nil, nil)
	assert.Nil(t, a.Allowed, "no personas configured allows every connection")
	_, err := a.Open(context.Background(), Source{Connection: "c", Bucket: "b"})
	require.ErrorContains(t, err, `no S3 connection named "c"`)
}
