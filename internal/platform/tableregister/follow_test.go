package tableregister

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A registration pins a directory, and every revision or version moves the
// source's head to a new one (#1536). What these hold: a registration made
// with follow is moved onto the new head by the write that produced it, with
// the columns the new file declares, under the same registrant, and the write
// is told; a pinned one stays and the write is told that too; and a follow
// that cannot be completed leaves the registration behind the file with the
// reason on it and never touches the write.

// newHeadCSV is the file after a refresh: one more column than csvBody.
const newHeadCSV = "store_id,vendor_code,rebate_pct,region\n101,ACME-NW,4.5,west\n"

// revisedSource is testSource after a version moved its head to a fresh
// per-version directory, which is what every revision and version write does.
func revisedSource() Source {
	src := testSource()
	src.HeadKey = "artifacts/u1/asset_1/v2/content.csv"
	return src
}

// moveHead models the write the follow answers: the new head's object exists
// beside the old one, in a directory of its own, holding body.
func (h *harness) moveHead(body string) Source {
	src := revisedSource()
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey, Size: int64(len(body))})
	h.objects.body = []byte(body)
	h.trino.statements = nil
	h.audit.events = nil
	return src
}

func registerFollowing(t *testing.T, h *harness, follow bool) *Result {
	t.Helper()
	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "mcp", Follow: follow})
	require.NoError(t, err)
	return res
}

// TestFollowSource_MovesAFollowingRegistrationOntoTheNewHead is the acceptance
// assertion: after the write, the table reads the new directory with the new
// columns, the record says so, and the audit trail names the version.
func TestFollowSource_MovesAFollowingRegistrationOntoTheNewHead(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	assert.True(t, reg.Follow, "the choice is stored on the registration")
	src := h.moveHead(newHeadCSV)

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.Equal(t, FollowOutcome{
		RegistrationID: reg.ID, Table: "scratch.uploads.analyst_content", Connection: "scratch",
		Followed: true, Version: 2, ColumnsChanged: true,
	}, out[0])
	assert.Equal(t,
		"scratch.uploads.analyst_content on scratch now reads version 2. Its columns changed with the file.",
		out[0].Sentence())

	// The same DDL a re-registration runs, at the new location, with the
	// header the new file declares.
	require.Len(t, h.trino.statements, 3)
	assert.Contains(t, h.trino.statements[0], "CREATE SCHEMA IF NOT EXISTS")
	assert.Equal(t, `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_content"`, h.trino.statements[1])
	assert.Contains(t, h.trino.statements[2], `external_location = 's3://portal-assets/artifacts/u1/asset_1/v2/'`)
	assert.Contains(t, h.trino.statements[2], `"region" VARCHAR`)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, "s3://portal-assets/artifacts/u1/asset_1/v2/", stored.Location)
	assert.Len(t, stored.Columns, 4)
	assert.Empty(t, stored.FollowError)
	assert.False(t, stored.IsStale(src.Bucket, src.HeadKey), "a followed registration is current")
	assert.Equal(t, "alice@example.com", stored.RegisteredBy, "the registrant is unchanged")

	require.Len(t, h.audit.events, 1)
	ev := h.audit.events[0]
	assert.Equal(t, followEvent, ev.ToolName)
	assert.Equal(t, "alice@example.com", ev.UserEmail, "recorded under the registrant, not the writer")
	assert.True(t, ev.Success)
	assert.Equal(t, 2, ev.Parameters["followed_version"])
	assert.Equal(t, []string{"store_id", "vendor_code", "rebate_pct"}, ev.Parameters["columns_before"])
	assert.Equal(t, []string{"store_id", "vendor_code", "rebate_pct", "region"}, ev.Parameters["columns_after"])
	assert.Contains(t, ev.Parameters["sql"], "DROP TABLE")
}

// TestFollowSource_APinnedRegistrationStaysAndIsReported is the default: the
// table serves what it was registered over, and the write is told it is now
// behind and how to move it.
func TestFollowSource_APinnedRegistrationStaysAndIsReported(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, false)
	src := h.moveHead(newHeadCSV)

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.True(t, out[0].Pinned)
	assert.Contains(t, out[0].Sentence(), "scratch.uploads.analyst_content on scratch is pinned")
	assert.Contains(t, out[0].Sentence(), "with follow left on")
	assert.Empty(t, h.trino.statements, "nothing runs for a pinned registration")
	assert.Empty(t, h.audit.events)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.True(t, stored.IsStale(src.Bucket, src.HeadKey), "behind the file, exactly as before")
	assert.Equal(t, reg.Location, stored.Location)
}

