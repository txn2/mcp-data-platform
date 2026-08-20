package scriptexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// PublishData refreshes the data region of an output asset this script already
// publishes, writing the result as a new version of that asset (#1389).
//
// The split it implements: the presentation lives in the asset, versioned where
// documents are versioned; the data lives in the script, refreshed on the
// script's cadence. The write is structural — the payload replaces the interior
// of the ONE element the data-region selector resolves, through the same
// anchored-editing machinery manage_asset patch uses — so the platform can state
// to a reviewer, truthfully, that this call cannot modify the document's markup.
// Every refreshed version is a self-contained as-of snapshot: a public share of
// the dashboard needs no view-time fetch, and an old version still shows exactly
// the data it showed.
func (w *outputWriter) PublishData(ctx context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	// A data region is a property of a portal document, so the destination is
	// the portal by construction; the host has already checked the grant covers
	// it before this writer is reached.
	destination := script.DestinationPortal
	if err := w.refuseRepeat(req.Name, destination); err != nil {
		return nil, err
	}
	if prior := w.priorAttempt(req.Name, destination); prior != nil {
		return prior, nil
	}
	if !w.deps.ready() {
		return nil, fmt.Errorf("output %q cannot be refreshed: this deployment has no portal asset store or object storage configured", req.Name)
	}

	// The payload is serialized and bounded before any I/O: it depends on
	// nothing but the request, and refusing it here spares the network read of
	// a document that can itself be at the output ceiling.
	payload, err := scriptrun.FormatDataPayload(req.Name, req.Data)
	if err != nil {
		//nolint:wrapcheck // FormatDataPayload names the output and the corrective
		// action; a second wrap here would only repeat the output's name.
		return nil, err
	}
	asset, body, err := w.refreshTarget(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	spliced, err := spliceDataRegion(req.Name, body, contenttype.Normalize(asset.ContentType), payload)
	if err != nil {
		return nil, err
	}

	version, err := w.writeRefreshedVersion(ctx, asset, spliced)
	if err != nil {
		return nil, err
	}
	out := script.RunOutput{
		Name: req.Name, Destination: destination,
		AssetID: asset.ID, AssetVersion: version,
		Format: scriptrun.PublishFormat, RowCount: scriptrun.PublishRowCount(req.Data),
		Refresh: true, Bytes: len(payload),
	}
	w.record(ctx, out)
	return &scriptrun.ExportResult{AssetID: asset.ID, AssetVersion: version, Bytes: len(payload)}, nil
}

// refreshTarget resolves the asset a refresh names — through the same identity
// key an export writes under — and loads its current body. A name that resolves
// to nothing is a refusal that says how the presentation comes to exist, and a
// document of the wrong kind is refused before any bytes move.
func (w *outputWriter) refreshTarget(ctx context.Context, name string) (asset *portal.Asset, body string, err error) {
	asset, err = w.deps.Assets.GetByIdempotencyKey(ctx, w.script.Principal(), w.outputIdentityKey(name))
	// A lookup that FAILED is not an asset that does not exist. The export path
	// may conflate the two because its unique-key insert arbitrates either way;
	// here the answer is terminal — a script failure is never retried — so a
	// transient store error must not become the false, confident statement that
	// the dashboard is missing and should be re-published.
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("resolving output %q: %w", name, err)
	}
	if asset == nil {
		return nil, "", fmt.Errorf("this script has no output asset named %q to refresh, and platform.publish_data replaces a region of an existing document rather than creating one; publish the dashboard once with platform.export(%q, body, format=\"html\") or format=\"jsx\", then refresh its data here",
			name, name)
	}
	ct := contenttype.Normalize(asset.ContentType)
	if ct != contenttype.HTML && ct != contenttype.JSX && ct != contenttype.Markdown {
		return nil, "", fmt.Errorf("output %q is a %s document, which carries no data region; only an html, jsx, or markdown document can be refreshed",
			name, asset.ContentType)
	}
	data, _, err := w.deps.S3.GetObject(ctx, asset.S3Bucket, asset.S3Key)
	if err != nil {
		return nil, "", fmt.Errorf("reading the current version of output %q: %w", name, err)
	}
	return asset, string(data), nil
}

