package sitegw

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSeeker(objects *memObjects, size int64) *objectSeeker {
	objects.sized = true
	return &objectSeeker{ctx: context.Background(), objects: objects, bucket: testBucket, key: "big", size: size}
}

func wantPattern(off, length int64) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = patternByte(off + int64(i))
	}
	return out
}

func TestObjectSeeker_Seek(t *testing.T) {
	tests := []struct {
		name    string
		start   int64
		offset  int64
		whence  int
		want    int64
		wantErr string
	}{
		{name: "from the start", offset: 7, whence: io.SeekStart, want: 7},
		{name: "from the current position", start: 10, offset: 5, whence: io.SeekCurrent, want: 15},
		{name: "backward from the current position", start: 10, offset: -4, whence: io.SeekCurrent, want: 6},
		{name: "from the end", offset: -10, whence: io.SeekEnd, want: 90},
		{name: "past the end is allowed", offset: 200, whence: io.SeekStart, want: 200},
		{name: "before the start is refused", offset: -1, whence: io.SeekStart, wantErr: "negative position"},
		{name: "an unknown whence is refused", whence: 9, wantErr: "invalid whence"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSeeker(newMemObjects(), 100)
			s.off = tt.start
			got, err := s.Seek(tt.offset, tt.whence)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, tt.start, s.off, "a refused seek moves nothing")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want, s.off)
		})
	}
}

func TestObjectSeeker_ReadsAcrossAChunkBoundary(t *testing.T) {
	objects := newMemObjects()
	size := rangeChunk*2 + 100
	s := newSeeker(objects, size)

	got, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, size, int64(len(got)))
	assert.Equal(t, wantPattern(0, size), got)
	assert.Equal(t, []int64{0, rangeChunk, rangeChunk * 2}, objects.ranges, "one fetch per chunk")
}

func TestObjectSeeker_TheLastChunkAsksOnlyForWhatIsLeft(t *testing.T) {
	objects := newMemObjects()
	s := newSeeker(objects, 10)
	buf := make([]byte, 10)
	n, err := s.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, wantPattern(0, 10), buf)
}

func TestObjectSeeker_ASeekInsideTheHeldChunkFetchesNothing(t *testing.T) {
	objects := newMemObjects()
	s := newSeeker(objects, rangeChunk*3)

	buf := make([]byte, 16)
	_, err := s.Read(buf)
	require.NoError(t, err)
	require.Len(t, objects.ranges, 1)

	_, err = s.Seek(100, io.SeekStart)
	require.NoError(t, err)
	_, err = io.ReadFull(s, buf)
	require.NoError(t, err)
	assert.Equal(t, wantPattern(100, 16), buf)
	assert.Len(t, objects.ranges, 1, "the position stayed in the held chunk")
}

func TestObjectSeeker_ASeekOutsideTheHeldChunkFetchesThere(t *testing.T) {
	objects := newMemObjects()
	s := newSeeker(objects, rangeChunk*3)

	buf := make([]byte, 8)
	_, err := s.Read(buf)
	require.NoError(t, err)

	_, err = s.Seek(rangeChunk+50, io.SeekStart)
	require.NoError(t, err)
	_, err = io.ReadFull(s, buf)
	require.NoError(t, err)
	assert.Equal(t, wantPattern(rangeChunk+50, 8), buf)

	_, err = s.Seek(10, io.SeekStart)
	require.NoError(t, err)
	_, err = io.ReadFull(s, buf)
	require.NoError(t, err)
	assert.Equal(t, wantPattern(10, 8), buf)
	assert.Equal(t, []int64{0, rangeChunk + 50, 10}, objects.ranges, "a position before the held chunk fetches too")
}

func TestObjectSeeker_ReadAtOrPastTheEndIsEOF(t *testing.T) {
	s := newSeeker(newMemObjects(), 10)
	_, err := s.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	n, err := s.Read(make([]byte, 4))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, io.EOF)
}

func TestObjectSeeker_AFetchErrorIsSaid(t *testing.T) {
	objects := newMemObjects()
	objects.getErr = errors.New("connection reset")
	s := newSeeker(objects, 10)
	_, err := s.Read(make([]byte, 4))
	require.ErrorContains(t, err, "reading a range")
	assert.ErrorIs(t, err, objects.getErr)
}

func TestObjectSeeker_AnEmptyAnswerIsAnUnexpectedEOF(t *testing.T) {
	objects := newMemObjects()
	s := &objectSeeker{ctx: context.Background(), objects: objects, bucket: testBucket, key: "absent", size: 10}
	_, err := s.Read(make([]byte, 4))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
