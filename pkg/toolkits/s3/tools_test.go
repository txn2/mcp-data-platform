package s3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeS3 is an in-memory S3Client: buckets of key -> object, plus the last
// inputs the handlers passed, so a test can assert what reached the client.
type fakeS3 struct {
	name    string
	buckets map[string]map[string]fakeObject
	fail    error
	lastPut *s3client.PutObjectInput
	lastCp  *s3client.CopyObjectInput
	lastLs  fakeListCall
}

type fakeObject struct {
	body        []byte
	contentType string
	metadata    map[string]string
}

type fakeListCall struct {
	bucket, prefix, delimiter, token string
	maxKeys                          int32
}

var fakeStamp = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

func newFakeS3(name string) *fakeS3 {
	return &fakeS3{name: name, buckets: map[string]map[string]fakeObject{
		"acme-lake":    {"sales/2026/q1.csv": {body: []byte("a,b\n1,2\n"), contentType: "text/csv"}, "sales/2026/q2.csv": {body: []byte("a,b\n3,4\n"), contentType: "text/csv"}},
		"acme-reports": {"blob.bin": {body: []byte{0xff, 0x00, 0x01}, contentType: "application/octet-stream", metadata: map[string]string{"owner": "ops"}}},
		"other":        {},
	}}
}

func (f *fakeS3) ConnectionName() string   { return f.name }
func (f *fakeS3) Config() *s3client.Config { return &s3client.Config{Name: f.name} }
func (*fakeS3) Close() error               { return nil }
func (f *fakeS3) ListBuckets(context.Context) ([]s3client.BucketInfo, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	out := make([]s3client.BucketInfo, 0, len(f.buckets))
	for name := range f.buckets {
		out = append(out, s3client.BucketInfo{Name: name, CreationDate: fakeStamp})
	}
	return out, nil
}

func (f *fakeS3) ListObjects(_ context.Context, bucket, prefix, delimiter string, maxKeys int32, token string) (*s3client.ListObjectsOutput, error) { //nolint:revive // the upstream S3Client interface fixes the arity
	f.lastLs = fakeListCall{bucket: bucket, prefix: prefix, delimiter: delimiter, token: token, maxKeys: maxKeys}
	if f.fail != nil {
		return nil, f.fail
	}
	objs, ok := f.buckets[bucket]
	if !ok {
		return nil, errors.New("NoSuchBucket")
	}
	out := &s3client.ListObjectsOutput{}
	for key, obj := range objs {
		if strings.HasPrefix(key, prefix) {
			out.Objects = append(out.Objects, s3client.ObjectInfo{Key: key, Size: int64(len(obj.body)), LastModified: fakeStamp, ETag: "e", StorageClass: "STANDARD"})
		}
	}
	if delimiter != "" {
		out.CommonPrefixes = []string{prefix + "sub" + delimiter}
	}
	if token == "" && len(out.Objects) > 1 {
		out.IsTruncated, out.NextContinueToken = true, "next"
	}
	return out, nil
}

func (f *fakeS3) object(bucket, key string) (fakeObject, error) {
	objs, ok := f.buckets[bucket]
	if !ok {
		return fakeObject{}, errors.New("NoSuchBucket")
	}
	obj, ok := objs[key]
	if !ok {
		return fakeObject{}, errors.New("NoSuchKey")
	}
	return obj, nil
}

func (f *fakeS3) GetObject(_ context.Context, bucket, key string) (*s3client.ObjectContent, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	obj, err := f.object(bucket, key)
	if err != nil {
		return nil, err
	}
	return &s3client.ObjectContent{Key: key, Body: obj.body, ContentType: obj.contentType, Size: int64(len(obj.body)), LastModified: fakeStamp, ETag: "e", Metadata: obj.metadata}, nil
}

