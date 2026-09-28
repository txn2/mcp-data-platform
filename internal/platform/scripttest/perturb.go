package scripttest

import (
	"context"
	"encoding/json"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// insensitive re-runs a test that passed against its recording with the
// recorded query results altered, and says why the test proves nothing when
// it passes against every alteration: its assertions do not depend on the
// data the script was given. It is mutation testing on the recorded answers
// rather than on the code, and it is cheap for the reason a test is: every
// run is answered from memory.
//
// Two alterations are tried, each changing every query result that has rows:
// the last row dropped, and every value of the first row changed. A test that
// fails, or whose script fails, under either one depends on the data. The
// altered run answers a call whose arguments moved with the rows with the
// answer its tool gave in the recording (scriptrec.Replay.Lenient). A
// recording holding no query rows is not altered, and the test stands.
func insensitive(ctx context.Context, req Request, name string, rec *scriptrec.Recording) string {
	alterations := []func([]any) []any{dropLastRow, changeFirstRow}
	altered := 0
	for _, alter := range alterations {
		changed, ok := alterRecording(rec, alter)
		if !ok {
			return ""
		}
		out, _, err := execute(ctx, req, execution{
			entry: name, recording: rec.RunID, rec: changed, replay: scriptrec.NewReplay(changed).Lenient(),
		})
		if err != nil || out.asserts == 0 {
			return ""
		}
		altered++
	}
	if altered == 0 {
		return ""
	}
	return name + " still passes when the recorded query results change (the last row dropped, or the first " +
		"row's values changed), so its assertions do not read what the script produced from them; assert on " +
		"the exports, state, notifications or result the rows lead to"
}

// alterRecording is a copy of rec with alter applied to the rows of every
// query result that has any, and whether there was one.
func alterRecording(rec *scriptrec.Recording, alter func([]any) []any) (*scriptrec.Recording, bool) {
	out := &scriptrec.Recording{Header: rec.Header, Calls: make([]scriptrec.Call, len(rec.Calls))}
	found := false
	for i, c := range rec.Calls {
		out.Calls[i] = c
		rows, ok := c.Out["rows"].([]any)
		if c.Tool != scriptrun.ToolQuery || !ok || len(rows) == 0 {
			continue
		}
		copied := cloneOut(c.Out)
		altered := alter(cloneRows(rows))
		copied["rows"] = altered
		if stats, ok := copied["stats"].(map[string]any); ok {
			stats["row_count"] = float64(len(altered))
		}
		if _, ok := copied["row_count"]; ok {
			copied["row_count"] = float64(len(altered))
		}
		out.Calls[i].Out = copied
		found = true
	}
	return out, found
}

func dropLastRow(rows []any) []any { return rows[:len(rows)-1] }

// changeFirstRow gives every value of the first row another value of its
// kind.
func changeFirstRow(rows []any) []any {
	row, ok := rows[0].(map[string]any)
	if !ok {
		return rows
	}
	for k, v := range row {
		switch v := v.(type) {
		case float64:
			row[k] = v + 1
		case string:
			row[k] = v + "~"
		case bool:
			row[k] = !v
		}
	}
	return rows
}

// cloneOut and cloneRows copy a recorded answer so an alteration never reaches
// the recording the test itself replays.
func cloneOut(m map[string]any) map[string]any {
	var out map[string]any
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, &out)
	return out
}

func cloneRows(rows []any) []any {
	var out []any
	data, _ := json.Marshal(rows)
	_ = json.Unmarshal(data, &out)
	return out
}
