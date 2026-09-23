package toolkit

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assetBook is an in-memory idempotency index: key to asset id.
type assetBook struct {
	byKey      map[string]string
	insertErr  error
	versionErr error
	versions   map[string]int
	inserted   []string
}

func (b *assetBook) write(newID string) RunAssetWrite {
	return RunAssetWrite{
		Lookup: func(_ context.Context, key string) (string, bool) {
			id, ok := b.byKey[key]
			return id, ok
		},
		Insert: func(_ context.Context, key string) error {
			if b.insertErr != nil {
				return b.insertErr
			}
			b.byKey[key] = newID
			b.inserted = append(b.inserted, newID)
			return nil
		},
		Version: func(_ context.Context, id string) (int, error) {
			if b.versionErr != nil {
				return 0, b.versionErr
			}
			b.versions[id]++
			return b.versions[id], nil
		},
	}
}

func newBook() *assetBook { return &assetBook{byKey: map[string]string{}, versions: map[string]int{}} }

// TestPersistRunAsset_OneAssetAcrossRuns is #1854's identity rule: the first
// run creates the asset, every later run writes its next version.
func TestPersistRunAsset_OneAssetAcrossRuns(t *testing.T) {
	b := newBook()
	id, v, err := PersistRunAsset(context.Background(), "script:s1:daily", "asset-1", b.write("asset-1"))
	require.NoError(t, err)
	assert.Equal(t, "asset-1", id)
	assert.Equal(t, 1, v)

	id, v, err = PersistRunAsset(context.Background(), "script:s1:daily", "asset-2", b.write("asset-2"))
	require.NoError(t, err)
	assert.Equal(t, "asset-1", id, "the second run versions the first run's asset")
	assert.Equal(t, 2, v)
	assert.Equal(t, []string{"asset-1"}, b.inserted, "no second asset is created")
}

// TestPersistRunAsset_TheLoserOfAFirstWriteRaceVersionsTheWinner covers two
// runs inserting the first asset at once: the loser's insert is refused and
// it writes its version onto the asset the winner created.
func TestPersistRunAsset_TheLoserOfAFirstWriteRaceVersionsTheWinner(t *testing.T) {
	b := newBook()
	w := b.write("asset-loser")
	lookups := 0
	w.Lookup = func(_ context.Context, _ string) (string, bool) {
		lookups++
		if lookups == 1 {
			return "", false // the winner has not committed when the loser looks
		}
		return "asset-winner", true
	}
	w.Insert = func(context.Context, string) error { return errors.New("duplicate key") }
	id, v, err := PersistRunAsset(context.Background(), "k", "asset-loser", w)
	require.NoError(t, err)
	assert.Equal(t, "asset-winner", id)
	assert.Equal(t, 1, v)
}

func TestPersistRunAsset_Failures(t *testing.T) {
	b := newBook()
	b.insertErr = errors.New("db down")
	_, _, err := PersistRunAsset(context.Background(), "k", "a", b.write("a"))
	require.ErrorContains(t, err, "saving the asset record")

	b = newBook()
	b.versionErr = errors.New("db down")
	_, _, err = PersistRunAsset(context.Background(), "k", "a", b.write("a"))
	require.ErrorContains(t, err, "recording the asset version")
}
