package thumbworker

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
type Config struct {
	Enabled *bool `yaml:"enabled"`
	// RendererURL is the renderer's DevTools address, http:// or ws://.
	// Empty means DefaultRendererURL.
	RendererURL string `yaml:"renderer_url"`
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