// TestFollowSource_APinnedRegistrationAlreadyAtTheHeadIsCurrent: a head
// written at the directory the table already reads is not behind, and saying
// so would be false.
func TestFollowSource_APinnedRegistrationAlreadyAtTheHeadIsCurrent(t *testing.T) {
	h := newHarness(t)
	registerFollowing(t, h, false)
	h.trino.statements = nil

	out := h.reg.FollowSource(context.Background(), testSource(), 2)

	require.Len(t, out, 1)
	assert.True(t, out[0].Followed)
	assert.False(t, out[0].Pinned)
	assert.Empty(t, h.trino.statements)
}

// TestFollowSource_ReportsEveryRegistrationOverTheFile: one file, two tables,
// one following and one pinned; each is answered on its own terms.
func TestFollowSource_ReportsEveryRegistrationOverTheFile(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "live", Follow: true})
	require.NoError(t, err)
	_, err = h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "snapshot"})
	require.NoError(t, err)
	src := h.moveHead(csvBody)

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 2)
	byTable := map[string]FollowOutcome{}
	for _, o := range out {
		byTable[o.Table] = o
	}
	assert.True(t, byTable["scratch.uploads.analyst_live"].Followed)
	assert.False(t, byTable["scratch.uploads.analyst_live"].ColumnsChanged, "same header, columns unchanged")
	assert.True(t, byTable["scratch.uploads.analyst_snapshot"].Pinned)
	assert.Equal(t, 2, len(Sentences(out)))
}

// TestFollowSource_ACreateThatFailsAfterTheDropPutsTheTableBack. The write
// succeeded either way; what must not be left is a registration naming a
// table the failed follow took away.
func TestFollowSource_ACreateThatFailsAfterTheDropPutsTheTableBack(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	src := h.moveHead(newHeadCSV)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) && strings.Contains(sql, "/v2/") {
			return errors.New("coordinator down")
		}
		return nil
	}

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.False(t, out[0].Pinned)
	assert.Contains(t, out[0].Reason, "coordinator down")
	assert.Contains(t, out[0].Sentence(), "follows this file but could not be moved to version 2")
	assert.Contains(t, out[0].Sentence(), "behind the file until it is registered again")

	// DROP, the failed CREATE at the new head, then the CREATE that restores
	// the old table.
	require.Len(t, h.trino.statements, 4)
	assert.Contains(t, h.trino.statements[2], "/v2/")
	assert.Contains(t, h.trino.statements[3], `external_location = 's3://portal-assets/artifacts/u1/asset_1/'`)
	assert.True(t, h.trino.tables[`"scratch"."uploads"."analyst_content"`], "the old table stands again")

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Location, stored.Location, "the registration is where it was")
	assert.Contains(t, stored.FollowError, "coordinator down")
	assert.True(t, stored.IsStale(src.Bucket, src.HeadKey))

	// The failed follow and the restore are both on the trail.
	require.Len(t, h.audit.events, 2)
	assert.False(t, h.audit.events[0].Success)
	assert.Contains(t, h.audit.events[0].ErrorMessage, "coordinator down")
	assert.Equal(t, followEvent, h.audit.events[1].ToolName)
	assert.Contains(t, h.audit.events[1].Parameters["sql"], "CREATE TABLE")
}

// TestFollowSource_ADropThatFailsChangedNothing: nothing ran that touched the
// table, so nothing is put back, and the registration is behind with the
// reason.
func TestFollowSource_ADropThatFailsChangedNothing(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	src := h.moveHead(newHeadCSV)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, dropTablePrefix) {
			return errors.New("refused")
		}
		return nil
	}

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Len(t, h.trino.statements, 2, "CREATE SCHEMA and the DROP that failed; no restore")
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "refused")
}

// TestFollowSource_ARestoreThatFailsIsAuditedAndTheReasonKept: a coordinator
// that refuses every CREATE takes the table away and cannot put it back; both
// statements are on the trail and the registration carries the reason.
func TestFollowSource_ARestoreThatFailsIsAuditedAndTheReasonKept(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	src := h.moveHead(newHeadCSV)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			return errors.New("coordinator down")
		}
		return nil
	}

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.Contains(t, out[0].Reason, "coordinator down")
	require.Len(t, h.audit.events, 2)
	assert.False(t, h.audit.events[1].Success, "the restore's failure is on the trail too")
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "coordinator down")
}

