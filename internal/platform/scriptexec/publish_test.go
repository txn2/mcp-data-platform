package scriptexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// htmlDashboard is the data-island shape #1389 refreshes: markup around one
// marked region whose interior holds the data the page renders from.
const htmlDashboard = `<html><body>
<h1>Revenue</h1>
<script type="application/json" id="data">{"seeded": true}</script>
<div id="chart"></div>
</body></html>`

// publishHarness seeds one existing output asset — the way a prior run's
// export leaves it — and returns the harness ready to refresh it.
func publishHarness(t *testing.T, name, format, body string) writerHarness { //nolint:unparam // one output name is all these cases need; the parameter names what it is
	t.Helper()
	h := newWriterHarness(t)
	_, err := h.writer.Export(context.Background(), scriptrun.ExportRequest{
		Name: name, Format: format, Body: &body, Destination: script.PortalDestination(),
	})
	require.NoError(t, err)
	// The seeding export is a prior RUN's write, not part of the refresh under
	// test: reset the run row and the attempt bookkeeping it recorded.
	h.run.Outputs = nil
	h.writer.written = map[string]bool{}
	return h
}

// publishRequest is one refresh in the shape the engine hands over.
func publishRequest(name string, data any) scriptrun.PublishRequest { //nolint:unparam // one output name is all these cases need; the parameter names what it is
	return scriptrun.PublishRequest{Name: name, Data: data}
}

func TestPublishData_RefreshesTheRegionAndNothingElse(t *testing.T) {
	// Acceptance: the refresh creates a new version whose bytes differ from the
	// prior version ONLY inside the marked element, and the run records it.
	h := publishHarness(t, "dash", "html", htmlDashboard)

	res, err := h.writer.PublishData(context.Background(), publishRequest("dash",
		map[string]any{"regions": []any{map[string]any{"name": "west"}}}))
	require.NoError(t, err)
	assert.Equal(t, 2, res.AssetVersion, "the refresh is the asset's next version")

	require.Len(t, h.versions.created, 2)
	refreshed := string(h.s3.objects[h.versions.created[1].S3Key])
	assert.Contains(t, refreshed, `"regions"`)
	assert.NotContains(t, refreshed, `"seeded"`)
	// Everything outside the island is byte-for-byte the author's.
	assert.Contains(t, refreshed, "<h1>Revenue</h1>")
	assert.Contains(t, refreshed, `<div id="chart"></div>`)
	assert.True(t, strings.HasPrefix(refreshed, "<html><body>"))

	payload, ferr := scriptrun.FormatDataPayload("dash", map[string]any{"regions": []any{map[string]any{"name": "west"}}})
	require.NoError(t, ferr)
	assert.Equal(t, len(payload), res.Bytes, "the record carries the payload size, not the document's")

	require.Len(t, h.run.Outputs, 1)
	out := h.run.Outputs[0]
	assert.True(t, out.Refresh)
	assert.Equal(t, "json", out.Format)
	assert.Equal(t, 2, out.AssetVersion)
	assert.Equal(t, script.DestinationPortal, out.Destination)
	assert.Contains(t, h.versions.created[1].ChangeSummary, "data refresh")
}

