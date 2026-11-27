package whimap

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

type fakeLibrary struct {
	files          map[string]StoredFile
	moves          []FolderMove
	deletedFolders []string
	// handedTo is, for each removed copy, the file its addresses went to.
	handedTo  map[string]string
	listErr   error
	moveErr   error
	removeErr error
}

func (f *fakeLibrary) ListAttachments(context.Context, whsource.Source) ([]StoredFile, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]StoredFile, 0, len(f.files))
	for _, v := range f.files {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeLibrary) MoveFolder(_ context.Context, _ whsource.Source, from, to string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moves = append(f.moves, FolderMove{From: from, To: to})
	for id, file := range f.files {
		if file.Folder == from {
			file.Folder = to
			f.files[id] = file
		}
	}
	return nil
}

func (f *fakeLibrary) RemoveCopy(_ context.Context, _ whsource.Source, id, keep string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.handedTo == nil {
		f.handedTo = map[string]string{}
	}
	f.handedTo[id] = keep
	delete(f.files, id)
	return nil
}

func (f *fakeLibrary) DeleteFolder(_ context.Context, _ whsource.Source, folder string) error {
	f.deletedFolders = append(f.deletedFolders, folder)
	return nil
}

type fakeEvents struct {
	windows []whstore.Window
	events  map[time.Time][]whevent.Stored
	err     error
}

func (f *fakeEvents) Live(context.Context, string) ([]whstore.Window, error) { return f.windows, f.err }

func (f *fakeEvents) WindowEvents(_ context.Context, _ whsource.Source, w whstore.Window) ([]whevent.Stored, error) {
	return f.events[w.Start], nil
}

type fakeRepairs struct {
	done     map[string]RepairReport
	versions map[string]int
	readErr  error
}

func (f *fakeRepairs) RepairedVersion(_ context.Context, source string) (int, error) {
	return f.versions[source], f.readErr
}

func (f *fakeRepairs) RecordRepair(_ context.Context, source string, version int, r RepairReport) error {
	if f.done == nil {
		f.done, f.versions = map[string]RepairReport{}, map[string]int{}
	}
	f.done[source], f.versions[source] = r, version
	return nil
}

func mailEvent(t *testing.T, id string, resourceIDs ...string) whevent.Stored {
	t.Helper()
	atts := make([]Attachment, 0, len(resourceIDs))
	for _, r := range resourceIDs {
		atts = append(atts, Attachment{Name: "x", ResourceID: r})
	}
	payload, err := json.Marshal(Payload{Attachments: atts})
	require.NoError(t, err)
	var e whevent.Stored
	e.EventID, e.EventType, e.Payload = id, EventType, payload
	return e
}

// otherEvent is an event of another type, which the repair reads past.
func otherEvent() whevent.Stored {
	var e whevent.Stored
	e.EventType, e.Payload = "other", []byte(`{}`)
	return e
}

// TestRepair_MovesNamedFoldersAndRemovesLaterCopies is #2836's repair: the
// folder the kept event names moves to the message's address, the later copy
// no event names (same day, same files) is deleted with its folder, and a
// folder no event names that matches nothing is kept and listed.
func TestRepair_MovesNamedFoldersAndRemovesLaterCopies(t *testing.T) {
	day := "webhooks/inbox/attachments/2026-11-04/"
	lib := &fakeLibrary{files: map[string]StoredFile{
		"r1": {ID: "r1", Folder: day + "inbox-7-3", Name: "a.pdf", Size: 10},
		"r2": {ID: "r2", Folder: day + "inbox-7-3", Name: "b.png", Size: 20},
		"r3": {ID: "r3", Folder: day + "inbox-9-3", Name: "a.pdf", Size: 10},
		"r4": {ID: "r4", Folder: day + "inbox-9-3", Name: "b.png", Size: 20},
		"r5": {ID: "r5", Folder: day + "inbox-9-5", Name: "c.txt", Size: 5},
		// Already the new form, and a form's file deeper down: not the repair's.
		"r6": {ID: "r6", Folder: day + MessageFolder("<m2@x>"), Name: "d.txt", Size: 1},
		"r7": {ID: "r7", Folder: day + "evt_1/upload", Name: "e.txt", Size: 1},
		// The same files on another day are not a copy of the named folder.
		"r8": {ID: "r8", Folder: "webhooks/inbox/attachments/2026-11-05/inbox-9-4", Name: "a.pdf", Size: 10},
		"r9": {ID: "r9", Folder: "webhooks/inbox/attachments/2026-11-05/inbox-9-4", Name: "b.png", Size: 20},
	}}
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	events := &fakeEvents{
		windows: []whstore.Window{{Source: "inbox", Start: start}},
		events:  map[time.Time][]whevent.Stored{start: {mailEvent(t, "<m1@x>", "r1", "r2"), otherEvent()}},
	}
	store := &fakeRepairs{}
	r := &Repairer{Library: lib, Events: events, Store: store}
	src := whsource.Source{Name: "inbox"}

	owed, err := r.Owed(context.Background(), src)
	require.NoError(t, err)
	require.True(t, owed)

	report, err := r.Repair(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, []FolderMove{{From: day + "inbox-7-3", To: day + MessageFolder("<m1@x>")}}, report.Moved)
	assert.Equal(t, []RemovedFolder{{Folder: day + "inbox-9-3", Files: 2}}, report.Removed)
	assert.Equal(t, 2, report.RemovedFiles())
	assert.ElementsMatch(t, []string{day + "inbox-9-5", "webhooks/inbox/attachments/2026-11-05/inbox-9-4"}, report.Kept)

	assert.NotContains(t, lib.files, "r3")
	assert.NotContains(t, lib.files, "r4")
	assert.Equal(t, map[string]string{"r3": "r1", "r4": "r2"}, lib.handedTo,
		"each copy's addresses go to the file of the same name that stays")
	assert.Equal(t, day+MessageFolder("<m1@x>"), lib.files["r1"].Folder, "the named folder answers at the message's address")
	assert.Equal(t, []string{day + "inbox-9-3"}, lib.deletedFolders)
	assert.Equal(t, report, store.done["inbox"], "the report is recorded")

	owed, err = r.Owed(context.Background(), src)
	require.NoError(t, err)
	assert.False(t, owed, "a repaired source is not repaired again")
}

