package sitefs

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tree() *FS {
	bodies := map[string]string{"a/b/stored.txt": "kept", "new.txt": "new", "a/c.txt": "c"}
	entries := make([]Entry, 0, len(bodies))
	for p, b := range bodies {
		entries = append(entries, Entry{Path: p, Size: int64(len(b))})
	}
	return New(entries, func(name string) ([]byte, error) { return []byte(bodies[name]), nil })
}

func TestItIsAFileSystem(t *testing.T) {
	require.NoError(t, fstest.TestFS(tree(), "a/b/stored.txt", "new.txt", "a/c.txt"))
}

func TestWhatIsMissing(t *testing.T) {
	f := tree()
	_, err := f.Stat("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = f.ReadFile("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = f.ReadDir("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = f.Open("/not/valid")
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestAFileIsOnlyReadWhenOpened(t *testing.T) {
	reads := 0
	failure := errors.New("down")
	f := New([]Entry{{Path: "a.txt", Size: 1}}, func(string) ([]byte, error) { reads++; return nil, failure })
	info, err := f.Stat("a.txt")
	require.NoError(t, err)
	assert.EqualValues(t, 1, info.Size())
	assert.Zero(t, reads)
	_, err = f.Open("a.txt")
	require.ErrorIs(t, err, failure)
	assert.Equal(t, 1, reads)
}

func TestTheCornersOfTheContract(t *testing.T) {
	f := tree()
	stat, err := f.Stat("new.txt")
	require.NoError(t, err)
	assert.Nil(t, stat.Sys())
	dir, err := f.Open("a")
	require.NoError(t, err)
	_, err = dir.Read(make([]byte, 1))
	require.ErrorIs(t, err, fs.ErrInvalid)
}
