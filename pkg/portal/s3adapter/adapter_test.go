package s3adapter

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// TestCountingReader verifies the counting reader the streaming-upload
// adapter uses to report bytes written tallies exactly the bytes pulled
// through it and transparently passes the underlying error/EOF.
func TestCountingReader(t *testing.T) {
	const payload = "hello, streaming world"
	cr := &countingReader{r: strings.NewReader(payload)}

	n, err := io.Copy(io.Discard, cr)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("io.Copy returned %d; want %d", n, len(payload))
	}
	if cr.n != int64(len(payload)) {
		t.Errorf("countingReader.n = %d; want %d", cr.n, len(payload))
	}
}

func TestCountingReader_PartialReads(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("abcdef")}
	buf := make([]byte, 4)

	read1, _ := cr.Read(buf)
	if read1 != 4 || cr.n != 4 {
		t.Fatalf("first read = %d, n = %d; want 4, 4", read1, cr.n)
	}
	read2, _ := cr.Read(buf)
	if read2 != 2 || cr.n != 6 {
		t.Fatalf("second read = %d, n = %d; want 2, 6", read2, cr.n)
	}
	if _, err := cr.Read(buf); !errors.Is(err, io.EOF) {
		t.Errorf("third read err = %v; want io.EOF", err)
	}
}

// fakeS3API is an in-memory API for exercising the adapter without a real S3
// endpoint. It drains the streamed body so the test can assert the bytes that
// would have been uploaded, records the last PutObject input, and can inject
// an error per method to exercise the adapter's error-wrapping paths.
type fakeS3API struct {
	streamErr  error
	streamed   []byte
	streamedCT string

	putErr   error
	putInput *s3client.PutObjectInput

	getErr  error
	getBody []byte
	getCT   string

	delErr   error
	closeErr error
}

func (f *fakeS3API) PutObject(_ context.Context, in *s3client.PutObjectInput) (*s3client.PutObjectOutput, error) {
	f.putInput = in
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &s3client.PutObjectOutput{}, nil
}

func (f *fakeS3API) PutObjectStream(_ context.Context, in *s3client.PutObjectStreamInput) (*s3client.PutObjectOutput, error) {
	b, _ := io.ReadAll(in.Body) // drains through the adapter's countingReader
	f.streamed = b
	f.streamedCT = in.ContentType
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return &s3client.PutObjectOutput{ETag: "etag"}, nil
}

func (f *fakeS3API) GetObject(_ context.Context, _, _ string) (*s3client.ObjectContent, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &s3client.ObjectContent{Body: f.getBody, ContentType: f.getCT}, nil
}
func (f *fakeS3API) DeleteObject(_ context.Context, _, _ string) error { return f.delErr }
func (f *fakeS3API) Close() error                                      { return f.closeErr }

func TestS3ClientAdapter_PutObjectStream(t *testing.T) {
	fake := &fakeS3API{}
	adapter := &ClientAdapter{client: fake}

	const payload = "streamed-export-bytes"
	n, err := adapter.PutObjectStream(context.Background(), "bucket", "key", strings.NewReader(payload), "application/json")
	if err != nil {
		t.Fatalf("PutObjectStream: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("returned size = %d; want %d", n, len(payload))
	}
	if string(fake.streamed) != payload {
		t.Errorf("streamed bytes = %q; want %q", fake.streamed, payload)
	}
	if fake.streamedCT != "application/json" {
		t.Errorf("content type = %q; want application/json", fake.streamedCT)
	}
}

func TestNew(t *testing.T) {
	// New wraps a concrete *s3client.Client; passing nil is enough to cover the
	// constructor and confirm it returns a usable, non-nil adapter.
	if got := New(nil); got == nil {
		t.Fatal("New returned nil")
	}
}

func TestS3ClientAdapter_PutObject(t *testing.T) {
	fake := &fakeS3API{}
	adapter := &ClientAdapter{client: fake}

	err := adapter.PutObject(context.Background(), "bucket", "key", []byte("payload"), "text/plain")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if fake.putInput == nil {
		t.Fatal("PutObject did not reach the underlying client")
	}
	if fake.putInput.Bucket != "bucket" || fake.putInput.Key != "key" {
		t.Errorf("PutObject input = %q/%q; want bucket/key", fake.putInput.Bucket, fake.putInput.Key)
	}
	if string(fake.putInput.Body) != "payload" || fake.putInput.ContentType != "text/plain" {
		t.Errorf("PutObject body/CT = %q/%q; want payload/text/plain", fake.putInput.Body, fake.putInput.ContentType)
	}
}

func TestS3ClientAdapter_PutObject_Error(t *testing.T) {
	fake := &fakeS3API{putErr: errors.New("s3 unavailable")}
	adapter := &ClientAdapter{client: fake}

	err := adapter.PutObject(context.Background(), "bucket", "key", []byte("x"), "text/plain")
	if err == nil || !strings.Contains(err.Error(), "s3 put") {
		t.Errorf("PutObject error = %v; want it wrapped with 's3 put'", err)
	}
}

func TestS3ClientAdapter_GetObject(t *testing.T) {
	fake := &fakeS3API{getBody: []byte("contents"), getCT: "application/json"}
	adapter := &ClientAdapter{client: fake}

	body, ct, err := adapter.GetObject(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(body) != "contents" || ct != "application/json" {
		t.Errorf("GetObject = %q/%q; want contents/application/json", body, ct)
	}
}

func TestS3ClientAdapter_GetObject_Error(t *testing.T) {
	fake := &fakeS3API{getErr: errors.New("missing")}
	adapter := &ClientAdapter{client: fake}

	if _, _, err := adapter.GetObject(context.Background(), "bucket", "key"); err == nil ||
		!strings.Contains(err.Error(), "s3 get") {
		t.Errorf("GetObject error = %v; want it wrapped with 's3 get'", err)
	}
}

func TestS3ClientAdapter_DeleteObject(t *testing.T) {
	adapter := &ClientAdapter{client: &fakeS3API{}}
	if err := adapter.DeleteObject(context.Background(), "bucket", "key"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	failing := &ClientAdapter{client: &fakeS3API{delErr: errors.New("denied")}}
	if err := failing.DeleteObject(context.Background(), "bucket", "key"); err == nil ||
		!strings.Contains(err.Error(), "s3 delete") {
		t.Errorf("DeleteObject error = %v; want it wrapped with 's3 delete'", err)
	}
}

func TestS3ClientAdapter_Close(t *testing.T) {
	adapter := &ClientAdapter{client: &fakeS3API{}}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	failing := &ClientAdapter{client: &fakeS3API{closeErr: errors.New("boom")}}
	if err := failing.Close(); err == nil || !strings.Contains(err.Error(), "closing s3 client") {
		t.Errorf("Close error = %v; want it wrapped with 'closing s3 client'", err)
	}
}

func TestS3ClientAdapter_PutObjectStream_Error(t *testing.T) {
	fake := &fakeS3API{streamErr: errors.New("s3 unavailable")}
	adapter := &ClientAdapter{client: fake}

	n, err := adapter.PutObjectStream(context.Background(), "bucket", "key", strings.NewReader("data"), "text/plain")
	if err == nil {
		t.Fatal("want error from failed stream upload")
	}
	if !strings.Contains(err.Error(), "s3 put stream") {
		t.Errorf("error = %v; want it wrapped with 's3 put stream'", err)
	}
	// The adapter still reports the bytes that were pulled before failure.
	if n != int64(len("data")) {
		t.Errorf("returned size = %d; want %d", n, len("data"))
	}
}
