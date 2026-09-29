package thumbworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
)

type fakeScripts struct {
	mu        sync.Mutex
	work      []scripttiles.Work
	claimErr  error
	recorded  map[string]string
	versions  map[string]int
	failed    map[string]string
	holds     map[string]hold
	orphans   []scripttiles.Orphan
	forgotten []string
	// storeErr fails every write and the orphan listing.
	storeErr error
	// claimedAs and recordedAs are the renderer generations the worker
	// claimed and recorded with.
	claimedAs, recordedAs int
}

func newFakeScripts(work ...scripttiles.Work) *fakeScripts {
	return &fakeScripts{
		work: work, recorded: map[string]string{}, versions: map[string]int{},
		failed: map[string]string{}, holds: map[string]hold{},
	}
}

func (f *fakeScripts) Claim(_ context.Context, renderer int, _ time.Duration, _ int) ([]scripttiles.Work, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimedAs = renderer
	w := f.work
	f.work = nil
	return w, f.claimErr
}

func (f *fakeScripts) Record(_ context.Context, id string, version int, key string, renderer int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedAs = renderer
	f.recorded[id], f.versions[id] = key, version
	return f.storeErr
}

func (f *fakeScripts) RecordFailure(_ context.Context, id string, _ int, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = reason
	return f.storeErr
}

func (f *fakeScripts) Hold(_ context.Context, id string, h time.Duration, attempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds[id] = hold{hold: h, attempts: attempts}
	return f.storeErr
}

func (f *fakeScripts) Orphans(context.Context, int) ([]scripttiles.Orphan, error) {
	o := f.orphans
	f.orphans = nil
	return o, f.storeErr
}

func (f *fakeScripts) Forget(_ context.Context, id string) error {
	f.forgotten = append(f.forgotten, id)
	return f.storeErr
}

const flowSource = "rows = platform.query(\"SELECT 1\", connection=\"warehouse\")\nplatform.export(\"daily\", rows[\"rows\"], format=\"csv\")\n"

// A script's tile is its flow graph drawn in the tile page, light and dark,
// recorded against the version it was drawn from (#1909).
func TestDrawScript_TheFlowGraphLightAndDark(t *testing.T) {
	d, blobs := &fakeDrawer{}, newBlobs()
	w := worker(d, nil, blobs)
	w.deps.CollectionPrefix = "p"
	scripts := newFakeScripts(scripttiles.Work{ScriptID: "s1", Name: "daily", Version: 3, Source: flowSource})
	w.deps.Scripts = scripts

	require.True(t, w.drawBatch(context.Background(), w.claimScripts(context.Background())))
	require.Len(t, d.pages, 2)
	data := payload(t, d.pages[0])
	assert.Equal(t, FlowTileType, data["contentType"])
	var g struct {
		Nodes     []struct{ Title string } `json:"nodes"`
		Structure struct {
			Nodes []struct{ Kind string } `json:"nodes"`
		} `json:"structure"`
	}
	content, ok := data["content"].(string)
	require.True(t, ok, "the tile page is handed the graph as text")
	require.NoError(t, json.Unmarshal([]byte(content), &g))
	assert.Equal(t, "Query warehouse", g.Nodes[0].Title)
	assert.False(t, d.pages[0].Dark)
	assert.True(t, d.pages[1].Dark)
	assert.Equal(t, tileWidth, d.pages[0].Width, "a flow tile is laid out at the tile size")

	for _, v := range []string{scripttiles.VariantLight, scripttiles.VariantDark} {
		assert.Contains(t, blobs.objects, bucket+"/"+scripttiles.Key("p", "s1", v))
	}
	assert.Equal(t, "p/scripts/s1/tile.png", scripts.recorded["s1"])
	assert.Equal(t, 3, scripts.versions["s1"])
	// The tile draws the Structure view, and a tile drawn before it existed is
	// owed a new one: script tiles have their own generation (#1972).
	require.NotEmpty(t, g.Structure.Nodes)
	assert.Equal(t, "start", g.Structure.Nodes[0].Kind)
	assert.Equal(t, ScriptRenderer, scripts.claimedAs)
	assert.Equal(t, ScriptRenderer, scripts.recordedAs)
	assert.Greater(t, ScriptRenderer, Renderer)
}

// A version that does not parse is recorded as not drawable, with the parse
// error as the reason; nothing is drawn.
func TestDrawScript_ASourceThatDoesNotParseIsNotDrawable(t *testing.T) {
	d := &fakeDrawer{}
	w := worker(d, nil, newBlobs())
	scripts := newFakeScripts(scripttiles.Work{ScriptID: "s1", Version: 2, Source: "def f(:\n"})
	w.deps.Scripts = scripts
	w.drawBatch(context.Background(), w.claimScripts(context.Background()))
	assert.Empty(t, d.pages)
	assert.Contains(t, scripts.failed["s1"], "the source does not parse: line 1")
}

