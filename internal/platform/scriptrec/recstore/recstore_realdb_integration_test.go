//go:build integration

package recstore

// The real-schema proof for recordings (#1939): the table migration 000166
// makes, a draft of an unsaved script attached to the script a test of it is
// saved with, the kept flag moving with the latest version's tests, and the
// sweep leaving a kept recording alone.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestRealDB_RecordingsAreKeptWhileATestNamesThem(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	scripts := scriptstore.New(db)
	sc := &script.Script{Name: "weekly", Source: "def main():\n    pass\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com"}))

	s := New(db)
	data := []byte{0x1f, 0x8b}
	require.NoError(t, s.Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "dpx_draft", ScriptName: "weekly", Kind: scriptrec.KindDraft, RecordedBy: "jane@example.com", Succeeded: true,
	}, Data: data}))
	require.NoError(t, s.Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "srun_1", ScriptID: sc.ID, ScriptName: "weekly", Kind: scriptrec.KindRun, RecordedBy: "jane@example.com",
		Version: 1, Succeeded: true,
	}, Data: data}))
	require.NoError(t, s.Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "srun_2", ScriptID: sc.ID, ScriptName: "weekly", Kind: scriptrec.KindRun, RecordedBy: "jane@example.com",
		Version: 1, Reason: "too large",
	}}))

	got, err := s.Get(ctx, "dpx_draft")
	require.NoError(t, err)
	assert.Empty(t, got.ScriptID)
	assert.Equal(t, data, got.Data)
	_, err = s.Get(ctx, "nope")
	assert.ErrorIs(t, err, scriptrec.ErrNotFound)

	recent, err := s.Recent(ctx, sc.ID, 5)
	require.NoError(t, err)
	require.Len(t, recent, 1, "only a replayable successful run is replayed")
	assert.Equal(t, "srun_1", recent[0].RunID)

	// The saved version's test names the draft: it joins the script and is kept.
	require.NoError(t, s.Keep(ctx, sc.ID, "jane@example.com", []string{"dpx_draft"}))
	got, err = s.Get(ctx, "dpx_draft")
	require.NoError(t, err)
	assert.Equal(t, sc.ID, got.ScriptID)
	assert.True(t, got.Kept)

	// Everything is past retention: the kept one stays.
	_, err = db.ExecContext(ctx, `UPDATE script_recordings SET created_at = NOW() - INTERVAL '400 days'`)
	require.NoError(t, err)
	purged, err := s.Purge(ctx, 365*24*time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 2, purged)
	_, err = s.Get(ctx, "dpx_draft")
	require.NoError(t, err)

	// A later version that stops naming it lets it go.
	require.NoError(t, s.Keep(ctx, sc.ID, "jane@example.com", nil))
	purged, err = s.Purge(ctx, 365*24*time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 1, purged)

	// Deleting the script takes its recordings with it.
	require.NoError(t, s.Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "srun_3", ScriptID: sc.ID, Kind: scriptrec.KindRun, Succeeded: true,
	}, Data: data}))
	_, err = scripts.Delete(ctx, sc.ID)
	require.NoError(t, err)
	_, err = s.Get(ctx, "srun_3")
	assert.ErrorIs(t, err, scriptrec.ErrNotFound)
}

// TestRealDB_RecordingIsSealedAtRest is #2402's criterion for the recording
// sink: a value a run bound -- here one a script decrypted -- is in no form in
// the stored row, while the store reads the recording back whole.
func TestRealDB_RecordingIsSealedAtRest(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	enc, err := fieldcrypt.NewFieldEncryptor(bytes.Repeat([]byte{7}, fieldcrypt.KeyLength))
	require.NoError(t, err)
	s := New(db).Sealed(fieldcrypt.NewRestFieldEncryptor(enc))

	const secret = "4111-1111-1111-1111"
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(`{"tool":"appdb.tx_execute","args":{"params":["` + secret + `"]}}`))
	require.NoError(t, w.Close())
	require.NoError(t, s.Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "srun_sealed", ScriptName: "pay", Kind: scriptrec.KindDraft, RecordedBy: "jane@example.com", Succeeded: true,
	}, Data: gz.Bytes()}))

	var raw []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT data FROM script_recordings WHERE run_id = 'srun_sealed'`).Scan(&raw))
	require.True(t, bytes.HasPrefix(raw, []byte("encb:")), "the stored recording is sealed")
	_, gzErr := gzip.NewReader(bytes.NewReader(raw))
	require.Error(t, gzErr, "the stored bytes are not a readable gzip stream")
	for _, form := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)),
		url.QueryEscape(secret), strings.Trim(mustJSON(t, secret), `"`)} {
		assert.NotContains(t, string(raw), form)
	}

	got, err := s.Get(ctx, "srun_sealed")
	require.NoError(t, err)
	assert.Equal(t, gz.Bytes(), got.Data, "the store opens what it sealed")

	// Sealed to its run: the row copied under another run id does not open.
	_, err = db.ExecContext(ctx, `INSERT INTO script_recordings (run_id, script_name, kind, recorded_by, source_sha256, succeeded, reason, bytes, data)
		SELECT 'srun_other', script_name, kind, recorded_by, source_sha256, succeeded, reason, bytes, data FROM script_recordings WHERE run_id = 'srun_sealed'`)
	require.NoError(t, err)
	_, err = s.Get(ctx, "srun_other")
	require.Error(t, err)

	// A deployment without the key cannot read it, and says so.
	_, err = New(db).Get(ctx, "srun_sealed")
	require.ErrorContains(t, err, "no encryption key")
	// One saved before the key was set reads as stored.
	require.NoError(t, New(db).Save(ctx, scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "srun_plain", ScriptName: "pay", Kind: scriptrec.KindDraft, RecordedBy: "jane@example.com", Succeeded: true,
	}, Data: gz.Bytes()}))
	got, err = s.Get(ctx, "srun_plain")
	require.NoError(t, err)
	assert.Equal(t, gz.Bytes(), got.Data)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
