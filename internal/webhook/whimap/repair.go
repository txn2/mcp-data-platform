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
//   - a folder no event names is a copy of another folder no event names when
//     both are the same folder and UID under different UIDVALIDITY values, on
//     the same day, holding the same files (#2842): the message's event has
//     expired, and the server renumbered the folder before the upgrade. The
//     copy with the lowest UIDVALIDITY, the first numbering, stays where it
//     is, and the others are deleted.
//   - a folder no event names and nothing matches is kept and listed: its
//     message's event may have expired, and its files may be all that is left
//     of it.
//   - a folder an event names whose message folder already holds files is not
//     moved and is listed as not moved (#2845). Another folder of the same
//     message took the address first, in this pass or an earlier one: one
//     message filed twice, two kept events each naming its own files. Moving it
//     there would be refused when the two hold a file of one name and would
//     merge two events' files into one folder when they do not, and deleting it
//     as a copy would leave its event naming a resource id that no longer
//     exists, since a handover moves addresses, not ids.
//
// A folder whose move or removal fails is listed with its error and left where
// it is, and the pass goes on to the rest and is recorded (#2845): a refusal
// that repeats would otherwise run the whole repair again on every poll and
// never clear, and a folder left in the old form still answers where it is.
//
// A copy is deleted file by file, each file's addresses (its own and any an
// earlier move left aliased to it) handed to the file of the same name in the
// folder that stays, so a reference to the copy still resolves, as a move
// keeps one to the address it vacates.
//
// The repair is recorded with its version. A source repaired by an earlier
// version is repaired again by this one, once: what the earlier pass moved is
// of the new form and not touched, so the second pass reads only the folders
// it kept.

// RepairVersion is the version of the repair this build runs. 1 was #2836's;
// 2 adds the collapse of copies no event names (#2842).
const RepairVersion = 2

// oldFolder matches a message folder of the pre-#2836 form, capturing the
// folder name, the UIDVALIDITY and the UID.
var oldFolder = regexp.MustCompile(`^(?:([a-z0-9-]*)-)?(\d+)-(\d+)$`)

// The groups oldFolder captures.
const (
	groupMailbox = iota + 1
	groupValidity
	groupUID
)

// A UIDVALIDITY is read as a decimal into 64 bits.
const (
	validityBase = 10
	validityBits = 64
)

// sep separates the segments of a library path.
const sep = "/"

// RepairReport is what a repair did.
type RepairReport struct {
	// Moved are the folders moved to the new form.
	Moved []FolderMove `json:"moved"`
	// Removed are the later copies deleted, each with how many files it held.
	Removed []RemovedFolder `json:"removed"`
	// Collapsed are the copies of a folder no event names, deleted into the
	// one that stays (#2842), each with how many files it held.
	Collapsed []RemovedFolder `json:"collapsed"`
	// Kept are the folders of the old form no event names that are a copy of
	// nothing.
	Kept []string `json:"kept"`
	// Unmoved are the folders an event names left where they are because the
	// message's folder already held files (#2845).
	Unmoved []string `json:"unmoved"`
	// Failed are the folders a move or removal failed on, each with what the
	// library said, left where they were (#2845).
	Failed []FailedFolder `json:"failed"`
}

