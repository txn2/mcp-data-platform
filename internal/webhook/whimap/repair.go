package whimap

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// The one-time repair of a source's attachments (#2836).
//
// Before #2836 a message's attachments were saved in a folder named by the
// server's numbering, {folder}-{uidvalidity}-{uid}. A server that renumbered a
// folder had every message read again, each attachment landed at a new
// address, and a second copy of the mailbox's attachments was filed. The
// events did not duplicate: compaction keeps one event per Message-ID, the
// first, so the kept event names the first copy and nothing names the second.
//
// The repair runs once per imap source, before its first poll after the
// upgrade, under the source's poll lock. For each folder of the old form:
//
//   - a folder an event names (a kept event lists one of its files) is moved to
//     the message's folder of the new form (MessageFolder of the event id), so
//     the next re-read finds each file at its address. A move records each
//     file's old address as an alias, so the URIs the events hold still
//     resolve.
//   - a folder no event names is a later copy when a folder an event names on
//     the same day holds the same files (names and sizes): its files and its
//     stored folder are deleted.
//   - a folder no event names and no named folder matches is kept and listed:
//     its message's event may have expired, and its files may be all that is
//     left of it.

// oldFolder matches a message folder of the pre-#2836 form.
var oldFolder = regexp.MustCompile(`^(?:[a-z0-9-]*-)?\d+-\d+$`)

// sep separates the segments of a library path.
const sep = "/"

// RepairReport is what a repair did.
type RepairReport struct {
	// Moved are the folders moved to the new form.
	Moved []FolderMove `json:"moved"`
	// Removed are the later copies deleted, each with how many files it held.
	Removed []RemovedFolder `json:"removed"`
	// Kept are the folders of the old form no event names and no named folder
	// matches.
	Kept []string `json:"kept"`
}

// FolderMove is one folder moved.
type FolderMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RemovedFolder is one later copy deleted.
type RemovedFolder struct {
	Folder string `json:"folder"`
	Files  int    `json:"files"`
}

// RemovedFiles is how many files the repair deleted.
func (r RepairReport) RemovedFiles() int {
	n := 0
	for _, f := range r.Removed {
		n += f.Files
	}
	return n
}

// Repairer repairs the attachments of imap sources.
type Repairer struct {
	Library AttachmentLibrary
	Events  EventReader
	Store   RepairStore
}

// Owed reports whether src has not been repaired yet.
func (r *Repairer) Owed(ctx context.Context, src whsource.Source) (bool, error) {
	done, err := r.Store.Repaired(ctx, src.Name)
	if err != nil {
		return false, fmt.Errorf("reading the attachment repair of %s: %w", src.Name, err)
	}
	return !done, nil
}

// Repair repairs src's attachments and records what it did. A failure leaves
// it owed, so the next poll tries again; what it had moved or deleted stays
// done, and a second run finds less to do.
func (r *Repairer) Repair(ctx context.Context, src whsource.Source) (RepairReport, error) {
	files, err := r.Library.ListAttachments(ctx, src)
	if err != nil {
		return RepairReport{}, fmt.Errorf("listing the attachments of %s: %w", src.Name, err)
	}
	folders := groupOldFolders(src.Name, files)
	report := RepairReport{Moved: []FolderMove{}, Removed: []RemovedFolder{}, Kept: []string{}}
	if len(folders) > 0 {
		owners, err := r.owners(ctx, src)
		if err != nil {
			return RepairReport{}, err
		}
		if report, err = r.apply(ctx, src, folders, owners); err != nil {
			return RepairReport{}, err
		}
	}
	if err := r.Store.RecordRepair(ctx, src.Name, report); err != nil {
		return RepairReport{}, fmt.Errorf("recording the attachment repair of %s: %w", src.Name, err)
	}
	return report, nil
}

// messageFolder is one folder of the old form: its path, its day, and its
// files.
type messageFolder struct {
	path  string
	day   string
	files []StoredFile
}