func TestPublishData_JSXPayloadIsAnEscapedTemplateLiteral(t *testing.T) {
	// A JSX module cannot hold bare JSON between tags — braces open an
	// expression — so the payload lands as a template-literal expression child
	// whose evaluation yields exactly the serialized JSON.
	jsx := `export default function Dash() {
  return (<div><script type="application/json" id="data">{` + "`{}`" + `}</script></div>);
}`
	h := publishHarness(t, "dash", "jsx", jsx)

	data := map[string]any{"note": "tick ` and ${brace} and back\\slash"}
	_, err := h.writer.PublishData(context.Background(), publishRequest("dash", data))
	require.NoError(t, err)

	refreshed := string(h.s3.objects[h.versions.created[1].S3Key])
	start := strings.Index(refreshed, `id="data">`) + len(`id="data">`)
	end := strings.Index(refreshed, "</script>")
	island := refreshed[start:end]
	require.True(t, strings.HasPrefix(island, "{`") && strings.HasSuffix(island, "`}"),
		"the island is a template-literal expression child: %q", island)

	// Un-escaping the literal reproduces the exact serialized payload.
	literal := island[2 : len(island)-2]
	unescaped := strings.NewReplacer("\\${", "${", "\\`", "`", `\\`, `\`).Replace(literal)
	payload, ferr := scriptrun.FormatDataPayload("dash", data)
	require.NoError(t, ferr)
	assert.Equal(t, string(payload), unescaped)
}

func TestPublishData_MarkdownCarriesTheIslandAsRawHTML(t *testing.T) {
	// A markdown dashboard marks its region as a raw-HTML block — legal
	// markdown — and the refresh resolves it with the element grammar even
	// though the document's own region grammar is headings.
	md := "# Revenue\n\n<script type=\"application/json\" id=\"data\">{}</script>\n\nProse below.\n"
	h := publishHarness(t, "dash", "markdown", md)

	_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
	require.NoError(t, err)

	refreshed := string(h.s3.objects[h.versions.created[1].S3Key])
	assert.Contains(t, refreshed, `"a": 1`)
	assert.Contains(t, refreshed, "# Revenue")
	assert.Contains(t, refreshed, "Prose below.")
}

func TestPublishData_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T) writerHarness
		wantErr string
	}{
		{
			name:    "no asset to refresh",
			seed:    newWriterHarness,
			wantErr: "no output asset named \"dash\" to refresh",
		},
		{
			name: "wrong document kind",
			seed: func(t *testing.T) writerHarness {
				t.Helper()
				return publishHarness(t, "dash", "text", "just prose")
			},
			wantErr: "only an html, jsx, or markdown document",
		},
		{
			name: "no marked region",
			seed: func(t *testing.T) writerHarness {
				t.Helper()
				return publishHarness(t, "dash", "html", "<html><body><h1>No island</h1></body></html>")
			},
			wantErr: "no element matches #data",
		},
		{
			name: "more than one region",
			seed: func(t *testing.T) writerHarness {
				t.Helper()
				return publishHarness(t, "dash", "html",
					`<div><b id="data">x</b><i id="data">y</i></div>`)
			},
			wantErr: "more than one element",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.seed(t)
			before := len(h.versions.created)
			_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Len(t, h.versions.created, before, "a refused refresh writes no version")
			assert.Empty(t, h.run.Outputs, "a refused refresh records nothing on the run")
		})
	}
}

func TestPublishData_MarkdownFenceIsNotTheDataRegion(t *testing.T) {
	// An id="data" quoted inside a fenced code block is example text, not the
	// region. A fence-only document is refused naming the fence, and a document
	// carrying both the island and a fenced example is refused with the actual
	// collision rather than advice about ids the author cannot follow.
	t.Run("only a fenced example", func(t *testing.T) {
		md := "# T\n\n```html\n<script type=\"application/json\" id=\"data\">{}</script>\n```\n"
		h := publishHarness(t, "dash", "markdown", md)
		_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fenced code block")
		assert.Empty(t, h.run.Outputs, "nothing was written")
	})
	t.Run("island plus a fenced example", func(t *testing.T) {
		md := "# T\n\n<script type=\"application/json\" id=\"data\">{}</script>\n\n```html\n<script type=\"application/json\" id=\"data\">{}</script>\n```\n"
		h := publishHarness(t, "dash", "markdown", md)
		_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inside a fenced code block also matches")
	})
}

func TestPublishData_StoreLookupFailureIsNotAMissingAsset(t *testing.T) {
	// A transient store error must not become the terminal, false statement
	// that the dashboard does not exist and should be re-published.
	h := publishHarness(t, "dash", "html", htmlDashboard)
	h.assets.lookupErr = errors.New("connection refused")
	_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving output \"dash\": connection refused")
	assert.NotContains(t, err.Error(), "has no output asset")
}