func (f *fakeS3) GetObjectMetadata(_ context.Context, bucket, key string) (*s3client.ObjectMetadata, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	obj, err := f.object(bucket, key)
	if err != nil {
		return nil, err
	}
	return &s3client.ObjectMetadata{Key: key, Size: int64(len(obj.body)), ContentLength: int64(len(obj.body)), ContentType: obj.contentType, LastModified: fakeStamp, ETag: "e", Metadata: obj.metadata}, nil
}

func (f *fakeS3) PutObject(_ context.Context, in *s3client.PutObjectInput) (*s3client.PutObjectOutput, error) {
	f.lastPut = in
	if f.fail != nil {
		return nil, f.fail
	}
	if _, ok := f.buckets[in.Bucket]; !ok {
		return nil, errors.New("NoSuchBucket")
	}
	f.buckets[in.Bucket][in.Key] = fakeObject{body: in.Body, contentType: in.ContentType, metadata: in.Metadata}
	return &s3client.PutObjectOutput{ETag: "put-etag"}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, bucket, key string) error {
	if f.fail != nil {
		return f.fail
	}
	if _, err := f.object(bucket, key); err != nil {
		return err
	}
	delete(f.buckets[bucket], key)
	return nil
}

func (f *fakeS3) CopyObject(_ context.Context, in *s3client.CopyObjectInput) (*s3client.CopyObjectOutput, error) {
	f.lastCp = in
	if f.fail != nil {
		return nil, f.fail
	}
	obj, err := f.object(in.SourceBucket, in.SourceKey)
	if err != nil {
		return nil, err
	}
	if _, ok := f.buckets[in.DestBucket]; !ok {
		return nil, errors.New("NoSuchBucket")
	}
	if in.Metadata != nil {
		obj.metadata = in.Metadata
	}
	f.buckets[in.DestBucket][in.DestKey] = obj
	return &s3client.CopyObjectOutput{ETag: "copy-etag", LastModified: fakeStamp}, nil
}

func (f *fakeS3) PresignGetURL(_ context.Context, bucket, key string, expires time.Duration) (*s3client.PresignedURL, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return &s3client.PresignedURL{URL: "https://s3.example.test/" + bucket + "/" + key + "?get", Method: methodGet, ExpiresAt: fakeStamp.Add(expires)}, nil
}

func (f *fakeS3) PresignPutURL(_ context.Context, bucket, key string, expires time.Duration) (*s3client.PresignedURL, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return &s3client.PresignedURL{URL: "https://s3.example.test/" + bucket + "/" + key + "?put", Method: methodPut, ExpiresAt: fakeStamp.Add(expires)}, nil
}

// newFakeToolkit is a Toolkit over the fake client, bound as connection "lake"
// with the given settings.
func newFakeToolkit(cfg Config) (*Toolkit, *fakeS3) {
	fake := newFakeS3("lake")
	cfg.ConnectionName = "lake"
	cfg = applyDefaults("lake", cfg)
	tk := &Toolkit{
		name:        "lake",
		config:      cfg,
		s3Toolkit:   s3tools.NewToolkit(fake),
		connections: map[string]connSettings{"lake": settingsOf(cfg)},
	}
	return tk, fake
}

// roundTrip drives the toolkit through a real MCP server and in-memory client,
// so every argument crosses as the JSON a client sends.
type roundTrip struct {
	t  *testing.T
	cs *mcp.ClientSession
}

func newRoundTrip(t *testing.T, tk *Toolkit) *roundTrip {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v0"}, nil)
	tk.RegisterTools(server)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return &roundTrip{t: t, cs: cs}
}

// call returns the decoded structured result, or the error text when the tool
// answered with an error result.
func (r *roundTrip) call(tool string, args map[string]any) (out map[string]any, errText string) {
	r.t.Helper()
	res, err := r.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(r.t, err)
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	if res.IsError {
		return nil, text
	}
	require.NoError(r.t, json.Unmarshal([]byte(text), &out), "structured result is the text block: %s", text)
	return out, ""
}