// TestFollowSource_RefusesWhatARegistrationWouldRefuse: the new head is read
// the way a registration reads it, so a file a table cannot be built over
// leaves the registration behind with the same reason a registration would
// give -- and nothing is corrected on the way.
func TestFollowSource_RefusesWhatARegistrationWouldRefuse(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		src    func(Source) Source
		extra  []ObjectEntry
		reason string
	}{
		{
			name:   "a file that needs correcting",
			body:   macBody,
			reason: "Register it again asking for the file to be corrected",
		},
		{
			name:   "a file that cannot be corrected",
			body:   "\xff\xfes\x00t\x00o\x00r\x00e\x00\n\x00",
			reason: "Re-export it as UTF-8 CSV",
		},
		{
			name:   "a second file in the new directory",
			body:   csvBody,
			extra:  []ObjectEntry{{Key: "artifacts/u1/asset_1/v2/extra.csv"}},
			reason: "extra.csv sits beside it",
		},
		{
			name: "a head that is no longer a CSV",
			body: `{"a":1}`,
			src: func(s Source) Source {
				s.ContentType, s.HeadKey = "application/json", "artifacts/u1/asset_1/v2/content.json"
				return s
			},
			reason: ErrNotCSV.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			reg := registerFollowing(t, h, true)
			src := h.moveHead(tc.body)
			if tc.src != nil {
				src = tc.src(src)
			}
			h.objects.entries = append(h.objects.entries, tc.extra...)
			h.reviser.saved = nil

			out := h.reg.FollowSource(context.Background(), src, 2)

			require.Len(t, out, 1)
			assert.False(t, out[0].Followed)
			assert.Contains(t, out[0].Reason, tc.reason)
			assert.Empty(t, h.trino.statements, "nothing runs against a file that cannot be a table")
			assert.Empty(t, h.reviser.saved, "a follow never rewrites the person's file")
			stored, err := h.store.Get(context.Background(), reg.ID)
			require.NoError(t, err)
			assert.Contains(t, stored.FollowError, tc.reason)
		})
	}
}

// TestFollowSource_ARegistrationAlreadyAtTheHeadClearsAnOldFailure: the
// second follow after a write finds nothing to move, and a failure recorded by
// an earlier one is cleared because the registration is where the file is.
func TestFollowSource_ARegistrationAlreadyAtTheHeadClearsAnOldFailure(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	src := h.moveHead(csvBody)
	require.NoError(t, h.store.RecordFollowFailure(context.Background(), reg.ID, "an earlier attempt"))

	first := h.reg.FollowSource(context.Background(), src, 2)
	require.True(t, first[0].Followed)
	statements := len(h.trino.statements)

	second := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, second, 1)
	assert.True(t, second[0].Followed)
	assert.Len(t, h.trino.statements, statements, "nothing to run the second time")
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.FollowError)
}

// TestFollowSource_ClearsAnOldFailureWithoutMovingWhenAlreadyCurrent covers
// the store write that only clears the reason.
func TestFollowSource_ClearsAnOldFailureWithoutMovingWhenAlreadyCurrent(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	require.NoError(t, h.store.RecordFollowFailure(context.Background(), reg.ID, "an earlier attempt"))
	h.trino.statements = nil

	out := h.reg.FollowSource(context.Background(), testSource(), 1)

	require.Len(t, out, 1)
	assert.True(t, out[0].Followed)
	assert.Empty(t, h.trino.statements)
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.FollowError)

	// The clearing write failing is reported like any other follow failure.
	require.NoError(t, h.store.RecordFollowFailure(context.Background(), reg.ID, "again"))
	h.store.relocateErr = errors.New("store away")
	out = h.reg.FollowSource(context.Background(), testSource(), 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "store away")
}

// TestFollowSource_ATableMovedButNotRecordedSaysSo: the DDL succeeded and the
// row could not be updated, which is the one state where the table is current
// and the record is not; the reason names it so the person registers again.
func TestFollowSource_ATableMovedButNotRecordedSaysSo(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	src := h.moveHead(csvBody)
	h.store.relocateErr = errors.New("store away")

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "the table was moved but its record could not be updated")
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "store away")
}