func TestPublishData_MissingAssetErrorSaysHowToCreateIt(t *testing.T) {
	h := newWriterHarness(t)
	_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform.export(\"dash\", body, format=\"html\")")
}

func TestPublishData_ReclaimedRunDoesNotRefreshTwice(t *testing.T) {
	// A prior attempt's recorded output answers the retry; nothing is written.
	h := publishHarness(t, "dash", "html", htmlDashboard)
	first, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
	require.NoError(t, err)

	reclaimed := newOutputWriter(h.writer.deps, h.runs, h.run, h.writer.script, h.caller)
	versionsBefore := len(h.versions.created)
	again, err := reclaimed.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
	require.NoError(t, err)
	assert.Equal(t, first.AssetVersion, again.AssetVersion)
	assert.Len(t, h.versions.created, versionsBefore, "the reclaimed attempt wrote nothing new")

	// And within one attempt, a second refresh of the same name is the script
	// bug the once-per-destination rule exists for.
	_, err = reclaimed.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(2)}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already written")
}

func TestPublishData_StoreFailuresFailTheRefresh(t *testing.T) {
	t.Run("upload fails", func(t *testing.T) {
		h := publishHarness(t, "dash", "html", htmlDashboard)
		h.s3.putErr = errors.New("bucket unavailable")
		_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing the refreshed version of output \"dash\": uploading the object")
	})
	t.Run("version record fails", func(t *testing.T) {
		h := publishHarness(t, "dash", "html", htmlDashboard)
		h.versions.createErr = errors.New("db down")
		_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{"a": int64(1)}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing the refreshed version of output \"dash\": recording the version")
		assert.Empty(t, h.run.Outputs, "a refresh that did not record did not happen")
	})
}

func TestPublishData_RequiresTheStores(t *testing.T) {
	h := newWriterHarness(t)
	h.writer.deps.S3 = nil
	_, err := h.writer.PublishData(context.Background(), publishRequest("dash", map[string]any{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no portal asset store or object storage")
}

// TestPublishData_EndToEndThroughTheInterpreter proves the assembled path: a
// real Starlark script, the real host binding, the real writer, and the
// version the refresh produced — not each piece with hand-built inputs.
func TestPublishData_EndToEndThroughTheInterpreter(t *testing.T) {
	h := publishHarness(t, "dash", "html", htmlDashboard)

	result, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: `res = platform.publish_data("dash", {"regions": [{"name": "west", "total": 12}]})
print("v%d" % res["asset_version"])`,
		Name: "test", RunID: h.run.ID,
		Exporter: h.writer,
	})
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	record := result.Exports[0]
	assert.True(t, record.Refresh)
	assert.Equal(t, 2, record.AssetVersion)
	assert.Contains(t, result.Log, "v2")

	refreshed := string(h.s3.objects[h.versions.created[1].S3Key])
	assert.Contains(t, refreshed, `"regions"`)
	assert.Contains(t, refreshed, "<h1>Revenue</h1>")
	require.Len(t, h.run.Outputs, 1)
	assert.True(t, h.run.Outputs[0].Refresh)
}

func TestInMarkdownFence(t *testing.T) {
	doc := "line1\n```\nfenced backtick\n```\nline5\n~~~\ntilde fenced\n```\nstill tilde fenced\n~~~\nline11\n"
	tests := map[int]bool{
		1:  false, // plain text
		3:  true,  // inside a backtick fence
		5:  false, // after it closed
		7:  true,  // inside a tilde fence
		9:  true,  // a ``` line inside a tilde fence is content, not a close
		11: false, // after the tilde fence closed
	}
	for line, want := range tests {
		assert.Equal(t, want, inMarkdownFence(doc, line), "line %d", line)
	}
	assert.False(t, inMarkdownFence(doc, 99), "past the document is not in a fence")
}
