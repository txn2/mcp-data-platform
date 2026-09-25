// Package whconfig is the webhooks: section of the platform configuration
// (#1870). Sources themselves are administrator-managed records in the
// database; this section only decides which replicas receive and which
// compact, and paces the compactor.
package whconfig

import (
	"errors"
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
}

// ReceiverEnabled reports whether this replica serves /hooks/.
func (c Config) ReceiverEnabled() bool {
	return c.Receiver.Enabled == nil || *c.Receiver.Enabled
}

// CompactorEnabled reports whether this replica compacts.
func (c Config) CompactorEnabled() bool {
	return c.Compactor.Enabled == nil || *c.Compactor.Enabled
}

// errNegative is a pacing value below zero.
var errNegative = errors.New("webhooks: write_timeout, poll, grace, lease, batch, retry_backoff and retention_every cannot be negative")

// Validate refuses a section that cannot pace anything.
func (c Config) Validate() error {
	for _, d := range []time.Duration{
		c.Receiver.WriteTimeout, c.Compactor.Poll, c.Compactor.Grace, c.Compactor.Lease,
		c.Compactor.RetryBackoff, c.Compactor.RetentionEvery,
	} {
		if d < 0 {
			return errNegative
		}
	}
	if c.Compactor.Batch < 0 {
		return errNegative
	}
	return nil
}

// Tuning is the compactor's pacing, defaults applied by the compactor.
func (c Config) Tuning() compactor.Tuning {
	return compactor.Tuning{
		Poll: c.Compactor.Poll, Lease: c.Compactor.Lease, Batch: c.Compactor.Batch,
		Grace: c.Compactor.Grace, RetryBackoff: c.Compactor.RetryBackoff,
		RetentionEvery: c.Compactor.RetentionEvery,
	}
}

// WriteTimeout is the receiver's segment write bound.
func (c Config) WriteTimeout() time.Duration {
	if c.Receiver.WriteTimeout > 0 {
		return c.Receiver.WriteTimeout
	}
	return receiver.DefaultWriteTimeout
}
