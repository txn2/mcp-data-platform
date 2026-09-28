package scripttest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/toolanswer"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// documentedBlock is the fenced Python block of docs/scripts/running.md that
// defines fn.
func documentedBlock(t *testing.T, fn string) string {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/scripts/running.md")
	require.NoError(t, err)
	for _, block := range strings.Split(string(raw), "```python\n")[1:] {
		body, _, _ := strings.Cut(block, "```")
		if strings.Contains(body, "def "+fn+"(") {
			return body
		}
	}
	t.Fatalf("no documented block defines %s", fn)
	return ""
}

// dailyRows answers the documented script's query with two regions, and any
// other call with nothing, which the draft never reaches.
type dailyRows struct{}

func (dailyRows) CallTool(_ context.Context, name string, _ map[string]any) (map[string]any, error) {
	if name != scriptrun.ToolQuery {
		return map[string]any{}, nil
	}
	return map[string]any{"columns": []any{"region", "n"}, "rows": []any{
		map[string]any{"region": "east", "n": 1.0},
		map[string]any{"region": "west", "n": 2.0},
	}}, nil
}

// The documentation's declared-answer example (#1953) does what the page says:
// its draft stops at the create and records the query before it, and its
// tests pass against that recording with the create answered as the
// manage_resource contract requires, reading every output they produce.
func TestTheDocumentedDeclaredAnswerExamplePasses(t *testing.T) {
	source := documentedBlock(t, "test_files_the_extract")
	fire := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	recorder := scriptrec.NewRecorder(scriptrec.Header{RunID: "dpx_recording_of_a_draft", FireTime: fire, MaxRows: scriptrun.DraftMaxRows, Preview: true})
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: source, Name: "daily", RunID: "dpx_recording_of_a_draft", FireTime: fire,
		Caller: dailyRows{}, OnCall: recorder.OnCall, Writes: scriptrun.WritesRefused,
	})
	require.ErrorContains(t, err, "manage_resource", "the draft stops at the create")
	data, reason := recorder.Finish()
	require.Empty(t, reason)
	rec, err := scriptrec.Decode(data)
	require.NoError(t, err)
	require.Len(t, rec.Calls, 1, "the recording holds the query and nothing after the write")

	report, err := Run(context.Background(), Request{
		Source: source, Name: "daily",
		Contracts: toolanswer.New((&portal.Toolkit{}).AnswerContracts()...),
		Load:      func(context.Context, string) (*scriptrec.Recording, error) { return rec, nil },
	})
	require.NoError(t, err)
	require.Len(t, report.Tests, 2)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, "%s: %s (line %d)", r.Name, r.Failure, r.Line)
		assert.Empty(t, r.Notes, "the answer was checked against the manage_resource contract")
	}
	assert.Empty(t, report.Unread)
	assert.GreaterOrEqual(t, report.Coverage.Percent, 80.0)
}
