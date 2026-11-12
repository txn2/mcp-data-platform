package configwarn

import (
	"context"
	"regexp"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// The tools that read a bucket a connection reaches: a persona holding either
// on a connection holds the connection's reach (#2616).
var s3ReadTools = []string{"s3_list", "s3_object"}

// backupBucket matches a bucket name that reads as holding backups: a word of
// its own (between the start, the end, a hyphen, an underscore or a dot) that
// names a backup or a backup tool.
var backupBucket = regexp.MustCompile(`(?i)(^|[-_.])(backups?|bak|pgbackrest|barman|velero|wal-?g)([-_.]|$)`)

// s3ReachTimeout bounds one connection's listing at boot.
const s3ReachTimeout = 15 * time.Second

// bucketReacher is an S3 toolkit's view the reach check needs: its
// connections and the buckets each one's credentials list.
type bucketReacher interface {
	BucketNames(ctx context.Context, connection string) ([]string, error)
	ListConnections() []connectionInfo
}

// connectionInfo is a connection as the reach check reads it.
type connectionInfo struct{ Name string }

// reacher adapts an S3 toolkit to bucketReacher.
type reacher struct{ tk *s3kit.Toolkit }

// BucketNames is the buckets a connection's credentials list.
func (r reacher) BucketNames(ctx context.Context, connection string) ([]string, error) {
	return r.tk.BucketNames(ctx, connection) //nolint:wrapcheck // the toolkit's own sentence
}

// ListConnections is the toolkit's connections by name.
func (r reacher) ListConnections() []connectionInfo {
	conns := r.tk.ListConnections()
	out := make([]connectionInfo, 0, len(conns))
	for _, c := range conns {
		out = append(out, connectionInfo{Name: c.Name})
	}
	return out
}

// CheckS3Reach warns of each S3 connection a persona may read buckets through
// whose credentials list a bucket the platform keeps its own data in, or one
// named for backups (#2616). The S3 tools refuse the platform's buckets
// whatever the credentials reach; this tells the operator the credentials
// still reach them, which no tool can take back. It lists over the network,
// so the caller runs it apart from startup.
func CheckS3Reach(ctx context.Context, reg *registry.Registry, personas *persona.Registry) {
	if reg == nil || personas == nil {
		return
	}
	for _, tk := range reg.GetByKind("s3") {
		if s3tk, ok := tk.(*s3kit.Toolkit); ok {
			checkReacher(ctx, reacher{tk: s3tk}, personas)
		}
	}
}

func checkReacher(ctx context.Context, r bucketReacher, personas *persona.Registry) {
	for _, c := range r.ListConnections() {
		if !granted(personas, c.Name) {
			continue
		}
		listCtx, cancel := context.WithTimeout(ctx, s3ReachTimeout)
		buckets, err := r.BucketNames(listCtx, c.Name)
		cancel()
		if err != nil {
			continue
		}
		warnReach(ctx, c.Name, buckets)
	}
}

// granted reports whether any persona may call a bucket-reading tool on the
// connection.
func granted(reg *persona.Registry, connection string) bool {
	filter := persona.NewToolFilter(reg)
	return slices.ContainsFunc(reg.All(), func(p *persona.Persona) bool {
		if !filter.IsConnectionAllowed(p, connection) {
			return false
		}
		return slices.ContainsFunc(s3ReadTools, func(tool string) bool { return reg.ToolAllowed(p.Name, tool) })
	})
}

// warnReach warns once for each platform bucket and each backup bucket a
// granted connection lists. Bucket names come from the object store, so they
// are sanitized like any upstream text.
func warnReach(ctx context.Context, connection string, buckets []string) {
	for _, b := range buckets {
		if r, ok := s3kit.IsReserved(b); ok {
			Warn(ctx, CodeS3ReachesPlatformBucket, "an S3 connection a persona may read buckets through reaches one of the platform's own buckets",
				"connection", logsan.SanitizeForLog(connection),
				"bucket", logsan.SanitizeForLog(b),
				"holds", r.Holds,
				"hint", "the S3 tools refuse it, but the credentials still reach it: give the connection credentials scoped to business buckets",
			)
			continue
		}
		if backupBucket.MatchString(b) {
			Warn(ctx, CodeS3ReachesBackupBucket, "an S3 connection a persona may read buckets through reaches a bucket named for backups",
				"connection", logsan.SanitizeForLog(connection),
				"bucket", logsan.SanitizeForLog(b),
				"hint", "anyone holding s3_list or s3_object on this connection can read it: give the connection credentials that cannot list it",
			)
		}
	}
}