// TestFollowSource_NothingToFollow: no registrations, an unwired registrar,
// and a store that cannot answer all report nothing rather than failing the
// write.
func TestFollowSource_NothingToFollow(t *testing.T) {
	h := newHarness(t)
	assert.Nil(t, h.reg.FollowSource(context.Background(), revisedSource(), 2))

	h.store.listErr = errors.New("store away")
	assert.Nil(t, h.reg.FollowSource(context.Background(), revisedSource(), 2))

	assert.Nil(t, New(Deps{}).FollowSource(context.Background(), revisedSource(), 2))
	assert.Nil(t, Sentences(nil))
}

// TestRegister_ACorrectionFollowsTheOtherTablesOverTheFile: saving a
// corrected version moves the head exactly as a revision does, so a following
// registration made earlier over the same file is moved onto the corrected
// version, a pinned one is reported behind, and the answer says both.
func TestRegister_ACorrectionFollowsTheOtherTablesOverTheFile(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.objects.body = []byte(macBody) })
	// Two registrations over the file as it was uploaded: the file has to be
	// readable for that, so they are made over csvBody and the defective
	// upload arrives afterwards.
	h.objects.body = []byte(csvBody)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "live", Follow: true})
	require.NoError(t, err)
	_, err = h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "snapshot"})
	require.NoError(t, err)
	h.objects.body = []byte(macBody)

	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "corrected", Repair: true})
	require.NoError(t, err)
	require.NotNil(t, res.Correction)

	require.Len(t, res.Correction.Followed, 2, "the registration just made is not among them")
	byTable := map[string]FollowOutcome{}
	for _, o := range res.Correction.Followed {
		byTable[o.Table] = o
	}
	assert.True(t, byTable["scratch.uploads.analyst_live"].Followed)
	assert.Equal(t, res.Correction.Version, byTable["scratch.uploads.analyst_live"].Version)
	assert.True(t, byTable["scratch.uploads.analyst_snapshot"].Pinned)
	assert.Contains(t, res.Correction.Summary(), "scratch.uploads.analyst_live on scratch now reads version")
	assert.Contains(t, res.Correction.Summary(), "scratch.uploads.analyst_snapshot on scratch is pinned")

	live, err := h.store.ByName(context.Background(), "scratch", "scratch", "uploads", "analyst_live")
	require.NoError(t, err)
	assert.Equal(t, res.Location, live.Location, "the following table reads the corrected version too")
}

// TestFollowOutcome_Sentence pins the three sentences a write carries.
func TestFollowOutcome_Sentence(t *testing.T) {
	base := FollowOutcome{Table: "scratch.uploads.t", Connection: "c", Version: 7}

	followed := base
	followed.Followed = true
	assert.Equal(t, "scratch.uploads.t on c now reads version 7.", followed.Sentence())

	pinned := base
	pinned.Pinned = true
	assert.Equal(t, "scratch.uploads.t on c is pinned to the version it was registered over and is now behind"+
		" this file; register it again to move it, with follow left on if it should keep up with the file.",
		pinned.Sentence())

	failed := base
	failed.Reason = "coordinator down."
	assert.Equal(t, "scratch.uploads.t on c follows this file but could not be moved to version 7:"+
		" coordinator down. It is behind the file until it is registered again.", failed.Sentence())
}

// The checks a write that ran DROP TABLE makes afterwards (#1546): every other
// registration on the connection is asked for, one that is gone is recorded
// and reported, and one that answers is left alone.

// registerNamed registers testSource under a table name, following or pinned.
func registerNamed(t *testing.T, h *harness, name string, follow bool) Registration {
	t.Helper()
	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: name, Source: "mcp", Follow: follow})
	require.NoError(t, err)
	return res.Registration
}

func TestFollow_ReportsARegistrationWhoseTableIsGone(t *testing.T) {
	h := newHarness(t)
	following := registerNamed(t, h, "x", true)
	pinned := registerNamed(t, h, "x_pinned", false)
	h.trino.missing = map[string]bool{pinned.QualifiedName(): true}
	src := h.moveHead(newHeadCSV)

	outcomes := h.reg.FollowSource(context.Background(), src, 2)
	var missing *FollowOutcome
	for i := range outcomes {
		if outcomes[i].Missing {
			missing = &outcomes[i]
		}
	}
	require.NotNil(t, missing, "the missing sibling is reported: %v", Sentences(outcomes))
	assert.Equal(t, pinned.ID, missing.RegistrationID)
	assert.Equal(t, pinned.QualifiedName()+" on scratch no longer exists: the table was removed while "+
		following.QualifiedName()+" was moved to version 2. Register it again to restore it.", missing.Sentence())

	stored, err := h.store.Get(context.Background(), pinned.ID)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored.FollowError, missingPrefix), "the row records it: %q", stored.FollowError)
	assert.Contains(t, stored.FollowError, following.QualifiedName())

	// A registration already recorded missing is not recorded or reported
	// again by the next write.
	for _, o := range h.reg.FollowSource(context.Background(), src, 3) {
		assert.False(t, o.Missing, "already recorded: %s", o.Sentence())
	}
}

