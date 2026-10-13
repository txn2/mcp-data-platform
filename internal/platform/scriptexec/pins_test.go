package scriptexec

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptres"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// pinnedLibraries is a library source that also reads the pins a version keeps.
type pinnedLibraries struct {
	pins map[string]script.ResourcePin
	err  error
	asks []string
}

func (*pinnedLibraries) LibrarySource(context.Context, string, int) (string, error) { return "", nil }

func (p *pinnedLibraries) ResourcePins(_ context.Context, versionID string) (map[string]script.ResourcePin, error) {
	p.asks = append(p.asks, versionID)
	return p.pins, p.err
}

type noStore struct{ resource.Store }

type noBlobs struct{ resource.S3Client }

// TestARunReadsTheFilesItsVersionPinned is #2113's run half: the loads of a
// run are answered at the versions its script version pinned, read by the
// version's id.
func TestARunReadsTheFilesItsVersionPinned(t *testing.T) {
	pins := map[string]script.ResourcePin{"res:global/flows/page.js": {ResourceID: "r1", Version: 3}}
	libs := &pinnedLibraries{pins: pins}
	r := &runner{libraries: libs, sources: scriptsrc.New(noStore{}, noBlobs{}, "b", "")}

	src, err := r.librariesFor(context.Background(), &script.Version{ID: "ver_7"})
	require.NoError(t, err)
	got, ok := src.(scriptres.Source)
	require.True(t, ok)
	assert.Equal(t, pins, got.Pins)
	assert.Nil(t, got.Claims, "a run reads only what its version pinned")
	assert.Equal(t, []string{"ver_7"}, libs.asks)

	libs.err = errors.New("down")
	_, err = r.librariesFor(context.Background(), &script.Version{ID: "ver_7"})
	assert.Error(t, err, "a run is not started on pins it could not read")

	plain := &runner{libraries: libs}
	src, err = plain.librariesFor(context.Background(), &script.Version{ID: "ver_7"})
	require.NoError(t, err)
	assert.Same(t, libs, src, "with no library to read files from, the library source is the run's")
}
