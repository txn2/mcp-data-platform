package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// streamingFake is a connection client that also uploads through the
// multipart uploader, as mcp-s3's *client.Client does.
type streamingFake struct {
	*fakeS3
	streamErr error
	streamed  *s3client.PutObjectStreamInput
	body      []byte
}

func (f *streamingFake) PutObjectStream(_ context.Context, in *s3client.PutObjectStreamInput) (*s3client.PutObjectOutput, error) {
	f.streamed = in
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the streamed body: %w", err)
	}
	f.body = b
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return &s3client.PutObjectOutput{ETag: "stream-etag", VersionID: "v1"}, nil
}

// A put on a client that has the multipart uploader goes through it, with the
// body, type and metadata intact, and never as a single PutObject (#1863).
func TestWriteObject_UsesTheMultipartUploader(t *testing.T) {
	fake := &streamingFake{fakeS3: newFakeS3("lake")}
	out, err := writeObject(context.Background(), fake, &s3client.PutObjectInput{
		Bucket: "acme-lake", Key: "drop/q1.csv", Body: []byte("a,b\n1,2\n"),
		ContentType: "text/csv", Metadata: map[string]string{"run": "r1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "stream-etag", out.ETag)
	assert.Equal(t, "v1", out.VersionID)
	assert.Nil(t, fake.lastPut, "a streaming client is never sent a single PutObject")
	require.NotNil(t, fake.streamed)
	assert.Equal(t, "acme-lake", fake.streamed.Bucket)
	assert.Equal(t, "drop/q1.csv", fake.streamed.Key)
	assert.Equal(t, "text/csv", fake.streamed.ContentType)
	assert.Equal(t, map[string]string{"run": "r1"}, fake.streamed.Metadata)
	assert.Equal(t, []byte("a,b\n1,2\n"), fake.body)
}

func TestWriteObject_StreamError(t *testing.T) {
	fake := &streamingFake{fakeS3: newFakeS3("lake"), streamErr: errors.New("chunk too big")}
	_, err := writeObject(context.Background(), fake, &s3client.PutObjectInput{Bucket: "acme-lake", Key: "k", Body: []byte("x")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk too big")
}

// A client without the uploader still stores the object as one PutObject.
func TestWriteObject_SinglePutWithoutUploader(t *testing.T) {
	fake := newFakeS3("lake")
	_, err := writeObject(context.Background(), fake, &s3client.PutObjectInput{Bucket: "acme-lake", Key: "k", Body: []byte("x")})
	require.NoError(t, err)
	assert.Equal(t, []byte("x"), fake.buckets["acme-lake"]["k"].body)

	fake.fail = errors.New("denied")
	_, err = writeObject(context.Background(), fake, &s3client.PutObjectInput{Bucket: "acme-lake", Key: "k", Body: []byte("x")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
}
