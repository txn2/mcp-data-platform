package tableregister

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pinned registration stays on the directory it was registered over, and
// Trino reads every file in that directory (#1851). What these hold: a new
// version written to a directory of its own leaves the table behind it; an
// object replaced in place is what the table now reads; and a directory that
// versions of the file were written into together -- a script's outputs before
// #1851 -- is reported as what the table reads, never as a table that followed
// or one that reads its version alone.

// The directory testSource is registered over, and the key a legacy script
// output wrote each version under: <asset>/<run>.csv, beside the one before.
const (
	pinnedDir    = "artifacts/u1/asset_1/"
	pinnedKey    = pinnedDir + "content.csv"
	legacyRunKey = pinnedDir + "run_2.csv"
)

func TestFollowSource_APinnedRegistrationIsAnsweredByTheDirectoryItReads(t *testing.T) {
	tests := []struct {
		name string
		// head is the key the write moved the file's head to, and entries every
		// object in the bucket after it.
		head    string
		entries []string
		bucket  string
		listErr error
		trunc   bool
		// want is the outcome's Followed, Pinned and Reason.
		followed, pinned bool
		reason           string
	}{
		{
			name:    "a version in a directory of its own beneath is ahead of the table",
			head:    pinnedDir + "v2/content.csv",
			entries: []string{pinnedKey, pinnedDir + "v2/content.csv"},
			pinned:  true,
		},
		{
			name:    "a version in a directory elsewhere is ahead of the table",
			head:    "scripts/s1/asset_1/run_2/content.csv",
			entries: []string{pinnedKey, "scripts/s1/asset_1/run_2/content.csv"},
			pinned:  true,
		},
		{
			name:     "an object replaced in place is what the table reads",
			head:     pinnedKey,
			entries:  []string{pinnedKey, pinnedDir + ".thumbnail.png"},
			followed: true,
		},
		{
			name:    "a version written beside the pinned one is read with it",
			head:    legacyRunKey,
			entries: []string{pinnedKey, legacyRunKey},
			pinned:  true,
			reason:  "this version was written into the directory it reads",
		},
		{
			name:    "earlier versions written beside the pinned one are still read after the layout changed",
			head:    pinnedDir + "run_3/content.csv",
			entries: []string{pinnedKey, legacyRunKey, pinnedDir + "run_3/content.csv"},
			pinned:  true,
			reason:  "earlier versions of this file were written into the directory it reads",
		},
		{
			name:    "a file Trino skips is not one the table reads",
			head:    pinnedDir + "v2/content.csv",
			entries: []string{pinnedKey, pinnedDir + "_SUCCESS", pinnedDir + ".thumbnail_dark.png"},
			pinned:  true,
		},
		{
			name:    "a listing too long to check counts as more than one file",
			head:    pinnedDir + "v2/content.csv",
			entries: []string{pinnedKey},
			trunc:   true,
			pinned:  true,
			reason:  "earlier versions of this file were written into the directory it reads",
		},
		{
			name:     "a listing that fails leaves the directory to decide: the same one",
			head:     pinnedKey,
			entries:  []string{pinnedKey},
			listErr:  errors.New("s3 down"),
			followed: true,
		},
		{
			name:    "a listing that fails leaves the directory to decide: another one",
			head:    pinnedDir + "v2/content.csv",
			entries: []string{pinnedKey},
			listErr: errors.New("s3 down"),
			pinned:  true,
		},
		{
			name:    "a head in another bucket is never the directory the table reads",
			head:    pinnedKey,
			bucket:  "other-bucket",
			entries: []string{pinnedKey, legacyRunKey},
			pinned:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			reg := registerFollowing(t, h, false)
			require.Equal(t, LocationURI("portal-assets", pinnedDir), reg.Location)

			h.objects.entries = nil
			for _, k := range tt.entries {
				h.objects.entries = append(h.objects.entries, ObjectEntry{Key: k, Size: 1})
			}
			h.objects.listErr, h.objects.truncated = tt.listErr, tt.trunc
			h.trino.statements = nil
			src := testSource()
			src.HeadKey = tt.head
			if tt.bucket != "" {
				src.Bucket = tt.bucket
			}

			out := h.reg.FollowSource(context.Background(), src, 2)

			require.Len(t, out, 1)
			assert.Equal(t, tt.followed, out[0].Followed, "followed")
			assert.Equal(t, tt.pinned, out[0].Pinned, "pinned")
			if tt.reason == "" {
				assert.Empty(t, out[0].Reason)
			} else {
				assert.Contains(t, out[0].Reason, tt.reason)
				assert.Contains(t, out[0].Sentence(), "does not read the version it was registered over alone")
			}
			assert.Empty(t, h.trino.statements, "a pinned table is never moved")
			stored, err := h.store.Get(context.Background(), reg.ID)
			require.NoError(t, err)
			assert.Equal(t, reg.Location, stored.Location)
			assert.Empty(t, stored.FollowError, "a pinned registration records no follow failure")
		})
	}
}

// TestFilesTrinoReads_AKindWithNoObjectReaderIsUnavailable: a source kind the
// deployment cannot list is not counted as holding nothing.
func TestFilesTrinoReads_AKindWithNoObjectReaderIsUnavailable(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.Kind = "unknown"
	_, err := h.reg.filesTrinoReads(context.Background(), src, pinnedDir)
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRegister_AScriptOutputWithManyVersionsRegisters is the other half of
// #1851: each version of a script's output alone in its own directory is
// registered over whatever versions came before it, while the layout it
// replaced -- every version in one directory -- is refused naming the version
// beside the head.
func TestRegister_AScriptOutputWithManyVersionsRegisters(t *testing.T) {
	const out = "scripts/script_1/asset_1/"
	t.Run("a version per directory", func(t *testing.T) {
		h := newHarness(t)
		h.objects.entries = []ObjectEntry{
			{Key: out + "run_1/content.csv", Size: 1},
			{Key: out + "run_2/content.csv", Size: 1},
			{Key: out + "run_2/.thumbnail.png", Size: 1},
		}
		src := testSource()
		src.HeadKey = out + "run_2/content.csv"
		res, err := h.reg.Register(context.Background(), testCaller(), src,
			Request{Connection: "scratch", Source: "mcp"})
		require.NoError(t, err)
		assert.Equal(t, LocationURI("portal-assets", out+"run_2/"), res.Location)
	})
	t.Run("every version in one directory", func(t *testing.T) {
		h := newHarness(t)
		h.objects.entries = []ObjectEntry{{Key: out + "run_1.csv", Size: 1}, {Key: out + "run_2.csv", Size: 1}}
		src := testSource()
		src.HeadKey = out + "run_2.csv"
		_, err := h.reg.Register(context.Background(), testCaller(), src,
			Request{Connection: "scratch", Source: "mcp"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_1.csv sits beside it")
	})
}

func TestFollowOutcome_SentenceOfAPinnedTableThatReadsMoreThanItsVersion(t *testing.T) {
	o := FollowOutcome{
		Table: "scratch.uploads.t", Connection: "c", Version: 3, Pinned: true,
		Reason: "earlier versions of this file were written into the directory it reads.",
	}
	assert.Equal(t, "scratch.uploads.t on c is pinned, but it does not read the version it was registered over"+
		" alone: earlier versions of this file were written into the directory it reads. Register it again to"+
		" point it at one version, with follow left on if it should keep up with the file.", o.Sentence())
}
