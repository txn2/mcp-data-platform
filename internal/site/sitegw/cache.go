package sitegw

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Item bounds: what is held in memory, and what is written to disk at all. A
// larger file is read from object storage in ranges and not cached.
const (
	maxMemoryItem = int64(8 << 20)
	maxDiskItem   = int64(64 << 20)
	// diskEvictTo is how full a pass leaves the disk cache, as a percentage.
	diskEvictTo = 90
	// wholePercent is 100%, what a percentage is taken of.
	wholePercent = 100
	diskPerm     = 0o700
	filePerm     = 0o600
)

// cache is one replica's memory and disk copy of served files. A key names
// content that never changes (a file's digest and encoding, or an asset
// version's object), so nothing here is ever stale and nothing is invalidated:
// a promote changes which keys are asked for.
type cache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    *list.List
	used     int64
	limit    int64
	dir      string
	diskUsed int64
	diskMax  int64
}

type cacheEntry struct {
	key  string
	body []byte
}

func newCache(memoryBytes int64, dir string, diskBytes int64) *cache {
	c := &cache{entries: map[string]*list.Element{}, order: list.New(), limit: memoryBytes, dir: dir, diskMax: diskBytes}
	if dir == "" {
		return c
	}
	if err := os.MkdirAll(dir, diskPerm); err != nil {
		slog.Warn("sites: the disk cache is off", "dir", logsan.SanitizeForLog(dir), "error", logsan.SanitizeForLog(err.Error()))
		c.dir = ""
		return c
	}
	c.diskUsed = c.scanDisk()
	return c
}

// get is a held copy's bytes.
func (c *cache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	entry, _ := el.Value.(*cacheEntry)
	return entry.body, true
}

// put holds body in memory, evicting the least recently read past the limit.
func (c *cache) put(key string, body []byte) {
	if int64(len(body)) > maxMemoryItem || int64(len(body)) > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	c.entries[key] = c.order.PushFront(&cacheEntry{key: key, body: body})
	c.used += int64(len(body))
	for c.used > c.limit {
		last := c.order.Back()
		entry, _ := last.Value.(*cacheEntry)
		c.order.Remove(last)
		delete(c.entries, entry.key)
		c.used -= int64(len(entry.body))
	}
}

// diskPath is where a key is written. The name is a digest, so a key can
// never name a path of its own.
func (c *cache) diskPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:]))
}

// readDisk opens a disk copy, nil for none.
func (c *cache) readDisk(key string) *os.File {
	if c.dir == "" {
		return nil
	}
	p := c.diskPath(key)
	f, err := os.Open(p) // #nosec G304 -- the name is a digest of the key, under the operator's cache directory
	if err != nil {
		return nil
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return f
}

// writeDisk stores a copy, by a rename so a reader never sees half of one.
func (c *cache) writeDisk(key string, body []byte) {
	if c.dir == "" || int64(len(body)) > maxDiskItem {
		return
	}
	final := c.diskPath(key)
	tmp, err := os.CreateTemp(c.dir, "tmp-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		return
	}
	_ = os.Chmod(tmp.Name(), filePerm)
	if os.Rename(tmp.Name(), final) != nil { // #nosec G703 -- both names are under the cache directory, the target a digest
		return
	}
	c.mu.Lock()
	c.diskUsed += int64(len(body))
	over := c.diskUsed > c.diskMax
	c.mu.Unlock()
	if over {
		c.evictDisk()
	}
}

func (c *cache) scanDisk() int64 {
	var total int64
	for _, f := range c.diskFiles() {
		total += f.size
	}
	return total
}

type diskFile struct {
	path string
	size int64
	used time.Time
}

func (c *cache) diskFiles() []diskFile {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil
	}
	out := make([]diskFile, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		out = append(out, diskFile{path: filepath.Join(c.dir, e.Name()), size: info.Size(), used: info.ModTime()})
	}
	return out
}

// evictDisk removes the least recently read files until the cache is back
// under its limit, and recounts what is there.
func (c *cache) evictDisk() {
	files := c.diskFiles()
	slices.SortFunc(files, func(a, b diskFile) int { return a.used.Compare(b.used) })
	var total int64
	for _, f := range files {
		total += f.size
	}
	target := c.diskMax * diskEvictTo / wholePercent
	for _, f := range files {
		if total <= target {
			break
		}
		if os.Remove(f.path) == nil { // #nosec G703 -- a file this cache listed from its own directory
			total -= f.size
		}
	}
	c.mu.Lock()
	c.diskUsed = total
	c.mu.Unlock()
}