func TestFollow_ASiblingThatStillExistsIsNotReported(t *testing.T) {
	h := newHarness(t)
	registerNamed(t, h, "x", true)
	registerNamed(t, h, "x_pinned", false)

	outcomes := h.reg.FollowSource(context.Background(), h.moveHead(newHeadCSV), 2)
	require.Len(t, outcomes, 2)
	for _, o := range outcomes {
		assert.False(t, o.Missing)
	}
}

func TestFollow_ALookupThatFailsIsLoggedNotReported(t *testing.T) {
	h := newHarness(t)
	registerNamed(t, h, "x", true)
	pinned := registerNamed(t, h, "x_pinned", false)
	h.trino.existsErr = errors.New("connection refused")

	for _, o := range h.reg.FollowSource(context.Background(), h.moveHead(newHeadCSV), 2) {
		assert.False(t, o.Missing, "a connection that cannot answer has not said the table is gone")
	}
	stored, err := h.store.Get(context.Background(), pinned.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.FollowError)
}

func TestUnregister_RecordsASiblingWhoseTableIsGone(t *testing.T) {
	h := newHarness(t)
	dropped := registerNamed(t, h, "x", true)
	pinned := registerNamed(t, h, "x_pinned", false)
	h.trino.missing = map[string]bool{pinned.QualifiedName(): true}

	require.NoError(t, h.reg.Unregister(context.Background(), testCaller(), dropped.ID, "mcp"))

	stored, err := h.store.Get(context.Background(), pinned.ID)
	require.NoError(t, err)
	assert.Equal(t, missingPrefix+"the table was removed while "+dropped.QualifiedName()+" was dropped.", stored.FollowError)
}

func TestRegister_AReplacementReportsASiblingWhoseTableIsGone(t *testing.T) {
	h := newHarness(t)
	first := registerNamed(t, h, "x", true)
	pinned := registerNamed(t, h, "x_pinned", false)
	h.trino.missing = map[string]bool{pinned.QualifiedName(): true}

	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "x", Source: "mcp", Follow: true})
	require.NoError(t, err)
	require.Len(t, res.Siblings, 1, "the replacement of %s checks the connection", first.QualifiedName())
	assert.Equal(t, pinned.ID, res.Siblings[0].RegistrationID)
	assert.Contains(t, res.Siblings[0].Sentence(), "was replaced.")

	// A registration that replaced nothing ran no DROP and checks nothing.
	fresh, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "y", Source: "mcp", Follow: true})
	require.NoError(t, err)
	assert.Empty(t, fresh.Siblings)
}

// --- a follow that corrects the file it follows (#1577) ---
//
// A registration made with repair saves a corrected version of a file a query
// engine cannot read past, and registers that version. The choice was made
// once and then forgotten, so the next version of the file carrying the same
// defect stopped the follow dead -- which is what a source producing one
// defect on a schedule does every time it runs. What these hold: the choice is
// carried on the registration, a follow re-applies it, and everything that was
// refused before is refused still.

// registerCorrecting registers a table that follows its file and corrects it,
// under a registrant who is nobody else in these tests.
func registerCorrecting(t *testing.T, h *harness) *Result {
	t.Helper()
	res, err := h.reg.Register(context.Background(), correctingRegistrant(), testSource(),
		Request{Connection: "scratch", TableName: "live", Source: "mcp", Follow: true, Repair: true})
	require.NoError(t, err)
	require.Nil(t, res.Correction, "the version it was registered over needed no correction")
	require.True(t, res.Repair, "the choice is stored on the registration")
	return res
}

// correctingRegistrant is who the registration was made by, and so who a
// version a follow saves is written under. It is deliberately not the harness
// default: attribution to the registrant has to be visible as a choice rather
// than as the only address in the test.
func correctingRegistrant() Caller {
	return Caller{UserID: "u2", Email: "dana@example.com", Persona: "analyst"}
}