// signature is a folder's files as a comparable value: each name and size.
func (f messageFolder) signature() string {
	parts := make([]string, 0, len(f.files))
	for _, file := range f.files {
		parts = append(parts, file.Name+"\x00"+strconv.FormatInt(file.Size, 10))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// groupOldFolders groups the files that sit directly in a message folder of
// the old form, {attachments}/{day}/{folder}-{validity}-{uid}, by folder, in
// path order.
func groupOldFolders(source string, files []StoredFile) []messageFolder {
	root := AttachmentsFolder(source) + sep
	byPath := map[string]*messageFolder{}
	for _, f := range files {
		rest, ok := strings.CutPrefix(f.Folder, root)
		if !ok {
			continue
		}
		day, key, ok := strings.Cut(rest, sep)
		if !ok || strings.Contains(key, sep) || !oldFolder.MatchString(key) {
			continue
		}
		mf := byPath[f.Folder]
		if mf == nil {
			mf = &messageFolder{path: f.Folder, day: day}
			byPath[f.Folder] = mf
		}
		mf.files = append(mf.files, f)
	}
	out := make([]messageFolder, 0, len(byPath))
	for _, mf := range byPath {
		out = append(out, *mf)
	}
	slices.SortFunc(out, func(a, b messageFolder) int { return strings.Compare(a.path, b.path) })
	return out
}

// owners maps each attachment resource a kept event names to that event's id.
func (r *Repairer) owners(ctx context.Context, src whsource.Source) (map[string]string, error) {
	windows, err := r.Events.Live(ctx, src.Name)
	if err != nil {
		return nil, fmt.Errorf("listing the windows of %s: %w", src.Name, err)
	}
	owners := map[string]string{}
	for _, w := range windows {
		events, err := r.Events.WindowEvents(ctx, src, w)
		if err != nil {
			return nil, fmt.Errorf("reading the events of %s: %w", src.Name, err)
		}
		addOwners(owners, events)
	}
	return owners, nil
}

// addOwners records, for each mail event, the event id that names each of its
// attachments' resources.
func addOwners(owners map[string]string, events []whevent.Stored) {
	for _, e := range events {
		if e.EventType != EventType {
			continue
		}
		var p struct {
			Attachments []Attachment `json:"attachments"`
		}
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		for _, a := range p.Attachments {
			if a.ResourceID != "" {
				owners[a.ResourceID] = e.EventID
			}
		}
	}
}

// apply moves the folders an event names, then deletes each folder no event
// names that a moved folder of its day matches.
func (r *Repairer) apply(ctx context.Context, src whsource.Source, folders []messageFolder, owners map[string]string) (RepairReport, error) {
	report := RepairReport{Moved: []FolderMove{}, Removed: []RemovedFolder{}, Kept: []string{}}
	named, unnamed, err := r.moveNamed(ctx, src, folders, owners, &report)
	if err != nil {
		return report, err
	}
	for _, f := range unnamed {
		if !named[f.day+"\x02"+f.signature()] {
			report.Kept = append(report.Kept, f.path)
			continue
		}
		if err := r.removeCopy(ctx, src, f); err != nil {
			return report, err
		}
		report.Removed = append(report.Removed, RemovedFolder{Folder: f.path, Files: len(f.files)})
	}
	return report, nil
}

// moveNamed moves each folder an event names to the message's folder of the
// new form, and returns the day and files of each, and the folders no event
// names.
func (r *Repairer) moveNamed(
	ctx context.Context, src whsource.Source, folders []messageFolder, owners map[string]string, report *RepairReport,
) (named map[string]bool, unnamed []messageFolder, err error) {
	named, targets := map[string]bool{}, map[string]bool{}
	for _, f := range folders {
		owner := ownerOf(f, owners)
		if owner == "" {
			unnamed = append(unnamed, f)
			continue
		}
		named[f.day+"\x02"+f.signature()] = true
		to := path.Join(path.Dir(f.path), MessageFolder(owner))
		// Two folders named by one message id are two kept events of it, in
		// windows of different days' arrival; each event names its own files,
		// so neither is a copy to delete, and only the first can take the
		// address.
		if targets[to] {
			report.Kept = append(report.Kept, f.path)
			continue
		}
		targets[to] = true
		if err := r.Library.MoveFolder(ctx, src, f.path, to); err != nil {
			return nil, nil, fmt.Errorf("moving %s to %s: %w", f.path, to, err)
		}
		report.Moved = append(report.Moved, FolderMove{From: f.path, To: to})
	}
	return named, unnamed, nil
}

// removeCopy deletes a later copy's files and its stored folder.
func (r *Repairer) removeCopy(ctx context.Context, src whsource.Source, f messageFolder) error {
	for _, file := range f.files {
		if err := r.Library.DeleteFile(ctx, src, file.ID); err != nil {
			return fmt.Errorf("deleting %s from %s: %w", file.ID, f.path, err)
		}
	}
	if err := r.Library.DeleteFolder(ctx, src, f.path); err != nil {
		return fmt.Errorf("deleting the folder %s: %w", f.path, err)
	}
	return nil
}

// ownerOf is the event id naming one of f's files, empty when none does.
func ownerOf(f messageFolder, owners map[string]string) string {
	for _, file := range f.files {
		if id := owners[file.ID]; id != "" {
			return id
		}
	}
	return ""
}