func TestS3List_BucketsAndObjects(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	rt := newRoundTrip(t, tk)

	t.Run("no bucket lists the buckets", func(t *testing.T) {
		out, errText := rt.call("s3_list", map[string]any{})
		require.Empty(t, errText)
		assert.EqualValues(t, 3, out["count"])
		names := bucketNames(out)
		assert.ElementsMatch(t, []string{"acme-lake", "acme-reports", "other"}, names)
	})

	t.Run("a bucket lists its objects and forwards every listing parameter", func(t *testing.T) {
		out, errText := rt.call("s3_list", map[string]any{
			"bucket": "acme-lake", "prefix": "sales/", "delimiter": "/", "max_keys": 500, "continuation_token": "",
		})
		require.Empty(t, errText)
		assert.Equal(t, "acme-lake", out["bucket"])
		assert.EqualValues(t, 2, out["count"])
		assert.Equal(t, true, out["is_truncated"])
		assert.Equal(t, "next", out["next_continuation_token"])
		assert.Equal(t, []any{"sales/sub/"}, out["common_prefixes"])
		assert.Equal(t, fakeListCall{bucket: "acme-lake", prefix: "sales/", delimiter: "/", maxKeys: 500}, fake.lastLs)

		out, errText = rt.call("s3_list", map[string]any{"bucket": "acme-lake", "continuation_token": "next"})
		require.Empty(t, errText)
		assert.Equal(t, false, out["is_truncated"], "the continuation page is the last")
		assert.Equal(t, "next", fake.lastLs.token)
	})

	t.Run("max_keys is clamped to 1000 and defaulted when absent", func(t *testing.T) {
		_, errText := rt.call("s3_list", map[string]any{"bucket": "acme-lake", "max_keys": 5000})
		require.Empty(t, errText)
		assert.EqualValues(t, 1000, fake.lastLs.maxKeys)
		_, errText = rt.call("s3_list", map[string]any{"bucket": "acme-lake"})
		require.Empty(t, errText)
		assert.EqualValues(t, 1000, fake.lastLs.maxKeys)
	})

	t.Run("a bucket the connection cannot see is the tool's own error", func(t *testing.T) {
		_, errText := rt.call("s3_list", map[string]any{"bucket": "missing"})
		assert.Contains(t, errText, "failed to list objects")
	})

	t.Run("an unknown connection is refused by name", func(t *testing.T) {
		_, errText := rt.call("s3_list", map[string]any{"connection": "nowhere"})
		assert.Contains(t, errText, "connection not found: nowhere")
	})

	t.Run("a listing failure reaches the client as the tool's error", func(t *testing.T) {
		fake.fail = errors.New("AccessDenied")
		defer func() { fake.fail = nil }()
		_, errText := rt.call("s3_list", map[string]any{})
		assert.Contains(t, errText, "failed to list buckets: AccessDenied")
	})
}

func bucketNames(out map[string]any) []string {
	buckets, _ := out["buckets"].([]any)
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		bucket, _ := b.(map[string]any)
		name, _ := bucket["name"].(string)
		names = append(names, name)
	}
	return names
}

// TestS3List_BucketPrefixFiltersBuckets: bucket_prefix narrows the bucket
// listing to the buckets it names, as its documentation says.
func TestS3List_BucketPrefixFiltersBuckets(t *testing.T) {
	tk, _ := newFakeToolkit(Config{BucketPrefix: "acme-"})
	out, errText := newRoundTrip(t, tk).call("s3_list", map[string]any{})
	require.Empty(t, errText)
	assert.ElementsMatch(t, []string{"acme-lake", "acme-reports"}, bucketNames(out))
	assert.EqualValues(t, 2, out["count"])
}