// tornNewVersion is the next version of the registered file, carrying the same
// defect the registration was made to correct: a value with a line break in
// it, which a table reads as the end of the row. Its header is the one the
// table already declares, so what the follow does about the defect is the only
// thing that changes.
const tornNewVersion = "store_id,vendor_code,rebate_pct\n" +
	"101,\"ACME\nNorth West\",4.5\n" +
	"102,\"BAY\nSeattle\",6.0\n"

// raggedNewVersion carries a defect the platform will not correct: its records
// do not all have the header's fields, and neither filling one in nor dropping
// one from another is something to do to somebody's data.
var raggedNewVersion = "store_id,vendor_code,rebate_pct\n101,\"ACME\nNW\",4.5\n" +
	strings.Repeat("9\n", 8)

// defectiveHead moves the source's head onto a version whose cells carry line
// breaks -- the shape a weekly spreadsheet export repeats. The write produced
// version 2, so a correction saved above it is version 3.
func defectiveHead(h *harness) Source {
	src := h.moveHead(tornNewVersion)
	h.reviser.saved, h.reviser.baseVersion = nil, defectiveVersion
	return src
}

// defectiveVersion is the version number the write that triggers these follows
// produced.
const defectiveVersion = 2

// TestFollowSource_ARepairingRegistrationCorrectsTheNewVersion is the
// acceptance assertion for #1577: a later version carrying the same
// correctable defect leaves the table reading that version's rows, through a
// corrected version saved above it under the registrant.
func TestFollowSource_ARepairingRegistrationCorrectsTheNewVersion(t *testing.T) {
	h := newHarness(t)
	reg := registerCorrecting(t, h)
	src := defectiveHead(h)

	out := h.reg.FollowSource(context.Background(), src, 2)

	// The correction is the file's next version, written through the version
	// trail rather than over the bytes the write produced, and recorded
	// against the person whose registration asked for it.
	require.Len(t, h.reviser.saved, 1, "exactly one version is written")
	saved := h.reviser.saved[0]
	assert.Equal(t, "dana@example.com", saved.by, "under the registrant, not whoever made the write")
	assert.Equal(t, "put 2 rows back onto one line", saved.summary)
	assert.NotContains(t, string(saved.content), "ACME\nNorth West")

	// The table reads the corrected version, and the write is told both halves.
	corrected := "s3://portal-assets/artifacts/u1/asset_1/v2/v/rev_3/"
	require.Len(t, out, 1)
	assert.True(t, out[0].Followed)
	assert.Equal(t, 3, out[0].Version, "the corrected version, not the one the write produced")
	assert.Equal(t,
		"scratch.uploads.analyst_live on scratch now reads version 3."+
			" Saved version 3 of this file, which put 2 rows back onto one line."+
			" The file as it was uploaded is still there as the version before it.",
		out[0].Sentence())

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, corrected, stored.Location)
	assert.Empty(t, stored.FollowError)
	assert.True(t, stored.Repair, "the choice survives the follow that used it")
	assert.Contains(t, h.trino.statements[len(h.trino.statements)-1], "external_location = '"+corrected+"'")

	// The version carrying the defect is still an object of its own, in the
	// directory the write put it in, which is what makes it revertible.
	assert.Contains(t, objectKeys(h), src.HeadKey)
}

// objectKeys is every object the fake store holds, for an assertion about what
// a correction left alone.
func objectKeys(h *harness) []string {
	keys := make([]string, 0, len(h.objects.entries))
	for _, e := range h.objects.entries {
		keys = append(keys, e.Key)
	}
	return keys
}

// TestFollowSource_ARegistrationWithoutTheChoiceRewritesNothing: the file is
// somebody's, and a registration that did not ask for it to be corrected does
// not get it corrected on the back of a write about something else.
func TestFollowSource_ARegistrationWithoutTheChoiceRewritesNothing(t *testing.T) {
	h := newHarness(t)
	reg := registerFollowing(t, h, true)
	assert.False(t, reg.Repair)
	src := defectiveHead(h)

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Empty(t, out[0].Repaired)
	assert.Contains(t, out[0].Reason, "Register it again asking for the file to be corrected")
	assert.Empty(t, h.reviser.saved, "no version is written")
	assert.Empty(t, h.trino.statements)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Location, stored.Location, "still on the version it was registered over")
	assert.Contains(t, stored.FollowError, "asking for the file to be corrected")
}

