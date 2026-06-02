package portal

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

// fakeS3API is an in-memory s3API for exercising the adapter without a
// real S3 endpoint. It drains the streamed body so the test can assert
// the bytes that would have been uploaded.
type fakeS3API struct {
	streamErr  error
	streamed   []byte
	streamedCT string
}

func (*fakeS3API) PutObject(_ context.Context, _ *s3client.PutObjectInput) (*s3client.PutObjectOutput, error) {
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

func (*fakeS3API) GetObject(_ context.Context, _, _ string) (*s3client.ObjectContent, error) {
	return &s3client.ObjectContent{}, nil
}
func (*fakeS3API) DeleteObject(_ context.Context, _, _ string) error { return nil }
func (*fakeS3API) Close() error                                      { return nil }

func TestS3ClientAdapter_PutObjectStream(t *testing.T) {
	fake := &fakeS3API{}
	adapter := &s3ClientAdapter{client: fake}

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

func TestS3ClientAdapter_PutObjectStream_Error(t *testing.T) {
	fake := &fakeS3API{streamErr: errors.New("s3 unavailable")}
	adapter := &s3ClientAdapter{client: fake}

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