func TestS3Object_Actions(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	rt := newRoundTrip(t, tk)

	t.Run("get returns text as text", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
		require.Empty(t, errText)
		assert.Equal(t, "a,b\n1,2\n", out["content"])
		assert.Equal(t, false, out["is_base64"])
		assert.Equal(t, "text/csv", out["content_type"])
		assert.Equal(t, "2026-09-02T10:00:00Z", out["last_modified"])
	})

	t.Run("get returns binary as base64", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-reports", "key": "blob.bin"})
		require.Empty(t, errText)
		assert.Equal(t, true, out["is_base64"])
		content, _ := out["content"].(string)
		raw, err := base64.StdEncoding.DecodeString(content)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xff, 0x00, 0x01}, raw)
		assert.Equal(t, map[string]any{"owner": "ops"}, out["metadata"])
	})

	t.Run("metadata returns the record without the content", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "metadata", "bucket": "acme-reports", "key": "blob.bin"})
		require.Empty(t, errText)
		assert.EqualValues(t, 3, out["size"])
		assert.EqualValues(t, 3, out["content_length"])
		assert.Equal(t, "application/octet-stream", out["content_type"])
		assert.Nil(t, out["content"])
	})

	t.Run("put uploads text with its content type and metadata", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{
			"action": "put", "bucket": "acme-lake", "key": "notes/readme.md", "content": "# hi",
			"content_type": "text/markdown", "metadata": map[string]any{"author": "ana"},
		})
		require.Empty(t, errText)
		assert.EqualValues(t, 4, out["size"])
		assert.Equal(t, "put-etag", out["etag"])
		assert.Equal(t, "text/markdown", fake.lastPut.ContentType)
		assert.Equal(t, map[string]string{"author": "ana"}, fake.lastPut.Metadata)
		assert.Equal(t, []byte("# hi"), fake.buckets["acme-lake"]["notes/readme.md"].body)
	})

	t.Run("put decodes base64 and defaults the content type", func(t *testing.T) {
		_, errText := rt.call("s3_object", map[string]any{
			"action": "put", "bucket": "acme-lake", "key": "bin/x", "content": base64.StdEncoding.EncodeToString([]byte{0, 1, 2}), "is_base64": true,
		})
		require.Empty(t, errText)
		assert.Equal(t, []byte{0, 1, 2}, fake.lastPut.Body)
		assert.Equal(t, "application/octet-stream", fake.lastPut.ContentType)
	})

	t.Run("put refuses undecodable base64 and empty content", func(t *testing.T) {
		_, errText := rt.call("s3_object", map[string]any{"action": "put", "bucket": "acme-lake", "key": "bin/x", "content": "%%%", "is_base64": true})
		assert.Contains(t, errText, "failed to decode base64 content")
		_, errText = rt.call("s3_object", map[string]any{"action": "put", "bucket": "acme-lake", "key": "bin/x"})
		assert.Contains(t, errText, "content parameter is required")
	})

	t.Run("copy defaults the destination bucket to the source bucket", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "copy", "bucket": "acme-lake", "key": "sales/2026/q1.csv", "dest_key": "archive/q1.csv"})
		require.Empty(t, errText)
		assert.Equal(t, "acme-lake", out["dest_bucket"])
		assert.Equal(t, "archive/q1.csv", out["dest_key"])
		assert.Equal(t, "copy-etag", out["etag"])
		assert.Contains(t, fake.buckets["acme-lake"], "archive/q1.csv")
	})

	t.Run("copy across buckets replaces metadata when given", func(t *testing.T) {
		_, errText := rt.call("s3_object", map[string]any{
			"action": "copy", "bucket": "acme-reports", "key": "blob.bin", "dest_bucket": "other", "dest_key": "blob.bin", "metadata": map[string]any{"owner": "finance"},
		})
		require.Empty(t, errText)
		assert.Equal(t, map[string]string{"owner": "finance"}, fake.buckets["other"]["blob.bin"].metadata)
		_, errText = rt.call("s3_object", map[string]any{"action": "copy", "bucket": "acme-reports", "key": "blob.bin"})
		assert.Contains(t, errText, "dest_key parameter is required")
	})

	t.Run("delete removes the object and a second delete is the backend's error", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "delete", "bucket": "other", "key": "blob.bin"})
		require.Empty(t, errText)
		assert.Equal(t, true, out["deleted"])
		assert.NotContains(t, fake.buckets["other"], "blob.bin")
		_, errText = rt.call("s3_object", map[string]any{"action": "delete", "bucket": "other", "key": "blob.bin"})
		assert.Contains(t, errText, "failed to delete object: NoSuchKey")
	})

	t.Run("presign defaults to GET for an hour, takes PUT, clamps and refuses other methods", func(t *testing.T) {
		out, errText := rt.call("s3_object", map[string]any{"action": "presign", "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
		require.Empty(t, errText)
		assert.Equal(t, "GET", out["method"])
		assert.EqualValues(t, 3600, out["expires_in_seconds"])
		assert.Contains(t, out["url"], "?get")
		assert.Equal(t, "2026-09-02T11:00:00Z", out["expires_at"])

		out, errText = rt.call("s3_object", map[string]any{"action": "presign", "bucket": "acme-lake", "key": "up.csv", "method": "put", "expires_in": 9999999})
		require.Empty(t, errText)
		assert.Equal(t, "PUT", out["method"])
		assert.EqualValues(t, maxPresignSeconds, out["expires_in_seconds"])

		_, errText = rt.call("s3_object", map[string]any{"action": "presign", "bucket": "acme-lake", "key": "x", "method": "DELETE"})
		assert.Contains(t, errText, "invalid method: DELETE")
	})

	t.Run("get of a missing key and an unknown action are refused in the tool's words", func(t *testing.T) {
		_, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": "nope"})
		assert.Contains(t, errText, "failed to get object metadata: NoSuchKey")
		_, errText = rt.call("s3_object", map[string]any{"action": "rename", "bucket": "acme-lake", "key": "x"})
		assert.Contains(t, errText, `unknown action "rename"`)
	})

	t.Run("bucket and key are required, and an unknown connection is named", func(t *testing.T) {
		_, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "", "key": "x"})
		assert.Contains(t, errText, "bucket parameter is required")
		_, errText = rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": ""})
		assert.Contains(t, errText, "key parameter is required")
		_, errText = rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": "x", "connection": "nowhere"})
		assert.Contains(t, errText, "connection not found: nowhere")
	})
}