// FailedFolder is one folder the repair could not move or remove.
type FailedFolder struct {
	Folder string `json:"folder"`
	Error  string `json:"error"`
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

// RemovedFiles is how many files the repair deleted as later copies of a
// folder an event names.
func (r RepairReport) RemovedFiles() int { return fileCount(r.Removed) }

// CollapsedFiles is how many files the repair deleted as copies of a folder no
// event names.
func (r RepairReport) CollapsedFiles() int { return fileCount(r.Collapsed) }

func fileCount(folders []RemovedFolder) int {
	n := 0
	for _, f := range folders {
		n += f.Files
	}
	return n
}

// newReport is an empty report, its lists empty rather than null.
func newReport() RepairReport {
	return RepairReport{
		Moved: []FolderMove{}, Removed: []RemovedFolder{}, Collapsed: []RemovedFolder{},
		Kept: []string{}, Unmoved: []string{}, Failed: []FailedFolder{},
	}
}

// Repairer repairs the attachments of imap sources.
type Repairer struct {
	Library AttachmentLibrary
	Events  EventReader
	Store   RepairStore
}

// Owed reports whether src has not been repaired by this version yet.
func (r *Repairer) Owed(ctx context.Context, src whsource.Source) (bool, error) {
	version, err := r.Store.RepairedVersion(ctx, src.Name)
	if err != nil {
		return false, fmt.Errorf("reading the attachment repair of %s: %w", src.Name, err)
	}
	return version < RepairVersion, nil
}

// Repair repairs src's attachments and records what it did. A failure to list
// the attachments or read the events leaves it owed, so the next poll tries
// again; a folder that fails is listed in the report, and the pass recorded.
func (r *Repairer) Repair(ctx context.Context, src whsource.Source) (RepairReport, error) {
	files, err := r.Library.ListAttachments(ctx, src)
	if err != nil {
		return RepairReport{}, fmt.Errorf("listing the attachments of %s: %w", src.Name, err)
	}
	folders := groupOldFolders(src.Name, files)
	report := newReport()
	if len(folders) > 0 {
		owners, err := r.owners(ctx, src)
		if err != nil {
			return RepairReport{}, err
		}
		report = r.apply(ctx, src, folders, occupied(files), owners)
	}
	if err := r.Store.RecordRepair(ctx, src.Name, RepairVersion, report); err != nil {
		return RepairReport{}, fmt.Errorf("recording the attachment repair of %s: %w", src.Name, err)
	}
	return report, nil
}

// messageFolder is one folder of the old form: its path, its day, the folder
// name, UIDVALIDITY and UID its name carries, and its files.
type messageFolder struct {
	path     string
	day      string
	mailbox  string
	validity uint64
	uid      string
	files    []StoredFile
}

// copyKey is what two copies of one message no event names share: the day,
// the folder and the UID. Only the UIDVALIDITY differs between them.
func (f messageFolder) copyKey() string { return f.day + "\x02" + f.mailbox + "\x02" + f.uid }

// namedKey is what a later copy shares with a folder an event names: the day
// and the files.
func (f messageFolder) namedKey() string { return f.day + "\x02" + f.signature() }

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
		if !ok || strings.Contains(key, sep) {
			continue
		}
		m := oldFolder.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		mf := byPath[f.Folder]
		if mf == nil {
			// The pattern admits digits only, so a parse fails only past
			// uint64, which no server's UIDVALIDITY (a 32-bit value) reaches.
			validity, _ := strconv.ParseUint(m[groupValidity], validityBase, validityBits)
			mf = &messageFolder{path: f.Folder, day: day, mailbox: m[groupMailbox], validity: validity, uid: m[groupUID]}
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

// occupied is every folder holding a file: a message folder of the new form
// is taken when it is among them, whatever moved or saved its files there.
func occupied(files []StoredFile) map[string]bool {
	out := make(map[string]bool, len(files))
	for _, f := range files {
		out[f.Folder] = true
	}
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

// apply moves the folders an event names, deletes each folder no event names
// that a named folder of its day matches, and collapses the copies of each
// message no event names. taken is every folder already holding a file.
func (r *Repairer) apply(
	ctx context.Context, src whsource.Source, folders []messageFolder, taken map[string]bool, owners map[string]string,
) RepairReport {
	report := newReport()
	named, unnamed := r.moveNamed(ctx, src, folders, taken, owners, &report)
	rest := make([]messageFolder, 0, len(unnamed))
	for _, f := range unnamed {
		keep, ok := named[f.namedKey()]
		if !ok {
			rest = append(rest, f)
			continue
		}
		if err := r.removeCopy(ctx, src, f, keep); err != nil {
			report.fail(f, err)
			continue
		}
		report.Removed = append(report.Removed, RemovedFolder{Folder: f.path, Files: len(f.files)})
	}
	r.collapse(ctx, src, rest, &report)
	sort.Strings(report.Kept)
	sort.Strings(report.Unmoved)
	slices.SortFunc(report.Failed, func(a, b FailedFolder) int { return strings.Compare(a.Folder, b.Folder) })
	return report
}

// fail lists f as a folder the repair could not move or remove.
func (r *RepairReport) fail(f messageFolder, err error) {
	r.Failed = append(r.Failed, FailedFolder{Folder: f.path, Error: err.Error()})
}

// collapse deletes each folder no event names into the first numbering of the
// same message (#2842): the folder of the same day, folder name and UID with
// the lowest UIDVALIDITY that holds the same files. A folder that matches no
// earlier numbering is kept, and one whose removal fails is listed as failed.
func (r *Repairer) collapse(ctx context.Context, src whsource.Source, folders []messageFolder, report *RepairReport) {
	slices.SortFunc(folders, func(a, b messageFolder) int {
		if c := strings.Compare(a.copyKey(), b.copyKey()); c != 0 {
			return c
		}
		if a.validity != b.validity {
			return cmpUint(a.validity, b.validity)
		}
		return strings.Compare(a.path, b.path)
	})
	keepers := map[string][]messageFolder{}
	for _, f := range folders {
		key := f.copyKey()
		keep, ok := sameFiles(keepers[key], f)
		if !ok {
			keepers[key] = append(keepers[key], f)
			report.Kept = append(report.Kept, f.path)
			continue
		}
		if err := r.removeCopy(ctx, src, f, keep); err != nil {
			report.fail(f, err)
			continue
		}
		report.Collapsed = append(report.Collapsed, RemovedFolder{Folder: f.path, Files: len(f.files)})
	}
}

func cmpUint(a, b uint64) int {
	if a < b {
		return -1
	}
	return 1
}

// sameFiles is the folder among keepers holding the files f holds.
func sameFiles(keepers []messageFolder, f messageFolder) (messageFolder, bool) {
	sig := f.signature()
	for _, k := range keepers {
		if k.signature() == sig {
			return k, true
		}
	}
	return messageFolder{}, false
}

// moveNamed moves each folder an event names to the message's folder of the
// new form, unless that folder already holds files, and returns each named
// folder by its day and files, and the folders no event names.
func (r *Repairer) moveNamed(
	ctx context.Context, src whsource.Source, folders []messageFolder, taken map[string]bool, owners map[string]string,
	report *RepairReport,
) (named map[string]messageFolder, unnamed []messageFolder) {
	named = map[string]messageFolder{}
	for _, f := range folders {
		owner := ownerOf(f, owners)
		if owner == "" {
			unnamed = append(unnamed, f)
			continue
		}
		if _, seen := named[f.namedKey()]; !seen {
			named[f.namedKey()] = f
		}
		to := path.Join(path.Dir(f.path), MessageFolder(owner))
		// Two folders named by one message id are two kept events of it; each
		// event names its own files, so neither is a copy to delete, and only
		// the first to reach the address takes it, in this pass or an earlier
		// one. The library holding files there is what says it was taken.
		if taken[to] {
			report.Unmoved = append(report.Unmoved, f.path)
			continue
		}
		if err := r.Library.MoveFolder(ctx, src, f.path, to); err != nil {
			report.fail(f, fmt.Errorf("moving to %s: %w", to, err))
			continue
		}
		taken[to] = true
		report.Moved = append(report.Moved, FolderMove{From: f.path, To: to})
	}
	return named, unnamed
}

// removeCopy deletes a copy's files and its stored folder, each file's
// addresses handed to the file of the same name in keep. A move keeps a
// file's id, so keep's files are found by the ids listed before it moved.
func (r *Repairer) removeCopy(ctx context.Context, src whsource.Source, f, keep messageFolder) error {
	byName := make(map[string]string, len(keep.files))
	for _, file := range keep.files {
		byName[file.Name] = file.ID
	}
	for _, file := range f.files {
		if err := r.Library.RemoveCopy(ctx, src, file.ID, byName[file.Name]); err != nil {
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
