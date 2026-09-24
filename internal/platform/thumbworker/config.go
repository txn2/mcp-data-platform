package thumbworker

import (
	"errors"
	"fmt"
	"time"
)

// DefaultRendererURL is where the platform looks for its renderer when none is
// configured: a headless browser beside it in the same pod.
const DefaultRendererURL = "http://127.0.0.1:9222"

// Config is the deployment's `thumbnails:` section. A tile is drawn in a
// headless Chrome the platform reaches over the DevTools protocol, and the
// platform answers every request the browser makes itself, so the renderer
// needs no network of its own and never reaches back to the platform.
//
// Enabled by default (nil = enabled). With no renderer answering, tiles keep
// their content-type icons and the platform logs once that it cannot draw.
//
// The rest paces the worker (#1868). Every field's zero value is its default,
// so a deployment sets nothing.
type Config struct {
	Enabled *bool `yaml:"enabled"`
	// RendererURL is the renderer's DevTools address, http:// or ws://.
	// Empty means DefaultRendererURL.
	RendererURL string `yaml:"renderer_url"`

	// Concurrency is how many documents one replica draws at once. The
	// renderer is one browser beside the replica, so the default is 1.
	Concurrency int `yaml:"concurrency"`
	// RenderTimeout bounds drawing one variant of one document. A document
	// that has not reported itself drawn by then is recorded as not drawable.
	RenderTimeout time.Duration `yaml:"render_timeout"`
	// Batch is how many rows of each kind one pass claims. The default is
	// Concurrency: a claimed row waits for the rows ahead of it, and one
	// that waits is held from every other replica meanwhile.
	Batch int `yaml:"batch"`
	// Lease is how long a claimed row is held. It must outlast drawing the
	// batch; the default is worked out from Batch, Concurrency and
	// RenderTimeout, and a lease shorter than that is refused.
	Lease time.Duration `yaml:"lease"`
	// Poll is how long an idle worker waits before asking for work again.
	Poll time.Duration `yaml:"poll"`
	// MaxAttempts is how many times a document is tried before one that
	// never finishes -- the renderer stopped answering while it was drawn,
	// or its file could not be read -- is recorded as not drawable.
	MaxAttempts int `yaml:"max_attempts"`
	// RetryBackoff is how long a document is held back after its first
	// attempt that did not finish; each later one waits four times longer,
	// up to an hour.
	RetryBackoff time.Duration `yaml:"retry_backoff"`
}

// IsEnabled reports whether the platform draws tiles, defaulting to true.
func (c Config) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// EffectiveRendererURL is RendererURL, or the default beside the platform.
func (c Config) EffectiveRendererURL() string {
	if c.RendererURL != "" {
		return c.RendererURL
	}
	return DefaultRendererURL
}

// errNegative is a pacing value below zero, which has no meaning.
var errNegative = errors.New("thumbnails: concurrency, batch, max_attempts, render_timeout, lease, poll and retry_backoff cannot be negative")

// Tuning is the section as the worker paces itself, with every default
// applied, or why it cannot be: a negative value, or a lease the batch it
// covers would outlast.
func (c Config) Tuning() (Tuning, error) {
	for _, n := range []int{c.Concurrency, c.Batch, c.MaxAttempts} {
		if n < 0 {
			return Tuning{}, errNegative
		}
	}
	for _, d := range []time.Duration{c.RenderTimeout, c.Lease, c.Poll, c.RetryBackoff} {
		if d < 0 {
			return Tuning{}, errNegative
		}
	}
	t := Tuning{
		Concurrency: c.Concurrency, RenderTimeout: c.RenderTimeout, Batch: c.Batch,
		Lease: c.Lease, Poll: c.Poll, MaxAttempts: c.MaxAttempts, RetryBackoff: c.RetryBackoff,
	}.withDefaults()
	if need := leaseFor(t.Batch, t.Concurrency, t.RenderTimeout); t.Lease < need {
		return Tuning{}, fmt.Errorf("thumbnails: lease %s is shorter than drawing a batch of %d at concurrency %d can take (%s); "+
			"raise lease, or lower batch or render_timeout", t.Lease, t.Batch, t.Concurrency, need)
	}
	return t, nil
}