// A tile the page refuses is the script's, recorded; a renderer that did not
// answer holds the script back for the next pass.
func TestDrawScript_RefusedAndUnfinished(t *testing.T) {
	d := &fakeDrawer{results: []error{errors.New("nothing draws it")}}
	w := worker(d, nil, newBlobs())
	scripts := newFakeScripts(scripttiles.Work{ScriptID: "s1", Version: 1, Source: flowSource, Attempts: 1})
	w.deps.Scripts = scripts
	w.drawBatch(context.Background(), w.claimScripts(context.Background()))
	assert.Equal(t, "nothing draws it", scripts.failed["s1"])
	assert.Empty(t, scripts.recorded)

	blobs := newBlobs()
	blobs.putErr = errors.New("storage down")
	w = worker(&fakeDrawer{}, nil, blobs)
	scripts = newFakeScripts(scripttiles.Work{ScriptID: "s2", Version: 1, Source: flowSource, Attempts: 1})
	w.deps.Scripts = scripts
	w.drawBatch(context.Background(), w.claimScripts(context.Background()))
	assert.Contains(t, scripts.holds, "s2", "storage that did not answer is not the script's")
	assert.Empty(t, scripts.failed)
}

// A deleted script's stored tiles are removed, then its row.
func TestClaimScripts_SweepsDeletedScriptsTiles(t *testing.T) {
	blobs := newBlobs()
	blobs.objects[bucket+"/p/scripts/gone/tile.png"] = []byte("x")
	blobs.objects[bucket+"/p/scripts/gone/tile-dark.png"] = []byte("x")
	w := worker(&fakeDrawer{}, nil, blobs)
	scripts := newFakeScripts()
	scripts.orphans = []scripttiles.Orphan{{ScriptID: "gone", Key: "p/scripts/gone/tile.png"}, {ScriptID: "never"}}
	w.deps.Scripts = scripts
	assert.Empty(t, w.claimScripts(context.Background()))
	assert.Empty(t, blobs.objects)
	assert.Equal(t, []string{"gone", "never"}, scripts.forgotten)

	w.deps.Scripts = nil
	assert.Nil(t, w.claimScripts(context.Background()))
}

// A tile that cannot be removed keeps its row for a later pass; a store that
// cannot list or forget is logged and the pass goes on to claim.
func TestClaimScripts_ASweepThatFailsIsTriedAgain(t *testing.T) {
	blobs := newBlobs()
	blobs.delErr = errors.New("storage down")
	w := worker(&fakeDrawer{}, nil, blobs)
	scripts := newFakeScripts()
	scripts.orphans = []scripttiles.Orphan{{ScriptID: "gone", Key: "p/scripts/gone/tile.png"}}
	w.deps.Scripts = scripts
	w.claimScripts(context.Background())
	assert.Empty(t, scripts.forgotten, "a tile still stored keeps the row naming it")

	scripts = newFakeScripts(scripttiles.Work{ScriptID: "s1", Version: 1, Source: flowSource})
	scripts.storeErr = errors.New("database down")
	w = worker(&fakeDrawer{}, nil, newBlobs())
	w.deps.Scripts = scripts
	assert.Len(t, w.claimScripts(context.Background()), 1, "a failed sweep does not stop the claim")
	scripts.orphans = []scripttiles.Orphan{{ScriptID: "never"}}
	scripts.storeErr = nil
	scripts.work = nil
	w.sweepScripts(context.Background())
	assert.Equal(t, []string{"never"}, scripts.forgotten)
}

// A store that cannot record the outcome is logged; the job still ends, and
// the hold and the refusal reach the store under the script's id.
func TestScriptJob_OutcomesReachTheStore(t *testing.T) {
	scripts := newFakeScripts()
	scripts.storeErr = errors.New("database down")
	w := worker(&fakeDrawer{}, nil, newBlobs())
	w.deps.Scripts = scripts
	s := scripttiles.Work{ScriptID: "s1", Version: 4, Source: flowSource}
	j := w.scriptJob(s)
	require.Error(t, j.hold(context.Background(), time.Minute, 2))
	assert.Equal(t, hold{hold: time.Minute, attempts: 2}, scripts.holds["s1"])
	require.Error(t, j.fail(context.Background(), "refused"))
	assert.Equal(t, "refused", scripts.failed["s1"])

	require.NoError(t, w.drawScript(context.Background(), s), "an unrecorded tile is logged, not retried")
	require.NoError(t, w.drawScript(context.Background(), scripttiles.Work{ScriptID: "s2", Source: "def f(:\n"}))
	assert.Contains(t, scripts.failed["s2"], "does not parse")
}

func TestParseReason(t *testing.T) {
	assert.Equal(t, "the source does not parse", parseReason(scriptflow.Graph{}))
	assert.Equal(t, "the source does not parse: boom",
		parseReason(scriptflow.Graph{Findings: []scriptrun.Finding{{Message: "boom"}}}))
	assert.Equal(t, "the source does not parse: line 3: boom",
		parseReason(scriptflow.Graph{Findings: []scriptrun.Finding{{Line: 3, Message: "boom"}}}))
}

// The tile page tells a flow graph by the content type the worker sends it
// as; the two constants are one value (#1909).
func TestFlowTileType_TheTilePageKnowsIt(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "ui", "src", "components", "thumbnail", "FlowTile.tsx"))
	require.NoError(t, err)
	assert.Contains(t, string(src), `export const FLOW_TILE_TYPE = "`+FlowTileType+`";`)
}
