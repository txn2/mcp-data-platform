package sitegw

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var hexName = regexp.MustCompile(`^[0-9a-f]{64}$`)

func held(c *cache, key string) bool {
	_, ok := c.get(key)
	return ok
}

func TestCache_EvictsTheLeastRecentlyReadByBytes(t *testing.T) {
	c := newCache(10, "", 0)
	c.put("a", []byte("aaaa"))
	c.put("b", []byte("bbbb"))
	require.True(t, held(c, "a"), "reading a makes b the least recent")
	c.put("c", []byte("cccc"))

	assert.False(t, held(c, "b"))
	assert.True(t, held(c, "a"))
	assert.True(t, held(c, "c"))
	assert.Equal(t, int64(8), c.used)
}

func TestCache_EvictsAsManyAsTheNewItemNeeds(t *testing.T) {
	c := newCache(10, "", 0)
	c.put("a", []byte("aaa"))
	c.put("b", []byte("bbb"))
	c.put("c", []byte("ccc"))
	c.put("d", []byte("dddddddddd"))

	assert.True(t, held(c, "d"))
	assert.False(t, held(c, "a"))
	assert.False(t, held(c, "b"))
	assert.False(t, held(c, "c"))
	assert.Equal(t, int64(10), c.used)
}

func TestCache_AnItemOverTheLimitIsNotHeld(t *testing.T) {
	c := newCache(4, "", 0)
	c.put("big", []byte("12345"))
	assert.False(t, held(c, "big"))
	assert.Zero(t, c.used)
}

func TestCache_AnItemOverTheMemoryItemBoundIsNotHeld(t *testing.T) {
	c := newCache(maxMemoryItem*2, "", 0)
	c.put("big", make([]byte, maxMemoryItem+1))
	assert.False(t, held(c, "big"))
}

func TestCache_APutOfAHeldKeyCountsOnce(t *testing.T) {
	c := newCache(100, "", 0)
	c.put("a", []byte("aaaa"))
	c.put("a", []byte("aaaa"))
	assert.Equal(t, int64(4), c.used)
}

func readAndClose(t *testing.T, f *os.File) string {
	t.Helper()
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(body)
}

func TestCache_DiskWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	c := newCache(100, dir, 1000)

	assert.Nil(t, c.readDisk("nothing"), "a key with no copy")

	c.writeDisk("k", []byte("hello"))
	f := c.readDisk("k")
	require.NotNil(t, f)
	assert.Equal(t, "hello", readAndClose(t, f))

	info, err := os.Stat(c.diskPath("k"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file stays behind")
	assert.Equal(t, int64(5), c.diskUsed)
}

func TestCache_ScansWhatADirectoryAlreadyHolds(t *testing.T) {
	dir := t.TempDir()
	newCache(100, dir, 1000).writeDisk("k", []byte("12345"))
	assert.Equal(t, int64(5), newCache(100, dir, 1000).diskUsed)
}

func TestCache_ADiskCopyOverTheItemBoundIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	c := newCache(100, dir, maxDiskItem*2)
	c.writeDisk("huge", make([]byte, maxDiskItem+1))
	assert.Nil(t, c.readDisk("huge"))
}

func TestCache_ADirectoryThatCannotBeMadeTurnsTheDiskOff(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), filePerm))

	c := newCache(100, filepath.Join(blocker, "under"), 1000)
	assert.Empty(t, c.dir)
	c.writeDisk("k", []byte("hello"))
	assert.Nil(t, c.readDisk("k"))
}

func TestCache_NoDirectoryIsNoDiskCache(t *testing.T) {
	c := newCache(100, "", 1000)
	c.writeDisk("k", []byte("hello"))
	assert.Nil(t, c.readDisk("k"))
	assert.Zero(t, c.diskUsed)
}

func age(t *testing.T, c *cache, key string, by time.Duration) {
	t.Helper()
	when := time.Now().Add(-by)
	require.NoError(t, os.Chtimes(c.diskPath(key), when, when))
}

func TestCache_EvictsTheLeastRecentlyUsedFromDisk(t *testing.T) {
	dir := t.TempDir()
	c := newCache(100, dir, 100)
	c.writeDisk("old", make([]byte, 60))
	age(t, c, "old", time.Hour)

	c.writeDisk("new", make([]byte, 60))

	assert.Nil(t, c.readDisk("old"), "past the limit, the oldest file goes")
	f := c.readDisk("new")
	require.NotNil(t, f)
	_ = f.Close()
	assert.Equal(t, int64(60), c.diskUsed)
}

func TestCache_EvictionLeavesTheDiskUnderTheTarget(t *testing.T) {
	dir := t.TempDir()
	c := newCache(100, dir, 100)
	for i, key := range []string{"a", "b", "c", "d"} {
		c.writeDisk(key, make([]byte, 30))
		age(t, c, key, time.Duration(10-i)*time.Hour)
	}
	assert.LessOrEqual(t, c.diskUsed, c.diskMax*diskEvictTo/100)
	assert.Nil(t, c.readDisk("a"))
}

func TestCache_ReadingAFileKeepsItAgainstEviction(t *testing.T) {
	dir := t.TempDir()
	c := newCache(100, dir, 100)
	c.writeDisk("read", make([]byte, 60))
	age(t, c, "read", 2*time.Hour)
	c.diskMax = 1000
	c.writeDisk("unread", make([]byte, 30))
	age(t, c, "unread", time.Hour)

	f := c.readDisk("read")
	require.NotNil(t, f)
	_ = f.Close()

	c.diskMax = 70
	c.evictDisk()

	assert.Nil(t, c.readDisk("unread"))
	f = c.readDisk("read")
	require.NotNil(t, f)
	_ = f.Close()
}

func TestCache_AKeyCanNeverNameAPathOfItsOwn(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "cache")
	c := newCache(100, dir, 1000)

	for _, key := range []string{
		"../../etc/passwd",
		"../escape",
		"/etc/passwd",
		`..\..\windows`,
		"a/b/c",
		strings.Repeat("x", 4096),
		"",
	} {
		t.Run(key, func(t *testing.T) {
			p := c.diskPath(key)
			assert.Equal(t, dir, filepath.Dir(p))
			assert.Regexp(t, hexName, filepath.Base(p))
		})
	}

	c.writeDisk("../escape", []byte("contained"))
	f := c.readDisk("../escape")
	require.NotNil(t, f)
	assert.Equal(t, "contained", readAndClose(t, f))

	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing was written beside the cache directory")
	assert.Equal(t, "cache", entries[0].Name())
}

func TestCache_ASubdirectoryIsNotCountedAsACopy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), diskPerm))
	c := newCache(100, dir, 1000)
	assert.Empty(t, c.diskFiles())
}