// TestRepair_TwoFoldersOfOneMessageKeepTheSecond: two kept events of one
// Message-ID in different windows each name their own folder; the first takes
// the address and the second is kept rather than deleted.
func TestRepair_TwoFoldersOfOneMessageKeepTheSecond(t *testing.T) {
	day := "webhooks/inbox/attachments/2026-11-04/"
	lib := &fakeLibrary{files: map[string]StoredFile{
		"r1": {ID: "r1", Folder: day + "inbox-7-3", Name: "a.pdf", Size: 10},
		"r2": {ID: "r2", Folder: day + "inbox-9-3", Name: "a.pdf", Size: 10},
	}}
	a, b := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	events := &fakeEvents{
		windows: []whstore.Window{{Start: a}, {Start: b}},
		events:  map[time.Time][]whevent.Stored{a: {mailEvent(t, "<m1@x>", "r1")}, b: {mailEvent(t, "<m1@x>", "r2")}},
	}
	report, err := (&Repairer{Library: lib, Events: events, Store: &fakeRepairs{}}).Repair(context.Background(), whsource.Source{Name: "inbox"})
	require.NoError(t, err)
	assert.Len(t, report.Moved, 1)
	assert.Equal(t, []string{day + "inbox-9-3"}, report.Kept)
	assert.Empty(t, report.Removed)
}

// TestRepair_NothingToDoStillRecords: a source with no folder of the old form
// is recorded without reading its events.
func TestRepair_NothingToDoStillRecords(t *testing.T) {
	store := &fakeRepairs{}
	events := &fakeEvents{err: errors.New("must not be read")}
	report, err := (&Repairer{Library: &fakeLibrary{files: map[string]StoredFile{}}, Events: events, Store: store}).
		Repair(context.Background(), whsource.Source{Name: "fresh"})
	require.NoError(t, err)
	assert.Empty(t, report.Moved)
	assert.Contains(t, store.done, "fresh")
}

// TestRepair_AFailureLeavesItOwed: a failed list, event read or move records
// nothing, so the next poll runs the repair again.
func TestRepair_AFailureLeavesItOwed(t *testing.T) {
	day := "webhooks/inbox/attachments/2026-11-04/"
	files := map[string]StoredFile{"r1": {ID: "r1", Folder: day + "inbox-7-3", Name: "a.pdf", Size: 10}}
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	ok := &fakeEvents{windows: []whstore.Window{{Start: start}}, events: map[time.Time][]whevent.Stored{start: {mailEvent(t, "<m@x>", "r1")}}}
	for name, r := range map[string]*Repairer{
		"list":   {Library: &fakeLibrary{listErr: errors.New("boom")}, Events: ok},
		"events": {Library: &fakeLibrary{files: files}, Events: &fakeEvents{err: errors.New("boom")}},
		"move":   {Library: &fakeLibrary{files: files, moveErr: errors.New("boom")}, Events: ok},
	} {
		store := &fakeRepairs{}
		r.Store = store
		_, err := r.Repair(context.Background(), whsource.Source{Name: "inbox"})
		require.Error(t, err, name)
		assert.Empty(t, store.done, name)
	}
	_, err := (&Repairer{Store: &fakeRepairs{readErr: errors.New("db")}}).Owed(context.Background(), whsource.Source{Name: "x"})
	assert.Error(t, err)
}

