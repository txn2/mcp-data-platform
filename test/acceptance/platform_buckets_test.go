//go:build integration

package acceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// The platform's own buckets are refused to every S3 tool on every connection,
// administrators included (#2616). A criterion that plants an object behind
// the platform's back (an orphan, a stray release file, a vendor drop) or
// reads what the platform wrote reaches the bucket the way an operator does:
// with the store's own client and credentials, never through a tool.

const (
	// devResourceBucket is where the dev stack keeps managed resources,
	// webhook events and the tables registered over them, on its TLS store.
	devResourceBucket = "managed-resources"
	// devAssetBucket is the portal's bucket on the dev stack's plain store:
	// assets, exports, script outputs and site releases.
	devAssetBucket = "portal-assets"
	// devBusinessBucket is a bucket on the plain store the platform keeps
	// nothing in, which the S3 tools serve (dev/start.sh).
	devBusinessBucket = "portal-lake"
)

// platformStore is one platform bucket reached with its store's own client.
type platformStore struct {
	t      *testing.T
	client *s3client.Client
	bucket string
}

// resourceStore is the managed-resources bucket on the dev stack's TLS store.
func resourceStore(t *testing.T) *platformStore {
	t.Helper()
	port := os.Getenv("DEV_S3_TLS_PORT")
	if port == "" {
		port = "9443"
	}
	root, _ := filepath.Abs("../..")
	t.Setenv("AWS_CA_BUNDLE", filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"))
	return openPlatformStore(t, "https://localhost:"+port, devResourceBucket)
}

// assetStore is the portal bucket on the dev stack's plain store.
func assetStore(t *testing.T) *platformStore {
	t.Helper()
	port := os.Getenv("DEV_S3_PORT")
	if port == "" {
		port = "9000"
	}
	return openPlatformStore(t, "http://localhost:"+port, devAssetBucket)
}

func openPlatformStore(t *testing.T, endpoint, bucket string) *platformStore {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := s3client.New(ctx, &s3client.Config{
		Region: "us-east-1", Endpoint: endpoint, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("opening the store at %s: %v", endpoint, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &platformStore{t: t, client: c, bucket: bucket}
}

// put writes key, and removes it when the test ends unless keep is asked for
// by deleting it first.
func (s *platformStore) put(key, contentType string, body []byte) {
	s.t.Helper()
	if _, err := s.client.PutObject(s.t.Context(), &s3client.PutObjectInput{
		Bucket: s.bucket, Key: key, Body: body, ContentType: contentType,
	}); err != nil {
		s.t.Fatalf("writing s3://%s/%s: %v", s.bucket, key, err)
	}
	s.t.Cleanup(func() { _ = s.client.DeleteObject(context.Background(), s.bucket, key) })
}

// get reads key, reporting false when there is no such object.
func (s *platformStore) get(key string) ([]byte, bool) {
	s.t.Helper()
	out, err := s.client.GetObject(s.t.Context(), s.bucket, key)
	if err != nil {
		if isMissing(err) {
			return nil, false
		}
		s.t.Fatalf("reading s3://%s/%s: %v", s.bucket, key, err)
	}
	return out.Body, true
}

// exists reports whether key is in the bucket.
func (s *platformStore) exists(key string) bool {
	s.t.Helper()
	if _, err := s.client.GetObjectMetadata(s.t.Context(), s.bucket, key); err != nil {
		if isMissing(err) {
			return false
		}
		s.t.Fatalf("reading the metadata of s3://%s/%s: %v", s.bucket, key, err)
	}
	return true
}

// keys is every key under prefix.
func (s *platformStore) keys(prefix string) []string {
	s.t.Helper()
	var out []string
	token := ""
	for {
		page, err := s.client.ListObjects(s.t.Context(), s.bucket, prefix, "", 1000, token)
		if err != nil {
			s.t.Fatalf("listing s3://%s/%s: %v", s.bucket, prefix, err)
		}
		for _, o := range page.Objects {
			out = append(out, o.Key)
		}
		if !page.IsTruncated || page.NextContinueToken == "" {
			return out
		}
		token = page.NextContinueToken
	}
}

// remove deletes key.
func (s *platformStore) remove(key string) {
	s.t.Helper()
	if err := s.delete(key); err != nil {
		s.t.Fatalf("deleting s3://%s/%s: %v", s.bucket, key, err)
	}
}

// delete deletes key and reports what the store answered, for a cleanup that
// records a failure rather than stopping.
func (s *platformStore) delete(key string) error {
	return s.client.DeleteObject(context.Background(), s.bucket, key) //nolint:wrapcheck // the store's own answer
}

// isMissing reports whether a store error says the object is not there.
func isMissing(err error) bool {
	var nf interface{ ErrorCode() string }
	if errors.As(err, &nf) {
		switch nf.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return containsAny(err.Error(), "NoSuchKey", "NotFound", "StatusCode: 404", "not found")
}