// TestS3Object_SizeLimits: max_get_size refuses a get whose object is larger
// before downloading it, and max_put_size refuses an oversize upload before
// the client is called.
func TestS3Object_SizeLimits(t *testing.T) {
	tk, fake := newFakeToolkit(Config{MaxGetSize: 4, MaxPutSize: 4})
	rt := newRoundTrip(t, tk)

	_, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
	assert.Contains(t, errText, "object size 8 bytes exceeds limit of 4 bytes")
	out, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-reports", "key": "blob.bin"})
	require.Empty(t, errText)
	assert.EqualValues(t, 3, out["size"])

	fake.lastPut = nil
	_, errText = rt.call("s3_object", map[string]any{"action": "put", "bucket": "acme-lake", "key": "big", "content": "12345"})
	assert.Contains(t, errText, "content size 5 bytes exceeds limit of 4 bytes")
	assert.Nil(t, fake.lastPut, "an oversize upload never reaches the client")
}

// TestS3Object_ReadOnlyRefusesWritesNamingTheConnection pins acceptance 2 of
// #1591: put, copy and delete are refused on a read-only connection, the
// refusal names the connection, and the read actions still work.
func TestS3Object_ReadOnlyRefusesWritesNamingTheConnection(t *testing.T) {
	tk, fake := newFakeToolkit(Config{ReadOnly: true})
	rt := newRoundTrip(t, tk)

	for _, action := range []string{"put", "copy", "delete"} {
		_, errText := rt.call("s3_object", map[string]any{"action": action, "bucket": "acme-lake", "key": "sales/2026/q1.csv", "content": "x", "dest_key": "y"})
		assert.Contains(t, errText, `connection "lake" is read-only`, action)
		assert.Contains(t, errText, `action "`+action+`" is not permitted`, action)
	}
	assert.Nil(t, fake.lastPut)
	assert.Nil(t, fake.lastCp)
	assert.Contains(t, fake.buckets["acme-lake"], "sales/2026/q1.csv", "nothing was deleted")

	for _, action := range []string{"get", "metadata", "presign"} {
		_, errText := rt.call("s3_object", map[string]any{"action": action, "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
		assert.Empty(t, errText, action)
	}
	_, errText := rt.call("s3_list", map[string]any{"bucket": "acme-lake"})
	assert.Empty(t, errText)
}

// TestS3Object_ReadOnlyIsPerConnection: a connection added at run time carries
// its own read_only, bucket_prefix and size ceilings, so a read-only
// connection added beside a writable primary refuses writes by its own name
// while the primary keeps accepting them, and removing it forgets its settings.
func TestS3Object_ReadOnlyIsPerConnection(t *testing.T) {
	tk, _ := newFakeToolkit(Config{})
	frozen := newFakeS3("frozen")
	tk.s3Toolkit.AddClient("frozen", frozen)
	tk.connMu.Lock()
	tk.connections["frozen"] = connSettings{readOnly: true, bucketPrefix: "acme-", maxGetSize: DefaultMaxGetSize, maxPutSize: DefaultMaxPutSize}
	tk.connMu.Unlock()
	rt := newRoundTrip(t, tk)

	_, errText := rt.call("s3_object", map[string]any{"action": "delete", "bucket": "acme-lake", "key": "sales/2026/q1.csv", "connection": "frozen"})
	assert.Contains(t, errText, `connection "frozen" is read-only`)
	_, errText = rt.call("s3_object", map[string]any{"action": "delete", "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
	assert.Empty(t, errText, "the writable primary is unaffected")

	out, errText := rt.call("s3_list", map[string]any{"connection": "frozen"})
	require.Empty(t, errText)
	assert.ElementsMatch(t, []string{"acme-lake", "acme-reports"}, bucketNames(out), "the added connection's bucket_prefix applies to it")

	require.NoError(t, tk.RemoveConnection("frozen"))
	assert.False(t, tk.HasConnection("frozen"))
	assert.Equal(t, settingsOf(tk.config), tk.settings("frozen"), "a removed connection's settings are gone")
}

// TestAddConnection_RecordsItsOwnSettings: AddConnection builds a real client
// (static credentials, no network) and enters the connection's read_only,
// bucket_prefix and size ceilings, defaults applied.
func TestAddConnection_RecordsItsOwnSettings(t *testing.T) {
	tk, err := New("primary", Config{Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b", Endpoint: "http://localhost:1"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	require.NoError(t, tk.AddConnection("archive", map[string]any{
		"region": "us-east-1", "access_key_id": "a", "secret_access_key": "b", "endpoint": "http://localhost:1",
		"read_only": true, "bucket_prefix": "arc-", "max_get_size": 512,
	}))
	got := tk.settings("archive")
	assert.Equal(t, connSettings{readOnly: true, bucketPrefix: "arc-", maxGetSize: 512, maxPutSize: DefaultMaxPutSize}, got)
	assert.Equal(t, connSettings{maxGetSize: DefaultMaxGetSize, maxPutSize: DefaultMaxPutSize}, tk.settings(""), "the primary's settings are the empty name's")
	assert.True(t, tk.HasConnection("archive"))

	err = tk.AddConnection("broken", map[string]any{"region": 42})
	assert.NoError(t, err, "ParseConfig tolerates a wrong-typed field; the connection is added with defaults")
}

func TestOperationLabel(t *testing.T) {
	assert.Equal(t, "put", operationLabel("put"))
	assert.Equal(t, "unknown", operationLabel("rename"), "a caller-supplied action never mints a metric series")
}

func TestIsTextContent(t *testing.T) {
	assert.True(t, isTextContent("application/json", []byte{0xff}), "a declared textual type wins")
	assert.True(t, isTextContent("", []byte("plain")))
	assert.True(t, isTextContent("", nil))
	assert.False(t, isTextContent("application/octet-stream", []byte{'a', 0, 'b'}), "a NUL is binary")
	assert.False(t, isTextContent("image/png", []byte{0xff, 0xfe}), "invalid UTF-8 is binary")
}

func TestStamp(t *testing.T) {
	assert.Equal(t, "", stamp(time.Time{}))
	assert.Equal(t, "2026-09-02T10:00:00Z", stamp(fakeStamp.In(time.FixedZone("x", 3600))))
}

// TestNewMulti_BindsEveryInstance: one toolkit serves every declared instance
// by its bound name, the default is the one named (or the alphabetically
// first), and list_connections sees them all.
func TestNewMulti_BindsEveryInstance(t *testing.T) {
	static := Config{Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b", Endpoint: "http://localhost:1"}
	archive := static
	archive.ReadOnly, archive.Description = true, "cold storage"
	labeled := static
	labeled.ConnectionName = "reports"

	tk, err := NewMulti(MultiConfig{DefaultConnection: "lake", Instances: map[string]Config{"lake": static, "archive": archive, "rep": labeled}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	assert.Equal(t, "lake", tk.Connection())
	assert.Equal(t, []toolkit.ConnectionDetail{
		{Name: "archive", Description: "cold storage"},
		{Name: "lake", IsDefault: true},
		{Name: "reports"},
	}, tk.ListConnections(), "an instance is bound by its connection_name, its instance name when it sets none")
	assert.True(t, tk.settings("archive").readOnly)
	assert.False(t, tk.settings("").readOnly, "the default connection's settings are the empty name's")
	assert.True(t, tk.HasConnection("reports"))
	assert.True(t, tk.HasConnection("archive"))

	first, err := NewMulti(MultiConfig{Instances: map[string]Config{"zeta": static, "alpha": static}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	assert.Equal(t, "alpha", first.Connection(), "with no default declared the alphabetically first instance is it")

	_, err = NewMulti(MultiConfig{})
	assert.ErrorContains(t, err, "at least one s3 instance")
	_, err = NewMulti(MultiConfig{DefaultConnection: "nope", Instances: map[string]Config{"lake": static}})
	assert.ErrorContains(t, err, `default connection "nope" not found`)
}

func TestParseMultiConfig(t *testing.T) {
	mc, err := ParseMultiConfig("lake", map[string]map[string]any{
		"lake":    {"region": "us-west-2", "description": "the lake", "read_only": true},
		"archive": {"bucket_prefix": "arc-"},
	})
	require.NoError(t, err)
	assert.Equal(t, "lake", mc.DefaultConnection)
	assert.Len(t, mc.Instances, 2)
	assert.Equal(t, "the lake", mc.Instances["lake"].Description)
	assert.True(t, mc.Instances["lake"].ReadOnly)
	assert.Equal(t, "arc-", mc.Instances["archive"].BucketPrefix)
}

// TestNewMulti_TwoInstancesShareOneRegistration pins the defect the aggregate
// closes: two instances on one server register s3_list and s3_object once, and
// a call reaches either by its connection name.
func TestNewMulti_TwoInstancesShareOneRegistration(t *testing.T) {
	tk, _ := newFakeToolkit(Config{})
	second := newFakeS3("reports")
	tk.s3Toolkit.AddClient("reports", second)
	tk.connMu.Lock()
	tk.connections["reports"] = settingsOf(applyDefaults("reports", Config{}))
	tk.connMu.Unlock()
	rt := newRoundTrip(t, tk)

	out, errText := rt.call("s3_list", map[string]any{"connection": "reports", "bucket": "acme-lake"})
	require.Empty(t, errText)
	assert.EqualValues(t, 2, out["count"])
	out, errText = rt.call("s3_list", map[string]any{"connection": "lake", "bucket": "acme-lake"})
	require.Empty(t, errText)
	assert.EqualValues(t, 2, out["count"])
}
