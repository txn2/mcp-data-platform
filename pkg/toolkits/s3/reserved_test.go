package s3

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platformSecret is what a personal resource in the platform's bucket holds:
// no tool answer may carry it in any form.
const platformSecret = "jane's private payroll 4471"

// reservePlatform reserves the platform's two buckets for one test and puts
// an object holding platformSecret in each.
func reservePlatform(t *testing.T, fake *fakeS3) {
	t.Helper()
	SetReservedBuckets(PlatformBuckets("portal-assets", "managed-resources"))
	t.Cleanup(func() { SetReservedBuckets(nil) })
	fake.buckets["managed-resources"] = map[string]fakeObject{"resources/user/jane/r1/pay.txt": {body: []byte(platformSecret), contentType: "text/plain"}}
	fake.buckets["portal-assets"] = map[string]fakeObject{"artifacts/jane/a1/content.html": {body: []byte(platformSecret), contentType: "text/html"}}
}

// assertNoSecret checks a tool answer for platformSecret as written, in
// base64 and in hex.
func assertNoSecret(t *testing.T, text string) {
	t.Helper()
	for _, form := range []string{platformSecret, base64.StdEncoding.EncodeToString([]byte(platformSecret)), hex.EncodeToString([]byte(platformSecret))} {
		assert.NotContains(t, text, form)
	}
}

// TestS3_RefusesThePlatformsBuckets is #2616: every s3_object action and
// s3_list naming one of the platform's own buckets is refused, before any
// request reaches the object store, naming the surface that reads it instead;
// a copy is refused by its destination as well as its source.
func TestS3_RefusesThePlatformsBuckets(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	reservePlatform(t, fake)
	rt := newRoundTrip(t, tk)

	_, errText := rt.call("s3_list", map[string]any{"bucket": "managed-resources"})
	assert.Contains(t, errText, `bucket "managed-resources" is where the platform keeps the managed-resource library`)
	assert.Contains(t, errText, "fetch or manage_resource")
	assert.Empty(t, fake.lastLs.bucket, "the platform's bucket was never listed")

	for _, bucket := range []string{"managed-resources", "portal-assets"} {
		for _, call := range []map[string]any{
			{"action": "get"},
			{"action": "metadata"},
			{"action": "put", "content": "planted"},
			{"action": "delete"},
			{"action": "copy", "dest_bucket": "acme-lake", "dest_key": "out.txt"},
			{"action": "presign"},
			{"action": "presign", "method": "PUT"},
		} {
			call["bucket"], call["key"] = bucket, "resources/user/jane/r1/pay.txt"
			if bucket == "portal-assets" {
				call["key"] = "artifacts/jane/a1/content.html"
			}
			out, errText := rt.call("s3_object", call)
			assert.Nil(t, out, call)
			assert.Contains(t, errText, `bucket "`+bucket+`" is where the platform keeps`, call)
			assertNoSecret(t, errText)
		}
	}
	_, errText = rt.call("s3_object", map[string]any{"action": "copy", "bucket": "acme-lake", "key": "sales/2026/q1.csv", "dest_bucket": "managed-resources", "dest_key": "resources/user/jane/r1/pay.txt"})
	assert.Contains(t, errText, `bucket "managed-resources" is where the platform keeps`, "a copy into the platform's bucket is refused by its destination")
	assert.Nil(t, fake.lastPut, "nothing was written")
	assert.Nil(t, fake.lastCp, "nothing was copied")
	assert.Zero(t, fake.presignPuts, "no upload link was signed")
	assert.Equal(t, platformSecret, string(fake.buckets["managed-resources"]["resources/user/jane/r1/pay.txt"].body), "the resource is untouched")
}

// TestS3_ReservedNameVariants: a name the object store could read as the
// platform's bucket is refused however it is written.
func TestS3_ReservedNameVariants(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	reservePlatform(t, fake)
	rt := newRoundTrip(t, tk)
	for _, bucket := range []string{"Managed-Resources", " managed-resources ", "managed-resources/", "arn:aws:s3:::managed-resources"} {
		_, errText := rt.call("s3_object", map[string]any{"action": "get", "bucket": bucket, "key": "resources/user/jane/r1/pay.txt"})
		assert.Contains(t, errText, "is where the platform keeps", bucket)
		assertNoSecret(t, errText)
	}
}

// TestS3List_OmitsThePlatformsBuckets: listing the buckets leaves the
// platform's out, and a business bucket is still served.
func TestS3List_OmitsThePlatformsBuckets(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	reservePlatform(t, fake)
	rt := newRoundTrip(t, tk)
	out, errText := rt.call("s3_list", map[string]any{})
	require.Empty(t, errText)
	buckets, ok := out["buckets"].([]any)
	require.True(t, ok)
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		bucket, _ := b.(map[string]any)
		name, _ := bucket["name"].(string)
		names = append(names, name)
	}
	assert.ElementsMatch(t, []string{"acme-lake", "acme-reports", "other"}, names)
	_, errText = rt.call("s3_object", map[string]any{"action": "get", "bucket": "acme-lake", "key": "sales/2026/q1.csv"})
	assert.Empty(t, errText, "a business bucket is served")
}

// TestReadClient_RefusesThePlatformsBuckets: a bucket source cannot read the
// platform's buckets either.
func TestReadClient_RefusesThePlatformsBuckets(t *testing.T) {
	tk, fake := newFakeToolkit(Config{})
	reservePlatform(t, fake)
	_, err := tk.ReadClient("lake", "portal-assets")
	require.ErrorContains(t, err, `bucket "portal-assets" is where the platform keeps portal assets, exports, script outputs and site releases`)
	assert.ErrorContains(t, err, "manage_site")
	c, err := tk.ReadClient("lake", "acme-lake")
	require.NoError(t, err)
	assert.NotNil(t, c)
}

// TestBucketNames lists what the credentials reach, the platform's buckets
// included: the boot check reads it, and no caller does.
func TestBucketNames(t *testing.T) {
	tk, fake := newFakeToolkit(Config{BucketPrefix: "acme-"})
	reservePlatform(t, fake)
	names, err := tk.BucketNames(context.Background(), "lake")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"acme-lake", "acme-reports", "other", "managed-resources", "portal-assets"}, names)
	_, err = tk.BucketNames(context.Background(), "elsewhere")
	require.ErrorContains(t, err, `no S3 connection named "elsewhere"`)
	fake.fail = assert.AnError
	_, err = tk.BucketNames(context.Background(), "lake")
	require.Error(t, err)
}

// TestIsReserved: nothing is reserved until the platform installs its
// buckets, and an empty store name reserves nothing.
func TestIsReserved(t *testing.T) {
	SetReservedBuckets(nil)
	_, ok := IsReserved("managed-resources")
	assert.False(t, ok)
	SetReservedBuckets(PlatformBuckets("", "managed-resources"))
	t.Cleanup(func() { SetReservedBuckets(nil) })
	_, ok = IsReserved("")
	assert.False(t, ok)
	_, ok = IsReserved("portal-assets")
	assert.False(t, ok, "the portal names no bucket")
	r, ok := IsReserved("managed-resources")
	require.True(t, ok)
	assert.Contains(t, r.Use, "platform.read_resource")
}
