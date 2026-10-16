// Package whconfig is the webhooks: section of the platform configuration
// (#1870). Sources themselves are administrator-managed records in the
// database; this section only decides which replicas receive and which
// compact, and paces the compactor.
package whconfig

import (
	"errors"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/compactor"
	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
)

// Config is the webhooks: section. Every field's zero value is its default,
// so a deployment sets nothing.
type Config struct {
	Receiver  ReceiverConfig  `yaml:"receiver"`
	Compactor CompactorConfig `yaml:"compactor"`
}

// ReceiverConfig decides whether this replica serves /hooks/.
type ReceiverConfig struct {
	// Enabled defaults to on. A deployment that keeps bursts off the
	// replicas serving MCP and the portal turns it off there and runs a
	// receiver-only deployment of the same image behind its own Ingress.
	Enabled *bool `yaml:"enabled"`
	// Address, when set, also serves the receiver on a listener of its own,
	// so it can sit behind its own Service. The main listener serves it
	// either way.
	Address string `yaml:"address"`
	// WriteTimeout bounds writing one segment to object storage. A request
	// whose segment has not been written by then is answered 503.
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// Spool, when its directory is set, answers a request once its events
	// are synced to this replica's disk and drains them to object storage
	// behind the answer (#2121).
	Spool SpoolConfig `yaml:"spool"`
}

// SpoolConfig is the receiver's local write-ahead spool. Off until Dir is
// set: the spool needs a writable volume, which a read-only root file system
// does not have.
type SpoolConfig struct {
	// Dir is the spool's directory, one per replica. An emptyDir keeps what
	// it holds across a container restart; a volume of the replica's own
	// (a StatefulSet's volumeClaimTemplates) keeps it across rescheduling.
	Dir string `yaml:"dir"`
	// MaxBytes bounds the bytes the spool holds. Past it a request is
	// answered 503 with Retry-After. 0 takes 1 GiB.
	MaxBytes int64 `yaml:"max_bytes"`
	// MaxAge bounds how long the oldest event may wait to be drained. Past
	// it a request is answered 503 with Retry-After. 0 takes an hour.
	MaxAge time.Duration `yaml:"max_age"`
}

// SpoolEnabled reports whether this replica's receiver spools.
func (c Config) SpoolEnabled() bool {
	return c.ReceiverEnabled() && strings.TrimSpace(c.Receiver.Spool.Dir) != ""
}

// CompactorConfig decides whether this replica compacts and applies
// retention, and paces it.
type CompactorConfig struct {
	Enabled *bool `yaml:"enabled"`
	// Poll is how long an idle compactor waits before looking for work.
	Poll time.Duration `yaml:"poll"`
	// Grace is how long after a window ends the compactor waits for segments
	// still being written into it.
	Grace time.Duration `yaml:"grace"`
	// Lease is how long a claimed window is held from other replicas.
	Lease time.Duration `yaml:"lease"`
	// Batch is how many windows one pass claims.
	Batch int `yaml:"batch"`
	// RetryBackoff is how long a failed window is held back, multiplied by its
	// attempts, up to an hour.
	RetryBackoff time.Duration `yaml:"retry_backoff"`
	// RetentionEvery is how often retention runs.
	RetentionEvery time.Duration `yaml:"retention_every"`
	// ExpiredWindowRetentionDays is how long the record of an expired
	// window is kept (#1904). 0 takes 90; a negative value keeps them for as
	// long as their source exists.
	ExpiredWindowRetentionDays int `yaml:"expired_window_retention_days"`
	// RollupCheckEvery and RollupDeleteDelay paced the open window's rollup,
	// which deleted files a running query had listed and is gone (#2140).
	// They are accepted so a configuration that sets them still starts, and
	// RetiredKeys names them; nothing reads them.
	RollupCheckEvery  time.Duration `yaml:"rollup_check_every"`
	RollupDeleteDelay time.Duration `yaml:"rollup_delete_delay"`
}

// RetiredKeys names the settings in the section that nothing reads any more,
// for the configuration warning that tells an operator to remove them.
func (c Config) RetiredKeys() []string {
	keys := make([]string, 0, 2)
	if c.Compactor.RollupCheckEvery != 0 {
		keys = append(keys, "webhooks.compactor.rollup_check_every")
	}
	if c.Compactor.RollupDeleteDelay != 0 {
		keys = append(keys, "webhooks.compactor.rollup_delete_delay")
	}
	return keys
}

// ReceiverEnabled reports whether this replica serves /hooks/.
func (c Config) ReceiverEnabled() bool {
	return c.Receiver.Enabled == nil || *c.Receiver.Enabled
}

// CompactorEnabled reports whether this replica compacts.
func (c Config) CompactorEnabled() bool {
	return c.Compactor.Enabled == nil || *c.Compactor.Enabled
}

// day is the unit the *_retention_days settings count in.
const day = 24 * time.Hour

// errNegative is a pacing value below zero.
var errNegative = errors.New("webhooks: write_timeout, spool.max_bytes, spool.max_age, poll, grace, lease, batch, retry_backoff and retention_every cannot be negative")

// Validate refuses a section that cannot pace anything.
func (c Config) Validate() error {
	for _, d := range []time.Duration{
		c.Receiver.WriteTimeout, c.Receiver.Spool.MaxAge, c.Compactor.Poll, c.Compactor.Grace, c.Compactor.Lease,
		c.Compactor.RetryBackoff, c.Compactor.RetentionEvery,
	} {
		if d < 0 {
			return errNegative
		}
	}
	if c.Compactor.Batch < 0 || c.Receiver.Spool.MaxBytes < 0 {
		return errNegative
	}
	return nil
}

// Tuning is the compactor's pacing, defaults applied by the compactor.
func (c Config) Tuning() compactor.Tuning {
	return compactor.Tuning{
		Poll: c.Compactor.Poll, Lease: c.Compactor.Lease, Batch: c.Compactor.Batch,
		Grace: c.Compactor.Grace, RetryBackoff: c.Compactor.RetryBackoff,
		RetentionEvery:     c.Compactor.RetentionEvery,
		ExpiredWindowsKept: time.Duration(c.Compactor.ExpiredWindowRetentionDays) * day,
	}
}

// WriteTimeout is the receiver's segment write bound.
func (c Config) WriteTimeout() time.Duration {
	if c.Receiver.WriteTimeout > 0 {
		return c.Receiver.WriteTimeout
	}
	return receiver.DefaultWriteTimeout
}
