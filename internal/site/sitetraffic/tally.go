// Package sitetraffic records each site's traffic and latency by environment
// and by replica in the platform's own database (#2647), so a site's people
// see it in the portal and in manage_site with no Prometheus backend and no
// grant beyond seeing the site.
//
// On the request path the gateway adds to an in-memory Tally: atomic
// increments of one status-class count and one fixed latency bucket, for the
// route's (kind, site or application, environment) and for the replica. There
// is no allocation, no lock shared across requests, and no database or network
// call. Once a minute a Flusher adds each replica's counts into shared minute
// and hour rows, one per pair that had traffic, and into the replica's own
// rows: a fixed handful of statements however many sites or requests. A key
// comes from the resolved route and the process name only; nothing the
// request carried (path, query, host, client address, cookies, user agent) is
// ever held.
package sitetraffic

import (
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The kinds a route is counted under: a site's environment, or an
// application's page origin.
const (
	KindSite  = "site"
	KindPages = "pages"
)

// Bounds are the upper bounds of the latency buckets, in milliseconds; a
// request past the last falls in one more, unbounded.
var Bounds = [...]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// NumBuckets is how many latency buckets a row holds.
const NumBuckets = len(Bounds) + 1

// NumClasses is how many status classes a row counts: 2xx, 3xx, 4xx, 5xx and
// anything else.
const NumClasses = 5

// Key is what a request is counted under: the route's kind, the site's id or
// the application's name, and the environment ("" for a page origin).
type Key struct {
	Kind, Subject, Environment string
}

// counters is one key's counts since the last flush.
type counters struct {
	classes [NumClasses]atomic.Int64
	buckets [NumBuckets]atomic.Int64
	// idle is how many flushes in a row found it empty; read and written by
	// the flusher alone.
	idle int
}

// Counts is a drained set of counts.
type Counts struct {
	Classes [NumClasses]int64
	Buckets [NumBuckets]int64
}

// Total is the requests counted.
func (c Counts) Total() int64 {
	var n int64
	for _, v := range c.Classes {
		n += v
	}
	return n
}

// Add adds o into c.
func (c *Counts) Add(o Counts) {
	for i := range c.Classes {
		c.Classes[i] += o.Classes[i]
	}
	for i := range c.Buckets {
		c.Buckets[i] += o.Buckets[i]
	}
}

func (c *counters) record(class, bucket int) {
	c.classes[class].Add(1)
	c.buckets[bucket].Add(1)
}

func (c *counters) drain() Counts {
	var out Counts
	for i := range c.classes {
		out.Classes[i] = c.classes[i].Swap(0)
	}
	for i := range c.buckets {
		out.Buckets[i] = c.buckets[i].Swap(0)
	}
	return out
}

// MaxKeys bounds the keys a replica holds: past it a new key's requests are
// counted as dropped, so memory stays bounded however many sites a replica
// serves.
const MaxKeys = 20000

// idleFlushes is how many empty flushes in a row before a key is let go.
const idleFlushes = 10

// Tally is one replica's counts since the last flush.
type Tally struct {
	enabled atomic.Bool
	// keys is read without a lock on the request path: a copy-on-write map
	// replaced whole when a key is added or let go.
	keys    atomic.Pointer[map[Key]*counters]
	mu      sync.Mutex // serializes replacing keys
	replica counters
	dropped atomic.Int64
	// maxKeys is MaxKeys unless a test sets it lower.
	maxKeys int
}

// NewTally is an empty tally, recording.
func NewTally() *Tally {
	t := &Tally{maxKeys: MaxKeys}
	empty := map[Key]*counters{}
	t.keys.Store(&empty)
	t.enabled.Store(true)
	return t
}

// SetEnabled turns recording on or off (sites.traffic.enabled).
func (t *Tally) SetEnabled(on bool) {
	if t != nil {
		t.enabled.Store(on)
	}
}

// Enabled reports whether the tally records.
func (t *Tally) Enabled() bool { return t != nil && t.enabled.Load() }

// Record counts one request answered under k with status and duration. It is
// safe from any goroutine and, once k has been seen, allocates nothing.
func (t *Tally) Record(k Key, status int, d time.Duration) {
	if t == nil || !t.enabled.Load() {
		return
	}
	class, bucket := ClassOf(status), BucketOf(d)
	t.replica.record(class, bucket)
	if c := (*t.keys.Load())[k]; c != nil {
		c.record(class, bucket)
		return
	}
	if c := t.add(k); c != nil {
		c.record(class, bucket)
		return
	}
	t.dropped.Add(1)
}

// add holds a new key, nil past MaxKeys.
func (t *Tally) add(k Key) *counters {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur := *t.keys.Load()
	if c := cur[k]; c != nil {
		return c
	}
	if len(cur) >= t.maxKeys {
		return nil
	}
	next := make(map[Key]*counters, len(cur))
	maps.Copy(next, cur)
	c := &counters{}
	next[k] = c
	t.keys.Store(&next)
	return c
}

// Drained is what one flush takes from the tally.
type Drained struct {
	Keys    map[Key]Counts
	Replica Counts
	// Dropped is the requests not counted under a key past MaxKeys.
	Dropped int64
}

// Drain takes every count since the last drain, keys with none left out, and
// lets go of keys idle for idleFlushes drains.
func (t *Tally) Drain() Drained {
	out := Drained{Keys: map[Key]Counts{}, Replica: t.replica.drain(), Dropped: t.dropped.Swap(0)}
	var idle []Key
	for k, c := range *t.keys.Load() {
		got := c.drain()
		if got.Total() == 0 {
			c.idle++
			if c.idle >= idleFlushes {
				idle = append(idle, k)
			}
			continue
		}
		c.idle = 0
		out.Keys[k] = got
	}
	if len(idle) > 0 {
		t.forget(idle)
	}
	return out
}

// forget lets go of idle keys. A request counted under one between its drain
// and here is lost, which an idle key makes as good as never.
func (t *Tally) forget(keys []Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur := *t.keys.Load()
	next := make(map[Key]*counters, len(cur))
	maps.Copy(next, cur)
	for _, k := range keys {
		delete(next, k)
	}
	t.keys.Store(&next)
}

// The status class indexes a row counts under.
const (
	class2xx = iota
	class3xx
	class4xx
	class5xx
	classOther
)

// classWidth is how many statuses one class spans.
const classWidth = 100

// ClassOf is the status class index of an HTTP status.
func ClassOf(status int) int {
	switch status / classWidth {
	case http.StatusOK / classWidth:
		return class2xx
	case http.StatusMultipleChoices / classWidth:
		return class3xx
	case http.StatusBadRequest / classWidth:
		return class4xx
	case http.StatusInternalServerError / classWidth:
		return class5xx
	}
	return classOther
}

// BucketOf is the latency bucket index of a duration.
func BucketOf(d time.Duration) int {
	ms := float64(d) / float64(time.Millisecond)
	for i, b := range Bounds {
		if ms <= b {
			return i
		}
	}
	return len(Bounds)
}

// Percentile is the latency in milliseconds at q (0 to 1) of the requests in
// buckets, read linearly within the bucket it falls in; 0 for none. A request
// past the last bound reads as that bound.
func Percentile(buckets [NumBuckets]int64, q float64) float64 {
	var total int64
	for _, n := range buckets {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := q * float64(total)
	var seen int64
	for i, n := range buckets {
		if n == 0 {
			continue
		}
		if float64(seen+n) >= rank {
			if i == len(Bounds) {
				return Bounds[len(Bounds)-1]
			}
			var lo float64
			if i > 0 {
				lo = Bounds[i-1]
			}
			frac := (rank - float64(seen)) / float64(n)
			return lo + frac*(Bounds[i]-lo)
		}
		seen += n
	}
	return Bounds[len(Bounds)-1]
}