// TestFollowSource_APinnedRegistrationCarryingTheChoiceCorrectsNothing: a
// pinned table is not moved onto a new version at all, so it never meets one
// to correct. Carrying the choice does not turn it into a following table.
func TestFollowSource_APinnedRegistrationCarryingTheChoiceCorrectsNothing(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), correctingRegistrant(), testSource(),
		Request{Connection: "scratch", Source: "mcp", Follow: false, Repair: true})
	require.NoError(t, err)
	src := defectiveHead(h)

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.True(t, out[0].Pinned)
	assert.Empty(t, h.reviser.saved, "no version is written for a table that does not follow")
}

// TestFollowSource_AnUncorrectableVersionIsStillRefused: what the platform
// cannot honestly correct it does not touch, whatever the registration asked
// for. Bytes in an encoding it does not convert are read wrongly by the
// correction too, and records that do not match the header are what the
// correction refuses in turn (#1449).
func TestFollowSource_AnUncorrectableVersionIsStillRefused(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		reason string
	}{
		{
			name:   "a wide encoding",
			body:   "\xff\xfes\x00t\x00o\x00r\x00e\x00\n\x00",
			reason: "Re-export it as UTF-8 CSV",
		},
		{
			name:   "records that do not match the header",
			body:   raggedNewVersion,
			reason: "its records do not all have the header's 3 fields",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			reg := registerCorrecting(t, h)
			src := h.moveHead(tc.body)
			h.reviser.saved, h.reviser.baseVersion = nil, 2

			out := h.reg.FollowSource(context.Background(), src, 2)

			require.Len(t, out, 1)
			assert.False(t, out[0].Followed)
			assert.Contains(t, out[0].Reason, tc.reason)
			assert.Equal(t, 2, out[0].Version, "behind the version the write produced")
			assert.Empty(t, h.reviser.saved, "no version is written")
			assert.Empty(t, h.trino.statements)

			stored, err := h.store.Get(context.Background(), reg.ID)
			require.NoError(t, err)
			assert.Equal(t, reg.Location, stored.Location)
			assert.Contains(t, stored.FollowError, tc.reason)
		})
	}
}

// TestFollowSource_OneCorrectionServesEveryTableOverTheFile: the correction is
// a version of the file, so a defective version produces one of them however
// many tables are over it, and every following table lands on it.
func TestFollowSource_OneCorrectionServesEveryTableOverTheFile(t *testing.T) {
	for _, tc := range []struct {
		name           string
		secondCorrects bool
	}{
		{name: "both registrations correct the file", secondCorrects: true},
		{name: "only one of them does", secondCorrects: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			first := registerCorrecting(t, h)
			second, err := h.reg.Register(context.Background(), testCaller(), testSource(),
				Request{
					Connection: "scratch", TableName: "second", Source: "mcp",
					Follow: true, Repair: tc.secondCorrects,
				})
			require.NoError(t, err)
			src := defectiveHead(h)

			out := h.reg.FollowSource(context.Background(), src, 2)

			require.Len(t, h.reviser.saved, 1, "the file is corrected once for the version")
			assert.Equal(t, "dana@example.com", h.reviser.saved[0].by,
				"under the registrant of the registration that asked for it")

			corrected := "s3://portal-assets/artifacts/u1/asset_1/v2/v/rev_3/"
			require.Len(t, out, 2)
			for _, o := range out {
				assert.True(t, o.Followed, o.Table)
				assert.Equal(t, 3, o.Version, o.Table)
			}
			for _, id := range []string{first.ID, second.ID} {
				stored, storeErr := h.store.Get(context.Background(), id)
				require.NoError(t, storeErr)
				assert.Equal(t, corrected, stored.Location, "both tables read the corrected version")
			}

			// One sentence about the file, carried by the follow that saved
			// it, rather than the same sentence once per table.
			var said int
			for _, line := range Sentences(out) {
				if strings.Contains(line, "Saved version 3 of this file") {
					said++
				}
			}
			assert.Equal(t, 1, said, "the correction is reported once")
		})
	}
}

// TestFollowSource_TheCorrectionIsAttributedToTheStandingChoice: where more
// than one registration over a file asks for it to be corrected, the version
// is written under the one that has been asking longest -- not under whichever
// row the store happened to hand back first, which is the newest.
func TestFollowSource_TheCorrectionIsAttributedToTheStandingChoice(t *testing.T) {
	h := newHarness(t)
	oldest := registerCorrecting(t, h)
	newest, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "second", Source: "mcp", Follow: true, Repair: true})
	require.NoError(t, err)

	regs, err := h.store.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	require.Len(t, regs, 2)
	require.Equal(t, newest.ID, regs[0].ID, "the store returns registrations newest first")

	h.reg.FollowSource(context.Background(), defectiveHead(h), defectiveVersion)

	require.Len(t, h.reviser.saved, 1)
	assert.Equal(t, "dana@example.com", h.reviser.saved[0].by,
		"the version is written under the oldest standing choice, not the newest registration")
	assert.Equal(t, oldest.RegisteredBy, h.reviser.saved[0].by)
}