// TestRepair_CollapsesCopiesNoEventNames is #2842: a message whose event has
// expired, filed again under each UIDVALIDITY the server gave its folder, is
// left as one folder, the first numbering, where it is; its copies' files hand
// their addresses to it and are counted as collapsed, not kept. Two messages
// that share one UID in different folders, the same UID under another
// numbering holding other files, and the same folder and UID on another day
// are not copies.
func TestRepair_CollapsesCopiesNoEventNames(t *testing.T) {
	day := "webhooks/inbox/attachments/2018-06-20/"
	lib := &fakeLibrary{files: map[string]StoredFile{
		"a1": {ID: "a1", Folder: day + "inbox-1538884683-11033", Name: "plan.pdf", Size: 10},
		"a2": {ID: "a2", Folder: day + "inbox-1538884683-11033", Name: "logo.png", Size: 4},
		"b1": {ID: "b1", Folder: day + "inbox-1794972606-11033", Name: "plan.pdf", Size: 10},
		"b2": {ID: "b2", Folder: day + "inbox-1794972606-11033", Name: "logo.png", Size: 4},
		// A third numbering, written before the second one sorted: still a copy.
		"c1": {ID: "c1", Folder: day + "inbox-1694972606-11033", Name: "plan.pdf", Size: 10},
		"c2": {ID: "c2", Folder: day + "inbox-1694972606-11033", Name: "logo.png", Size: 4},
		// Same UID, another numbering, other files: another message.
		"d1": {ID: "d1", Folder: day + "inbox-1894972606-11033", Name: "other.pdf", Size: 10},
		// Same UID in another folder: another message.
		"e1": {ID: "e1", Folder: day + "sent-1794972606-11033", Name: "plan.pdf", Size: 10},
		"e2": {ID: "e2", Folder: day + "sent-1794972606-11033", Name: "logo.png", Size: 4},
		// Same folder and UID on another day: another message.
		"f1": {ID: "f1", Folder: "webhooks/inbox/attachments/2018-06-21/inbox-1794972606-11033", Name: "plan.pdf", Size: 10},
	}}
	store := &fakeRepairs{}
	report, err := (&Repairer{Library: lib, Events: &fakeEvents{}, Store: store}).Repair(context.Background(), whsource.Source{Name: "inbox"})
	require.NoError(t, err)

	assert.Empty(t, report.Moved)
	assert.Empty(t, report.Removed)
	assert.Equal(t, []RemovedFolder{
		{Folder: day + "inbox-1694972606-11033", Files: 2},
		{Folder: day + "inbox-1794972606-11033", Files: 2},
	}, report.Collapsed)
	assert.Equal(t, 4, report.CollapsedFiles())
	assert.Equal(t, []string{
		day + "inbox-1538884683-11033",
		day + "inbox-1894972606-11033",
		day + "sent-1794972606-11033",
		"webhooks/inbox/attachments/2018-06-21/inbox-1794972606-11033",
	}, report.Kept, "kept lists only the folders that are a copy of nothing")

	assert.Equal(t, map[string]string{"b1": "a1", "b2": "a2", "c1": "a1", "c2": "a2"}, lib.handedTo)
	assert.Equal(t, day+"inbox-1538884683-11033", lib.files["a1"].Folder, "the first numbering stays where it is")
	assert.ElementsMatch(t, []string{day + "inbox-1694972606-11033", day + "inbox-1794972606-11033"}, lib.deletedFolders)
	assert.Equal(t, RepairVersion, store.versions["inbox"])
}

// TestRepair_ASourceRepairedByTheFirstVersionIsRepairedAgain: a source the
// first repair recorded is owed to this one, and a source this one recorded is
// not (#2842).
func TestRepair_ASourceRepairedByTheFirstVersionIsRepairedAgain(t *testing.T) {
	store := &fakeRepairs{versions: map[string]int{"old": 1, "new": RepairVersion}}
	r := &Repairer{Store: store}
	owed, err := r.Owed(context.Background(), whsource.Source{Name: "old"})
	require.NoError(t, err)
	assert.True(t, owed, "repaired by version 1")
	owed, err = r.Owed(context.Background(), whsource.Source{Name: "new"})
	require.NoError(t, err)
	assert.False(t, owed)
}

// TestRepair_AFailedCollapseLeavesItOwed: a copy that cannot be removed fails
// the repair before it is recorded.
func TestRepair_AFailedCollapseLeavesItOwed(t *testing.T) {
	day := "webhooks/inbox/attachments/2018-06-20/"
	lib := &fakeLibrary{removeErr: errors.New("boom"), files: map[string]StoredFile{
		"a1": {ID: "a1", Folder: day + "inbox-1-7", Name: "x", Size: 1},
		"b1": {ID: "b1", Folder: day + "inbox-2-7", Name: "x", Size: 1},
	}}
	store := &fakeRepairs{}
	_, err := (&Repairer{Library: lib, Events: &fakeEvents{}, Store: store}).Repair(context.Background(), whsource.Source{Name: "inbox"})
	require.Error(t, err)
	assert.Empty(t, store.done)
}