// spliceDataRegion replaces the interior of the document's marked data region
// with the serialized payload, leaving every other byte as the author wrote it.
//
// The edit is the anchored-editing engine's replace_content on the data-region
// selector, never string interpolation, and the selector must resolve to exactly
// one element — the same uniqueness rule every selector edit obeys. A document
// without the region fails the run with the convention spelled out, rather than
// writing anywhere else.
func spliceDataRegion(name, body, kind string, payload []byte) (string, error) {
	res, err := textpatch.Apply(body, []textpatch.Edit{{
		Op:       textpatch.OpReplaceContent,
		Selector: script.DataRegionSelector,
		Text:     regionContent(kind, payload),
	}}, textpatch.Options{
		// The element grammar is named explicitly rather than derived from the
		// content type, because a markdown dashboard carries its island as a
		// raw-HTML block — legal markdown that the document's own region grammar
		// (headings) cannot address. For html and jsx this is what the content
		// type derives anyway.
		Syntax:         textpatch.SyntaxHTML,
		MaxResultBytes: scriptrun.MaxOutputBytes,
	})
	if err != nil {
		return "", refreshRegionError(name, kind, err)
	}
	// The element grammar knows nothing of markdown fences, so on a markdown
	// document an id="data" written as EXAMPLE text inside a fenced code block
	// would match the selector. A splice that landed inside a fence would
	// rewrite the example and leave the real page unrefreshed while the run
	// reports success, so it is refused with the actual explanation.
	if kind == contenttype.Markdown && inMarkdownFence(body, res.Edits[0].Line) {
		return "", fmt.Errorf("output %q: the only %s match sits inside a fenced code block, which is example text rather than the document's data region; add the island as a raw-HTML block outside any fence",
			name, script.DataRegionSelector)
	}
	return res.Body, nil
}

// inMarkdownFence reports whether the 1-based line falls inside a fenced code
// block. Fences are line-based: a line opening with ``` or ~~~ starts one, and
// only a line opening with the same delimiter ends it, so a ``` line quoted
// inside a ~~~ block stays content.
func inMarkdownFence(body string, line int) bool {
	var fence byte
	for i, l := range strings.Split(body, "\n") {
		t := strings.TrimLeft(l, " \t")
		var delim byte
		switch {
		case strings.HasPrefix(t, "```"):
			delim = '`'
		case strings.HasPrefix(t, "~~~"):
			delim = '~'
		}
		switch {
		case fence == 0 && delim != 0:
			fence = delim
		case fence != 0 && delim == fence:
			fence = 0
		case i+1 == line:
			return fence != 0
		}
	}
	return false
}

// refreshRegionError translates a splice failure into the author's terms: what
// the document is missing and how to mark it, rather than a patch engine code.
func refreshRegionError(name, kind string, err error) error {
	var patchErr *textpatch.Error
	if errors.As(err, &patchErr) {
		switch patchErr.Code {
		case textpatch.CodeSectionNotFound:
			return fmt.Errorf("output %q has no data region: no element matches %s; mark one in the document, conventionally <script type=\"application/json\" id=\"data\">...</script>, and the refresh replaces only its content",
				name, script.DataRegionSelector)
		case textpatch.CodeAmbiguous:
			msg := fmt.Sprintf("output %q marks more than one element as %s; a document has one data region, so give the others different ids",
				name, script.DataRegionSelector)
			// On markdown the extra match is as likely to be example markup
			// quoted in a code fence, which the element grammar cannot tell from
			// the real island — so the author is told what actually collides.
			if kind == contenttype.Markdown {
				msg += " (an id=\"data\" occurrence inside a fenced code block also matches; rewrite the example so it does not carry the real id)"
			}
			return errors.New(msg)
		}
	}
	return fmt.Errorf("refreshing the data region of output %q: %w", name, err)
}

// regionContent renders the payload as the region's interior in the document's
// own grammar.
//
// An HTML island holds the JSON verbatim; the serializer's \u escaping of '<'
// means it can never terminate the enclosing element. A JSX document is a
// module, where bare JSON between tags would parse as a brace expression and
// break the component — so the payload is wrapped as a template-literal
// expression child, escaped so that evaluating the literal yields exactly the
// serialized JSON. Either way the element's rendered text content IS the
// payload, which is what the dashboard's own code reads back.
func regionContent(kind string, payload []byte) string {
	if kind != contenttype.JSX {
		return string(payload)
	}
	return "{`" + templateLiteralEscaper.Replace(string(payload)) + "`}"
}

// templateLiteralEscaper escapes a string for the inside of a JavaScript
// template literal in one pass: evaluating the literal yields the input
// exactly, and none of backslash, backtick, or ${ survives to close the
// literal or open an interpolation.
var templateLiteralEscaper = strings.NewReplacer("\\", "\\\\", "`", "\\`", "${", "\\${")

// writeRefreshedVersion stores the spliced document as the asset's next
// version, through the same store step an export's write uses: an immutable
// per-run object and a version row whose summary names the script version and
// run that produced it.
func (w *outputWriter) writeRefreshedVersion(ctx context.Context, asset *portal.Asset, body string) (int, error) {
	summary := fmt.Sprintf("data refresh: %s v%d, run %s", w.script.Name, w.run.Version, w.run.ID)
	version, err := w.storeVersion(ctx, asset.ID, scriptrun.OutputIdentity{
		ContentType: asset.ContentType,
		Extension:   contenttype.Extension(asset.ContentType),
	}, []byte(body), summary)
	if err != nil {
		return 0, fmt.Errorf("writing the refreshed version of output %q: %w", asset.Name, err)
	}
	return version, nil
}
