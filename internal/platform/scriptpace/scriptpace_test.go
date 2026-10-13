package scriptpace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

var sleepBuiltin = starlark.NewBuiltin(SleepCapability, nil)

func sleep(c Clock, args ...starlark.Value) error {
	_, err := c.Sleep(nil, sleepBuiltin, args, nil)
	return err
}

// TestSleepInATestReturnsAtOnce is #2112's "a test of that script runs
// instantly": an hour of polling is a few hundred calls that wait for nothing.
func TestSleepInATestReturnsAtOnce(t *testing.T) {
	c := Clock{Ctx: context.Background(), Testing: true}
	start := time.Now()
	for range 300 {
		require.NoError(t, sleep(c, starlark.MakeInt(10)))
	}
	assert.Less(t, time.Since(start), time.Second)
}

func TestSleepRefusesWhatItCannotWait(t *testing.T) {
	c := Clock{Ctx: context.Background(), Testing: true}
	cases := map[string]struct {
		args []starlark.Value
		want string
	}{
		"zero":         {[]starlark.Value{starlark.MakeInt(0)}, "greater than zero"},
		"negative":     {[]starlark.Value{starlark.Float(-1)}, "greater than zero"},
		"a string":     {[]starlark.Value{starlark.String("10")}, "must be a number"},
		"over the cap": {[]starlark.Value{starlark.MakeInt(61)}, "loop of shorter calls"},
		"huge":         {[]starlark.Value{starlark.MakeInt(1).Lsh(70)}, "more than one call waits"},
		"no argument":  {nil, "got 0 arguments, want 1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := sleep(c, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestSleepRefusesToPassTheDeadline: a wait the run cannot finish is refused
// before it starts, naming the time left.
func TestSleepRefusesToPassTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := sleep(Clock{Ctx: ctx}, starlark.MakeInt(10))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would pass the run's deadline")
}

func TestSleepStopsWhenTheRunEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleep(Clock{Ctx: ctx}, starlark.MakeInt(30))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "err = %v", err)
}

func TestSleepWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	require.NoError(t, sleep(Clock{Ctx: ctx}, starlark.Float(0.02)))
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

var remainingBuiltin = starlark.NewBuiltin("platform.remaining_ms", nil)

func TestRemainingMS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var recorded map[string]any
	c := Clock{Ctx: ctx, Record: func(tool string, _, out map[string]any, _ error) {
		assert.Equal(t, remainingKey, tool)
		recorded = out
	}}
	v, err := c.RemainingMS(nil, remainingBuiltin, nil, nil)
	require.NoError(t, err)
	ms, _ := starlark.AsInt32(v)
	assert.Greater(t, ms, 110*1000)
	assert.Equal(t, int64(ms), recorded["remaining_ms"])

	v, err = Clock{Ctx: context.Background()}.RemainingMS(nil, remainingBuiltin, nil, nil)
	require.NoError(t, err)
	hour, _ := starlark.AsInt32(v)
	assert.Equal(t, int(time.Hour.Milliseconds()), hour, "no deadline reads as an hour")

	_, err = c.RemainingMS(nil, remainingBuiltin, starlark.Tuple{starlark.MakeInt(1)}, nil)
	assert.Error(t, err)
}

func TestRemainingMSInATest(t *testing.T) {
	pinned := int64(10)
	v, err := Clock{Testing: true, Pinned: func() *int64 { return &pinned }}.RemainingMS(nil, remainingBuiltin, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt64(10), v)

	recorded := func(answer map[string]any, err error) Clock {
		return Clock{
			Testing: true, Pinned: func() *int64 { return nil },
			Recorded: func(string) (map[string]any, error) { return answer, err },
		}
	}
	v, err = recorded(map[string]any{"remaining_ms": float64(42)}, nil).RemainingMS(nil, remainingBuiltin, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt64(42), v)
	v, err = recorded(map[string]any{"remaining_ms": int64(7)}, nil).RemainingMS(nil, remainingBuiltin, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt64(7), v)
	_, err = recorded(map[string]any{}, nil).RemainingMS(nil, remainingBuiltin, nil, nil)
	assert.ErrorContains(t, err, "holds no value")
	_, err = recorded(nil, errors.New("no more values")).RemainingMS(nil, remainingBuiltin, nil, nil)
	assert.ErrorContains(t, err, "testing.set_run(remaining_ms=")
	_, err = Clock{Testing: true}.RemainingMS(nil, remainingBuiltin, nil, nil)
	assert.ErrorContains(t, err, "no recording")
}
