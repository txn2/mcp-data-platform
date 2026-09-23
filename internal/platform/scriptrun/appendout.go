package scriptrun

import (
	"errors"
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptout"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout/exportrecord"
)

// Appended outputs (#1861). platform.export(..., append=True) adds a page of
// rows to an output the run builds across calls; the page is serialized at
// once and its Starlark values are the script's to drop, so a run can page an
// API into one file without holding every page. The first call to a name and
// destination starts the output and fixes where it goes, what it registers and
// what it is tagged with; every later call adds rows. The output is written
// once, when the script finishes: a run that fails part-way writes none of
// it, because a partial file under a registered table is a dataset that
// silently lost its tail.

// appending returns the output the run is appending to under name and
// destination, or nil.
func (h *hostState) appending(name, destination string) *ExportRequest {
	for _, req := range h.appended {
		if req.Name == name && req.Destination.Name == destination {
			return req
		}
	}
	return nil
}

// appendExport adds one page to an appended output, starting it on the first.
func (h *hostState) appendExport(b *starlark.Builtin, page ExportRequest) (starlark.Value, error) {
	out := h.appending(page.Name, page.Destination.Name)
	switch {
	case !page.Append:
		return nil, fmt.Errorf("in %s: output %q is being appended to in this run; pass append=True on every call that writes it",
			b.Name(), page.Name)
	case page.Rows == nil:
		return nil, fmt.Errorf("in %s: append=True adds rows, a list of dicts; a document or a workbook is written whole",
			b.Name())
	case out == nil:
		started, err := h.startAppend(b, page)
		if err != nil {
			return nil, err
		}
		out = started
	case out.Key != page.Key:
		return nil, fmt.Errorf("in %s: output %q was started at key %q; every page of it goes to the same file",
			b.Name(), page.Name, out.Key)
	}
	if err := out.Spooled.Append(page.Columns, page.Rows); err != nil {
		return nil, argErr(b, err)
	}
	h.mem.Holding(h.appendedBytes())
	return exportrecord.AppendingValue(exportrecord.Appending{
		Name: out.Name, Destination: out.Destination.Name, Format: out.Format,
		Rows: out.Spooled.Rows(), Bytes: out.Spooled.Bytes(), Preview: h.opts.Exporter == nil,
	}), nil
}

// startAppend starts an appended output under the rules every output meets:
// the per-run budget and one output per name and destination.
func (h *hostState) startAppend(b *starlark.Builtin, page ExportRequest) (*ExportRequest, error) {
	if len(h.exports)+len(h.appended) >= maxExports {
		return nil, fmt.Errorf("in %s: a run may produce at most %d outputs", b.Name(), maxExports)
	}
	if err := h.admitOutput(b, page.Name, page.Destination.Name); err != nil {
		return nil, err
	}
	spool, err := scriptout.NewSpool(page.Name, page.Format)
	if err != nil {
		return nil, argErr(b, err)
	}
	out := page
	out.Rows, out.Columns, out.Spooled = nil, nil, spool
	h.appended = append(h.appended, &out)
	return &out, nil
}

// appendedBytes is what the appended outputs hold so far.
func (h *hostState) appendedBytes() int64 {
	var n int64
	for _, req := range h.appended {
		n += int64(req.Spooled.Bytes())
	}
	return n
}

// landAppended writes every appended output, and registers the table one
// asked for, as platform.export writes any other output.
func (h *hostState) landAppended() error {
	b := starlark.NewBuiltin(CapabilityExport, nil)
	var errs []error
	for _, req := range h.appended {
		record, err := h.persistOrPreview(b, *req)
		if err == nil && req.Register != nil {
			record.Table, err = h.registerOutput(b, req.Register, record)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		h.exports = append(h.exports, record)
	}
	h.appended = nil
	return errors.Join(errs...) //nolint:wrapcheck // each error names the output it failed
}
