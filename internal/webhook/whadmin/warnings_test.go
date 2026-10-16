package whadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// TestWarnings covers #2134's partition read: a fast read warns of nothing; a
// slow read names the metastore cache settings; a read that fails says so.
// A busy source's settings warn of nothing since the open window is no longer
// rolled up (#2140).
func TestWarnings(t *testing.T) {
	src := input()

	r := newRig()
	assert.Empty(t, r.svc.Warnings(ctx, src), "the defaults on one replica, read fast")
	assert.NotNil(t, r.svc.Warnings(ctx, src), "no warnings is an empty list")

	r.tables.partitionRead = 57 * time.Second
	got := r.svc.Warnings(ctx, src)
	if assert.Len(t, got, 1) {
		assert.Contains(t, got[0], "took 57 s or more")
		assert.Contains(t, got[0], "hive.metastore-cache-ttl")
	}

	r = newRig()
	r.tables.err["partitions"] = errors.New("reading the partitions of webhook_esp_raw: timed out")
	got = r.svc.Warnings(ctx, src)
	if assert.Len(t, got, 1) {
		assert.Contains(t, got[0], "timed out")
	}

	r = newRig()
	r.tables.err["target"] = errors.New("no scratch target")
	got = r.svc.Warnings(ctx, src)
	if assert.Len(t, got, 1) {
		assert.Contains(t, got[0], "could not be read")
	}

	r = newRig()
	busy := src
	busy.Config = whsource.Config{FlushMaxIntervalMS: 200}
	r.windows.spools = []whstore.SpoolState{{Replica: "a", UpdatedAt: now}, {Replica: "b", UpdatedAt: now}}
	assert.Empty(t, r.svc.Warnings(ctx, busy), "no segment estimate is made")
}

// TestWarningsBoundThePartitionRead: a read still running at the bound is
// reported slow, and the save does not wait for it.
func TestWarningsBoundThePartitionRead(t *testing.T) {
	r := newRig()
	r.tables.partitionHangs = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := r.svc.Warnings(ctx, input())
	if assert.Len(t, got, 1) {
		assert.Contains(t, got[0], "took 15 s or more")
	}
}