// TestFollowSource_ACorrectionSurvivesAFailureAfterIt: the person's file has a
// new version whether or not the head could then be described, so a refusal
// that arrives after the correction still says the file changed.
func TestFollowSource_ACorrectionSurvivesAFailureAfterIt(t *testing.T) {
	h := newHarness(t)
	reg := registerCorrecting(t, h)
	src := defectiveHead(h)
	// A second object lands in the corrected version's directory, which is a
	// directory a table cannot be pointed at: a table reads every file under
	// its external location.
	h.reviser.afterSave = func() {
		h.objects.entries = append(h.objects.entries,
			ObjectEntry{Key: "artifacts/u1/asset_1/v2/v/rev_3/notes.csv"})
	}

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, h.reviser.saved, 1)
	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "notes.csv sits beside it")
	assert.Contains(t, out[0].Sentence(), "Saved version 3 of this file")

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "notes.csv sits beside it")
}

// TestFollowSource_ACorrectionThatCannotBeSavedLeavesTheTableBehind: the
// version trail refused the write, so there is no corrected version and the
// registration is behind the file with the reason on it -- which is what every
// other follow failure does.
func TestFollowSource_ACorrectionThatCannotBeSavedLeavesTheTableBehind(t *testing.T) {
	h := newHarness(t)
	reg := registerCorrecting(t, h)
	src := defectiveHead(h)
	h.reviser.err = errors.New("the version trail is unreachable")

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "saving a corrected version of the file")
	assert.Empty(t, h.trino.statements)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Location, stored.Location)
	assert.Contains(t, stored.FollowError, "the version trail is unreachable")
}

// TestFollowSource_ADeploymentWithNoVersionTrailCannotCorrect: a kind this
// deployment keeps no history for has nowhere to put a corrected version, so
// the registration is left behind with the sentence that says why rather than
// a corrected file appearing somewhere no version panel can undo it.
func TestFollowSource_ADeploymentWithNoVersionTrailCannotCorrect(t *testing.T) {
	h := newHarness(t)
	reg := registerCorrecting(t, h)
	src := defectiveHead(h)
	h.reg.deps.Revisers = map[string]Reviser{}

	out := h.reg.FollowSource(context.Background(), src, 2)

	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "keeps no version history for a stored asset")

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Location, stored.Location)
}

// TestRepairRegistrant_PicksTheStandingChoice covers what decides which
// registrant a corrected version is written under, including the cases the
// store's own order would answer differently: the rows arrive newest first,
// the choice is the oldest, and two made in the same instant are separated by
// id so the answer is the same on every read rather than on the first one.
func TestRepairRegistrant_PicksTheStandingChoice(t *testing.T) {
	at := func(minute int) time.Time { return time.Date(2026, 8, 20, 14, minute, 0, 0, time.UTC) }
	corrects := func(id string, registered time.Time) Registration {
		return Registration{
			ID: id, RegisteredBy: id + "@example.com", RegisteredAt: registered,
			Follow: true, Repair: true,
		}
	}

	cases := []struct {
		name     string
		regs     []Registration
		exceptID string
		want     string
	}{
		{
			name: "no registration asked for the file to be corrected",
			regs: []Registration{{ID: "a", Follow: true}},
		},
		{
			name: "a pinned registration carrying the choice never meets a new version",
			regs: []Registration{{ID: "a", Repair: true}},
		},
		{
			name: "the oldest of several, whatever order they arrive in",
			regs: []Registration{corrects("c", at(30)), corrects("a", at(10)), corrects("b", at(20))},
			want: "a",
		},
		{
			name: "two made in the same instant are separated by id",
			regs: []Registration{corrects("b", at(10)), corrects("a", at(10))},
			want: "a",
		},
		{
			name:     "the registration a correction just registered is not its own author",
			regs:     []Registration{corrects("a", at(10)), corrects("b", at(20))},
			exceptID: "a",
			want:     "b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repairRegistrant(tc.regs, tc.exceptID)
			if tc.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.ID)
		})
	}
}
