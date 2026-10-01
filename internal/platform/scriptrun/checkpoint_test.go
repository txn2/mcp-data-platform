package scriptrun

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

type noCaller struct{}

func (noCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// TestRemainingMSIsRecordedLive holds #2004's live half: each read is the
// time left before the run's deadline -- its own time limit, under the
// caller's -- recorded the way a tool answer is.
func TestRemainingMSIsRecordedLive(t *testing.T) {
	var recorded []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	res, err := Run(ctx, Options{
		Source: "def main():\n    \"\"\"Reads the time left.\"\"\"\n    platform.result(platform.remaining_ms())\n",
		Name:   "t", Caller: noCaller{}, Timeout: 2 * time.Minute,
		OnCall: func(tool string, _, out map[string]any, _ error) {
			if tool == remainingKey {
				recorded = append(recorded, out)
			}
		},
	})
	require.NoError(t, err)
	require.Len(t, recorded, 1)
	ms, _ := recorded[0]["remaining_ms"].(int64)
	assert.Greater(t, ms, int64(110*1000), "the run's own limit, not the caller's hour")
	assert.LessOrEqual(t, ms, int64(120*1000))
	assert.NotEmpty(t, res.Return)
}

func TestCheckpointAndRemainingRefuseBadArguments(t *testing.T) {
	for name, src := range map[string]string{
		"checkpoint not a dict":   `platform.checkpoint([1])`,
		"checkpoint not JSON":     `platform.checkpoint({"f": len})`,
		"remaining with an arg":   `platform.remaining_ms(1)`,
		"checkpoint over the cap": `platform.checkpoint({"k": "x" * 70000})`,
	} {
		_, err := Run(context.Background(), Options{
			Source: "def main():\n    \"\"\"Calls it wrong.\"\"\"\n    " + src + "\n", Name: "t", Caller: noCaller{},
		})
		assert.Error(t, err, name)
	}
}

// TestRemainingMSInATestWithoutARecording names the way out.
func TestRemainingMSInATestWithoutARecording(t *testing.T) {
	h := &hostState{ctx: context.Background(), opts: Options{Test: &TestHooks{}, Caller: noCaller{}}}
	_, err := h.testRemaining(starlark.NewBuiltin(CapabilityRemainingMS, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testing.set_run(remaining_ms=")
}

// TestCheckpointIsHandedBack holds #2003's engine half: the last checkpoint
// is on the result, marked as one, beside a save_state staged separately.
func TestCheckpointIsHandedBack(t *testing.T) {
	res, err := Run(context.Background(), Options{
		Source: "def main():\n    \"\"\"Checkpoints twice.\"\"\"\n" +
			"    platform.checkpoint({\"through\": 1})\n" +
			"    platform.checkpoint({\"through\": 2})\n",
		Name: "t", Caller: noCaller{},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Checkpoint)
	assert.True(t, res.Checkpoint.Checkpoint)
	assert.Equal(t, map[string]any{"through": int64(2)}, res.Checkpoint.Value, "the last one wins")
	assert.Nil(t, res.State, "a checkpoint is not a save_state")
}

// recordedValues is a test's Caller holding recorded host values.
type recordedValues struct {
	noCaller
	out map[string]any
	err error
}

func (r recordedValues) HostValue(string) (map[string]any, error) { return r.out, r.err }

// TestRemainingMSInATest covers where a test's remaining_ms comes from: the
// value testing.set_run set, else the recording, in either number form a
// recording decodes to, and a recording that holds nothing names the way out.
func TestRemainingMSInATest(t *testing.T) {
	set := int64(5)
	cases := []struct {
		name    string
		inputs  *TestInputs
		caller  Caller
		want    int64
		wantErr string
	}{
		{name: "set by the test", inputs: &TestInputs{RemainingMS: &set}, caller: noCaller{}, want: 5},
		{name: "a decoded recording", caller: recordedValues{out: map[string]any{"remaining_ms": float64(42)}}, want: 42},
		{name: "a live recording", caller: recordedValues{out: map[string]any{"remaining_ms": int64(7)}}, want: 7},
		{name: "a recording without the value", caller: recordedValues{out: map[string]any{}}, wantErr: "holds no value"},
		{name: "a recording that ran out", caller: recordedValues{err: assert.AnError}, wantErr: "testing.set_run(remaining_ms="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Run(context.Background(), Options{
				Source: "def main():\n    \"\"\"Reads the time left.\"\"\"\n    platform.result(platform.remaining_ms())\n",
				Name:   "t", Caller: tc.caller, Test: &TestHooks{Inputs: tc.inputs},
			})
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, strconv.FormatInt(tc.want, 10), string(res.Return))
		})
	}
}
