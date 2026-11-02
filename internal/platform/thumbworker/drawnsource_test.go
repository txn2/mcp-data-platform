package thumbworker

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2302: the tile page draws a D2 source from the SVG the platform renders,
// in the variant's theme; a source that cannot be rendered has a reason, not
// a tile.
func TestDrawnSource(t *testing.T) {
	light, why := drawnSource(context.Background(), tileSource{contentType: "text/vnd.d2", content: []byte("a -> b\n")})
	require.Empty(t, why)
	assert.Equal(t, "image/svg+xml", light.contentType)
	assert.True(t, strings.Contains(string(light.content), "<svg"))

	dark, why := drawnSource(context.Background(), tileSource{contentType: "text/vnd.d2", content: []byte("a -> b\n"), dark: true})
	require.Empty(t, why)
	assert.NotEqual(t, string(light.content), string(dark.content))

	_, why = drawnSource(context.Background(), tileSource{contentType: "text/vnd.d2", content: []byte("a: {shape: nosuchshape}")})
	assert.Contains(t, why, "could not be drawn")

	md := tileSource{contentType: "text/markdown", content: []byte("# x")}
	same, why := drawnSource(context.Background(), md)
	assert.Empty(t, why)
	assert.Equal(t, md, same)

	assert.True(t, isBinary("application/zip+dotlottie"), "a dotLottie archive reaches the tile page by URL")
}
