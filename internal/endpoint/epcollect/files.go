package epcollect

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// filesAtOnce is how many of a submission's files are saved at once.
const filesAtOnce = 4

// ErrCannotSaveFiles is a form declaring a file field where files cannot be
// saved as managed resources.
var ErrCannotSaveFiles = errors.New("the form declares file fields, which are saved as managed resources, and this deployment has no managed resources or no webhook receiver to store them through")

// File is one file of a submission to save (#2333): the event it belongs to
// and when that was received, the field it was sent in, the name it is saved
// under, its type and bytes.
type File struct {
	EventID     string
	At          time.Time
	Field       string
	Name        string
	ContentType string
	Data        []byte
}

// Files saves a form's files as managed resources in its source's library,
// under the folder the source's attachments are kept in, which is removed
// with the source.
type Files interface {
	// FileURI is the address f will be saved at.
	FileURI(src whsource.Source, f File) string
	// PutFile saves f and says where.
	PutFile(ctx context.Context, src whsource.Source, f File) (uri, id string, err error)
	// DeleteFile removes a saved file.
	DeleteFile(ctx context.Context, id string) error
}

// CanSaveFiles reports whether a form's files can be saved here.
func (c *Collector) CanSaveFiles() bool {
	return c != nil && c.Files != nil
}

// Prepare readies a submission that carries files before form_submit runs:
// it names the event the submission will be stored as, and the address each
// file will be saved at, which it writes into the file's description in
// sub.Values, so form_submit is handed the address the stored event carries.
// Nothing is saved: Store saves the files once form_submit accepted. A file
// name the platform does not store (an extension it refuses) is the field's
// error.
func (c *Collector) Prepare(ctx context.Context, sub *Submission, uploads []epform.Upload) (map[string]string, error) {
	errs := map[string]string{}
	if len(uploads) == 0 {
		return errs, nil
	}
	if !c.CanSaveFiles() {
		return nil, ErrCannotSaveFiles
	}
	src, ok := c.Sources.FormSource(ctx, sub.ScriptID)
	if !ok {
		return nil, ErrNoSource
	}
	id, err := newEventID()
	if err != nil {
		return nil, err
	}
	sub.EventID, sub.ReceivedAt = id, c.now().UTC()
	taken := map[string]bool{}
	index := map[string]int{}
	for _, u := range uploads {
		i := index[u.Field]
		index[u.Field]++
		desc := describedFile(sub.Values, u.Field, i)
		if desc == nil {
			return nil, fmt.Errorf("file %d of field %q was not checked", i, u.Field)
		}
		name, err := resource.SanitizeFilename(u.Name)
		if err != nil {
			errs[u.Field] = fmt.Sprintf("%q cannot be saved: %v", u.Name, err)
			continue
		}
		f := File{EventID: id, At: sub.ReceivedAt, Field: u.Field, Name: unique(taken, u.Field, name), ContentType: u.ContentType(), Data: u.Data}
		desc[epform.FileKeyURI] = c.Files.FileURI(src, f)
		sub.Files = append(sub.Files, f)
	}
	if len(errs) > 0 {
		sub.Files = nil
	}
	return errs, nil
}

// describedFile is the description of file i of field in values.
func describedFile(values map[string]any, field string, i int) map[string]any {
	list, _ := values[field].([]any)
	if i >= len(list) {
		return nil
	}
	desc, _ := list[i].(map[string]any)
	return desc
}

// unique is name, or name with a number before its extension, so no two
// files of one field share an address.
func unique(taken map[string]bool, field, name string) string {
	out := name
	ext := filepath.Ext(name)
	for n := 2; taken[field+"/"+out]; n++ {
		out = strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(n) + ext
	}
	taken[field+"/"+out] = true
	return out
}

// saveFiles saves a submission's files, writing where each was saved into its
// description, and returns the ids saved; on a failure it removes those it
// saved and returns the failure.
func (c *Collector) saveFiles(ctx context.Context, src whsource.Source, sub Submission) ([]string, error) {
	var mu sync.Mutex
	var saved []string
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(filesAtOnce)
	index := map[string]int{}
	for _, f := range sub.Files {
		desc := describedFile(sub.Values, f.Field, index[f.Field])
		index[f.Field]++
		g.Go(func() error {
			uri, id, err := c.Files.PutFile(gctx, src, f)
			if err != nil {
				return fmt.Errorf("saving file %q of field %q: %w", f.Name, f.Field, err)
			}
			mu.Lock()
			defer mu.Unlock()
			saved = append(saved, id)
			if desc != nil {
				desc[epform.FileKeyURI], desc[epform.FileKeyResourceID] = uri, id
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		c.removeFiles(ctx, saved)
		return nil, err //nolint:wrapcheck // the goroutine's error names the file
	}
	return saved, nil
}

// removeFiles removes files saved for a submission that was not stored. A
// removal that fails leaves the file, which is better than refusing the
// submission's refusal; the source's deletion removes it with the rest.
func (c *Collector) removeFiles(ctx context.Context, ids []string) {
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		_ = c.Files.DeleteFile(ctx, id)
	}
}
